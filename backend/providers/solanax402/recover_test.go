package solanax402

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/paychan"
)

// channelNode is a node that serves one payment channel and records what is sent.
type channelNode struct {
	mu      sync.Mutex
	channel solana.PublicKey
	data    []byte // nil: no such account
	sent    []string
}

func (n *channelNode) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params []any           `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n.mu.Lock()
		defer n.mu.Unlock()
		ctx := map[string]any{"slot": 1}
		var result any
		switch req.Method {
		case "getAccountInfo":
			if addr, _ := req.Params[0].(string); addr == n.channel.String() && n.data != nil {
				result = map[string]any{"context": ctx, "value": map[string]any{
					"lamports": 1, "owner": paychan.ProgramID.String(), "executable": false, "data": []string{base64.StdEncoding.EncodeToString(n.data), "base64"},
				}}
			} else {
				result = map[string]any{"context": ctx, "value": nil}
			}
		case "getLatestBlockhash":
			result = map[string]any{"context": ctx, "value": map[string]any{"blockhash": solana.EncodeBase58(make([]byte, 32)), "lastValidBlockHeight": 1_000_000}}
		case "sendTransaction":
			b64, _ := req.Params[0].(string)
			n.sent = append(n.sent, b64)
			result = solana.EncodeBase58(make([]byte, 64))
		case "getSignatureStatuses":
			result = map[string]any{"context": ctx, "value": []any{map[string]any{"slot": 1, "confirmations": nil, "err": nil, "confirmationStatus": "finalized"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// instructionsSent are the payment-channel program's instruction numbers in
// everything sent, in order.
func (n *channelNode) instructionsSent(t *testing.T) []byte {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []byte
	for _, b64 := range n.sent {
		tx, err := solana.DecodeTransactionBase64(b64)
		if err != nil {
			t.Fatal(err)
		}
		for _, ix := range tx.Message.Instructions {
			if tx.Message.AccountKeys[ix.ProgramIDIndex] == paychan.ProgramID && len(ix.Data) > 0 {
				out = append(out, ix.Data[0])
			}
		}
	}
	return out
}

// channelBytes lays a channel out as DecodeChannel reads it.
func channelBytes(c paychan.Channel) []byte {
	b := make([]byte, paychan.ChannelSize)
	le := binary.LittleEndian
	b[0], b[1], b[3] = 1, c.Version, byte(c.Status)
	le.PutUint64(b[4:], c.Salt)
	le.PutUint64(b[12:], c.Deposit)
	le.PutUint64(b[20:], c.Settled)
	le.PutUint64(b[28:], c.PayoutWatermark)
	le.PutUint64(b[36:], uint64(c.ClosureStartedAt))
	le.PutUint64(b[44:], uint64(c.PayerWithdrawnAt))
	le.PutUint32(b[52:], c.GracePeriod)
	copy(b[88:], c.Payer[:])
	copy(b[120:], c.Payee[:])
	copy(b[152:], c.AuthorizedSigner[:])
	copy(b[184:], c.Mint[:])
	copy(b[216:], c.RentPayer[:])
	le.PutUint64(b[248:], c.OpenSlot)
	return b
}

type recoverRig struct {
	rail  *Rail
	node  *channelNode
	ev    econ.Evidence
	payer *solana.Keypair
	now   time.Time
}

func newRecoverRig(t *testing.T, c paychan.Channel) *recoverRig {
	t.Helper()
	payer, _ := solana.NewKeypair()
	ch, _ := solana.NewKeypair()
	mintStr, _ := chain.AssetAddress(chain.SolanaDevnet, "USDC")
	c.Payer = payer.PublicKey()
	c.Mint = solana.MustPublicKey(mintStr)
	node := &channelNode{channel: ch.PublicKey(), data: channelBytes(c)}
	rg := &recoverRig{node: node, payer: payer, now: time.Unix(2_000_000, 0)}
	rail, err := New(Config{Cluster: "devnet", RPC: solana.NewRPC(node.serve(t), nil), Signer: payer, Now: func() time.Time { return rg.now }})
	if err != nil {
		t.Fatal(err)
	}
	rg.rail = rail
	rg.ev = econ.Evidence{Channel: ch.PublicKey().String(), Payer: payer.PublicKey().String(), Network: chain.SolanaDevnet}
	return rg
}

func (rg *recoverRig) recover(t *testing.T, notBefore time.Time) string {
	t.Helper()
	did, err := rg.rail.RecoverEscrow(context.Background(), rg.ev, notBefore)
	if err != nil {
		t.Fatal(err)
	}
	return did
}

func TestRecoverEscrowWalksTheProgramsExit(t *testing.T) {
	const grace = 60
	t.Run("an open channel is left alone until the provider has had its time", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusOpen, Deposit: 50_000})
		if did := rg.recover(t, rg.now.Add(time.Minute)); did != "" || len(rg.node.sent) != 0 {
			t.Fatalf("too early: %q, sent %d", did, len(rg.node.sent))
		}
	})
	t.Run("then the payer asks for the close", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusOpen, Deposit: 50_000})
		if did := rg.recover(t, rg.now.Add(-time.Minute)); !strings.Contains(did, "close") {
			t.Fatalf("did %q", did)
		}
		if got := rg.node.instructionsSent(t); len(got) != 1 || got[0] != 5 {
			t.Fatalf("want one request-close (5), sent %v", got)
		}
	})
	t.Run("a closing channel waits out its grace period, then is sealed", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusClosing, Deposit: 50_000, GracePeriod: grace, ClosureStartedAt: 2_000_000 - 20})
		if did := rg.recover(t, rg.now); did != "" || len(rg.node.sent) != 0 {
			t.Fatalf("within the grace period: %q", did)
		}
		rg.node.data = channelBytes(paychan.Channel{Status: paychan.StatusClosing, Deposit: 50_000, GracePeriod: grace, ClosureStartedAt: 2_000_000 - 200, Payer: rg.payer.PublicKey()})
		if did := rg.recover(t, rg.now); !strings.Contains(did, "sealed") {
			t.Fatalf("did %q", did)
		}
		if got := rg.node.instructionsSent(t); len(got) != 1 || got[0] != 6 {
			t.Fatalf("want one seal (6), sent %v", got)
		}
	})
	t.Run("a sealed channel's remainder is withdrawn once, then it is done", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusSealed, Deposit: 50_000})
		if did := rg.recover(t, rg.now); !strings.Contains(did, "withdrew") {
			t.Fatalf("did %q", did)
		}
		if got := rg.node.instructionsSent(t); len(got) != 1 || got[0] != 8 {
			t.Fatalf("want one withdraw (8), sent %v", got)
		}
		rg.node.data = channelBytes(paychan.Channel{Status: paychan.StatusSealed, Deposit: 50_000, PayerWithdrawnAt: 1_999_999, Payer: rg.payer.PublicKey()})
		if did := rg.recover(t, rg.now); did != "" || len(rg.node.sent) != 1 {
			t.Fatalf("already withdrawn: %q, sent %d", did, len(rg.node.sent))
		}
	})
	t.Run("a channel the provider settled and refunded needs nothing", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusDistributed, Deposit: 50_000, Settled: 2_240})
		if did := rg.recover(t, rg.now); did != "" || len(rg.node.sent) != 0 {
			t.Fatalf("did %q", did)
		}
	})
	t.Run("no account, no evidence: nothing to do", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusOpen, Deposit: 50_000})
		rg.node.data = nil
		if did := rg.recover(t, rg.now); did != "" {
			t.Fatalf("closed out already: %q", did)
		}
		rg.ev.Channel = ""
		if did := rg.recover(t, rg.now); did != "" {
			t.Fatalf("a payment that used no channel: %q", did)
		}
	})
	t.Run("another wallet's channel is never touched", func(t *testing.T) {
		rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusOpen, Deposit: 50_000})
		other, _ := solana.NewKeypair()
		rg.node.data = channelBytes(paychan.Channel{Status: paychan.StatusOpen, Deposit: 50_000, Payer: other.PublicKey()})
		if _, err := rg.rail.RecoverEscrow(context.Background(), rg.ev, rg.now.Add(-time.Hour)); err == nil || len(rg.node.sent) != 0 {
			t.Fatalf("want a refusal and nothing sent: %v, sent %d", err, len(rg.node.sent))
		}
	})
}

// Until the remainder is withdrawn, a sealed channel's money is not back: its
// payment is not final, so nothing is refunded to the vault from an empty payer.
func TestASealedChannelIsNotFinalUntilTheRemainderIsWithdrawn(t *testing.T) {
	rg := newRecoverRig(t, paychan.Channel{Status: paychan.StatusSealed, Deposit: 50_000})
	rg.ev.Scheme = paychan.Scheme
	rg.ev.PayTo = rg.payer.PublicKey().String() // any address: the channel's payee is not read here
	st, err := rg.rail.Settlement(context.Background(), rg.ev)
	if err != nil || st.Status != app.SettlementPending {
		t.Fatalf("sealed, not withdrawn: %+v %v", st, err)
	}
	rg.node.data = channelBytes(paychan.Channel{Status: paychan.StatusSealed, Deposit: 50_000, PayerWithdrawnAt: 1_999_999, Payer: rg.payer.PublicKey()})
	st, err = rg.rail.Settlement(context.Background(), rg.ev)
	if err != nil || st.Status != app.SettlementNotSettled {
		t.Fatalf("sealed and withdrawn with nothing settled: %+v %v", st, err)
	}
}
