package spendpass

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Signed is a transaction ready to send, and what's needed to prove later
// whether it landed: its signature is its identity, and past
// LastValidBlockHeight it can never land.
type Signed struct {
	Base64               string `json:"-"`
	Signature            string `json:"signature"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
}

// ComputeUnits is enough for any one instruction of this program (create
// with its vault is the largest, about 40k measured in LiteSVM).
const ComputeUnits = 80_000

// Sign builds a v0 transaction paid by feePayer, with a compute limit and a
// priority fee, and signs it with feePayer and others. Nothing is sent.
func Sign(ctx context.Context, rpc *solana.RPC, feePayer *solana.Keypair, others []*solana.Keypair, priceMicroLamports uint64, instrs ...solana.Instruction) (*Signed, error) {
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return nil, err
	}
	all := append([]solana.Instruction{solana.SetComputeUnitLimit(ComputeUnits), solana.SetComputeUnitPrice(priceMicroLamports)}, instrs...)
	msg, err := solana.CompileV0(feePayer.PublicKey(), all, bh.Blockhash)
	if err != nil {
		return nil, err
	}
	tx := solana.NewTransaction(msg)
	for _, k := range append([]*solana.Keypair{feePayer}, others...) {
		if err := tx.PartialSign(k); err != nil {
			return nil, err
		}
	}
	sig, _ := tx.SignerSignature(feePayer.PublicKey())
	return &Signed{Base64: tx.Base64(), Signature: solana.EncodeBase58(sig[:]), LastValidBlockHeight: bh.LastValidBlockHeight}, nil
}

// Unsigned builds a v0 transaction for a wallet to sign (the owner's
// instructions: create, deposit, freeze, withdraw, close). The wallet signs
// and sends it; Algebra never sees the owner's key.
func Unsigned(ctx context.Context, rpc *solana.RPC, feePayer solana.PublicKey, priceMicroLamports uint64, instrs ...solana.Instruction) (string, uint64, error) {
	bh, err := rpc.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return "", 0, err
	}
	all := append([]solana.Instruction{solana.SetComputeUnitLimit(ComputeUnits), solana.SetComputeUnitPrice(priceMicroLamports)}, instrs...)
	msg, err := solana.CompileV0(feePayer, all, bh.Blockhash)
	if err != nil {
		return "", 0, err
	}
	return solana.NewTransaction(msg).Base64(), bh.LastValidBlockHeight, nil
}

// ErrNeverLanded: the transaction's blockhash expired at a finalized height
// and it isn't on chain, so it never can be.
var ErrNeverLanded = errors.New("spendpass: the transaction expired without landing")

// ConfirmWait bounds how long Send waits for a transaction to land. A pull
// funds a payment someone is waiting for, so it is kept short; one that
// lands later is still found by Status and settled by reconciliation.
const ConfirmWait = 20 * time.Second

// Send submits a signed transaction (simulating first, so a pull the program
// would refuse fails here with its reason) and waits up to ConfirmWait until
// it reaches commitment, rebroadcasting it meanwhile so a dropped copy doesn't
// strand it. An error that isn't ErrNeverLanded, a refusal or a failure on
// chain means its fate is unknown: check Status before acting as if it didn't
// happen.
func Send(ctx context.Context, rpc *solana.RPC, s *Signed, commitment string) error {
	return SendWithin(ctx, rpc, s, commitment, ConfirmWait)
}

// SendWithin is Send with its own bound on the wait.
func SendWithin(ctx context.Context, rpc *solana.RPC, s *Signed, commitment string, wait time.Duration) error {
	if _, err := rpc.SendTransaction(ctx, s.Base64, solana.SendOptions{}); err != nil {
		return Explain(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	zero := 0
	for i := 1; ; i++ {
		sts, err := rpc.GetSignatureStatuses(waitCtx, []string{s.Signature}, false)
		if err == nil && len(sts) == 1 && sts[0] != nil {
			if sts[0].Failed() {
				return Explain(fmt.Errorf("%w: %s", solana.ErrTransactionFailed, string(sts[0].Err)))
			}
			if sts[0].Reached(commitment) {
				return nil
			}
		}
		select {
		case <-waitCtx.Done():
			if st, serr := Status(ctx, rpc, s); serr == nil {
				switch st {
				case Expired:
					return ErrNeverLanded
				case Landed:
					return nil
				case Failed:
					return fmt.Errorf("%w: %s", solana.ErrTransactionFailed, s.Signature)
				}
			}
			return fmt.Errorf("spendpass: %s not %s within %s", s.Signature, commitment, wait)
		case <-poll.C:
		}
		if i%2 == 0 {
			// The same signed bytes: it can land at most once.
			_, _ = rpc.SendTransaction(waitCtx, s.Base64, solana.SendOptions{SkipPreflight: true, MaxRetries: &zero})
		}
	}
}

// Fate is what the chain says about a signed transaction.
type Fate string

const (
	Landed  Fate = "landed"
	Failed  Fate = "failed"
	Pending Fate = "pending"
	// Expired: not on chain, and its blockhash is past a finalized height.
	Expired Fate = "expired"
)

// Status checks a signed transaction's fate from chain state alone.
func Status(ctx context.Context, rpc *solana.RPC, s *Signed) (Fate, error) {
	return StatusOf(ctx, rpc, s.Signature, s.LastValidBlockHeight)
}

// StatusOf is Status for a signature recorded earlier.
func StatusOf(ctx context.Context, rpc *solana.RPC, signature string, lastValid uint64) (Fate, error) {
	sts, err := rpc.GetSignatureStatuses(ctx, []string{signature}, true)
	if err != nil {
		return Pending, err
	}
	if len(sts) == 1 && sts[0] != nil {
		switch {
		case sts[0].Failed():
			return Failed, nil
		case sts[0].Reached(solana.Confirmed):
			return Landed, nil
		}
		return Pending, nil
	}
	h, err := rpc.GetBlockHeight(ctx, solana.Finalized)
	if err != nil {
		return Pending, err
	}
	if h <= lastValid {
		return Pending, nil
	}
	// Past its last valid height at a finalized commitment: check once more,
	// so a transaction that landed just before isn't missed.
	sts, err = rpc.GetSignatureStatuses(ctx, []string{signature}, true)
	if err != nil {
		return Pending, err
	}
	if len(sts) == 1 && sts[0] != nil {
		if sts[0].Failed() {
			return Failed, nil
		}
		return Landed, nil
	}
	return Expired, nil
}

var customError = regexp.MustCompile(`custom program error: 0x([0-9a-fA-F]+)|"Custom":\s*(\d+)`)

// Explain adds the program's own name for a custom error to err, when it
// carries one ("custom program error: 0x1771" becomes "... (OverPerCallCap)").
func Explain(err error) error {
	if err == nil {
		return nil
	}
	m := customError.FindStringSubmatch(err.Error())
	if m == nil {
		return err
	}
	var code uint64
	if m[1] != "" {
		code, _ = strconv.ParseUint(m[1], 16, 32)
	} else {
		code, _ = strconv.ParseUint(m[2], 10, 32)
	}
	if name := ErrorName(uint32(code)); name != "" {
		return &ProgramError{Code: uint32(code), Name: name, err: err}
	}
	return err
}

// ProgramError is a refusal by the Spend Pass program itself.
type ProgramError struct {
	Code uint32
	Name string
	err  error
}

func (e *ProgramError) Error() string {
	return fmt.Sprintf("spend pass program refused: %s (%d)", e.Name, e.Code)
}
func (e *ProgramError) Unwrap() error { return e.err }
