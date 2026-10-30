package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Made by the browser (frontend/lib/wallet/solana-tx.ts allowanceTx +
// signAsSecond) for the "abandon … about" vault, with a payer whose seed is 32
// bytes of 7, on devnet USDC, blockhash 32 bytes of 9: an approve of 5 USDC
// and a revoke, the payer's slot empty.
const (
	fixtureOwner   = "HAgk14JpMQLgt6rVgv7cBQFJWFto5Dqxi472uT3DKpqk"
	fixturePayer   = "GmaDrppBC7P5ARKV8g3djiwP89vz1jLK23V2GBjuAEGB"
	fixtureAccount = "Bjr2Shw9sy31UNdm8kYs5y72iTbnxB4JemK98vJqyQWm"
	fixtureMint    = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	fixtureApprove = "AgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB+AZX2rCNKYJUrU5lTbqzlSTjyfaG6I4Vz09wVfdrTEGP/WYmDaVlWE0E26+GYChdzRqRyrrW4QJiidTVihwwGAgEDBupKbGPinFIKvvVQexMuxfmVR3auvr57kkIe6mkURtIs8DYnYkanW53jNJ7UKxXiMvZRj8IPX81PHWToH5vSWPefjrIjpNyTLPvaeNDHbQBHTH0KYhj4Iv3veDKdy24JGjtELLORIVfxOpM9ATQoLQMrX/7NAaLb8bd5BgjfAC6nBt324ddloZPZy+FGzut5rBy0he1fWzeROoz1hX7/AKkDBkZv5SEXMv/srbpyw5vnvIzlu8X3EmssQ5s6QAAAAAkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJAwUABQIgTgAABQAJAxAnAAAAAAAABAQCAwABCg1AS0wAAAAAAAY="
	fixtureRevoke  = "AgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACuWFi3+qYhuR8JZMD0aVJRN2/5sXF5EHe/RFwlJ9d0dSVAOlqP10U4LhK5zSs04yx54Z2xslbFvDYkL8Xz7csNAgECBepKbGPinFIKvvVQexMuxfmVR3auvr57kkIe6mkURtIs8DYnYkanW53jNJ7UKxXiMvZRj8IPX81PHWToH5vSWPefjrIjpNyTLPvaeNDHbQBHTH0KYhj4Iv3veDKdy24JGgbd9uHXZaGT2cvhRs7reawctIXtX1s3kTqM9YV+/wCpAwZGb+UhFzL/7K26csOb57yM5bvF9xJrLEObOkAAAAAJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQMEAAUCIE4AAAQACQMQJwAAAAAAAAMCAgEBBQ=="
)

func fixturePayerKey(t *testing.T) *solana.Keypair {
	t.Helper()
	seed, _ := hex.DecodeString(strings.Repeat("07", 32))
	priv := ed25519.NewKeyFromSeed(seed)
	nums := make([]string, len(priv))
	for i, b := range priv {
		nums[i] = fmt.Sprint(b)
	}
	kp, err := solana.ParseKeypair("[" + strings.Join(nums, ",") + "]")
	if err != nil || kp.PublicKey().String() != fixturePayer {
		t.Fatalf("payer key: %v", err)
	}
	return kp
}

func decodeFixture(t *testing.T, b64 string) *solana.Transaction {
	t.Helper()
	tx, err := solana.DecodeTransactionBase64(b64)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestCheckAllowanceTx_ReadsTheBrowsersApproveAndRevoke(t *testing.T) {
	payer, owner, account, mint := solana.MustPublicKey(fixturePayer), solana.MustPublicKey(fixtureOwner), solana.MustPublicKey(fixtureAccount), solana.MustPublicKey(fixtureMint)
	if a, _ := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram); a != account {
		t.Fatalf("the browser's token account %s isn't Go's %s", fixtureAccount, a)
	}
	revoke, amount, err := checkAllowanceTx(decodeFixture(t, fixtureApprove), payer, owner, account, mint, DefaultAllowanceMaxMinor)
	if err != nil || revoke || amount != 5_000_000 {
		t.Fatalf("approve: revoke=%v amount=%d err=%v", revoke, amount, err)
	}
	revoke, _, err = checkAllowanceTx(decodeFixture(t, fixtureRevoke), payer, owner, account, mint, DefaultAllowanceMaxMinor)
	if err != nil || !revoke {
		t.Fatalf("revoke: %v %v", revoke, err)
	}
	// The vault signed the exact legacy bytes the payer will co-sign.
	for _, b64 := range []string{fixtureApprove, fixtureRevoke} {
		raw, _ := base64.StdEncoding.DecodeString(b64)
		rt, err := solana.ParseRawTransaction(raw)
		if err != nil {
			t.Fatal(err)
		}
		tx := decodeFixture(t, b64)
		if !ed25519.Verify(owner[:], rt.Message(), tx.Signatures[1][:]) {
			t.Fatal("the vault's signature doesn't verify over the message")
		}
	}
	// A ceiling below the amount refuses it.
	if _, _, err := checkAllowanceTx(decodeFixture(t, fixtureApprove), payer, owner, account, mint, 1_000_000); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("over the ceiling: %v", err)
	}
	// Another payer, or another vault, is refused.
	other, _ := solana.NewKeypair()
	if _, _, err := checkAllowanceTx(decodeFixture(t, fixtureApprove), other.PublicKey(), owner, account, mint, DefaultAllowanceMaxMinor); !errors.Is(err, ErrAllowanceTx) {
		t.Fatalf("other payer: %v", err)
	}
}

// What a vault (or a server pretending to be one) might try to get the payer
// to sign as fee payer: every one is refused.
func TestCheckAllowanceTx_RefusesAnythingButOneApproveOrRevoke(t *testing.T) {
	payer, _ := solana.NewKeypair()
	ownerKey, _ := solana.NewKeypair()
	p, owner := payer.PublicKey(), ownerKey.PublicKey()
	mint := solana.MustPublicKey(fixtureMint)
	account, _ := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram)
	payerUSDC, _ := solana.AssociatedTokenAddress(p, mint, solana.TokenProgram)
	thief, _ := solana.NewKeypair()

	approve := func(delegate solana.PublicKey, amount uint64) solana.Instruction {
		data := make([]byte, 10)
		data[0] = 13
		binary.LittleEndian.PutUint64(data[1:], amount)
		data[9] = chain.USDCDecimals
		return solana.Instruction{ProgramID: solana.TokenProgram, Data: data, Accounts: []solana.AccountMeta{
			{Pubkey: account, IsWritable: true}, {Pubkey: mint}, {Pubkey: delegate}, {Pubkey: owner, IsSigner: true}}}
	}
	good := approve(p, 5_000_000)
	cases := map[string][]solana.Instruction{
		"another delegate":            {approve(thief.PublicKey(), 5_000_000)},
		"the payer's USDC taken too":  {good, solana.TransferChecked(solana.TokenProgram, payerUSDC, mint, account, p, 1_000_000, 6)},
		"SOL taken from the payer":    {good, solana.SystemTransfer(p, thief.PublicKey(), 1_000_000)},
		"two approves":                {good, good},
		"no approve at all":           {solana.SetComputeUnitPrice(1)},
		"a priority fee far too high": {solana.SetComputeUnitPrice(maxAllowanceUnitPrice + 1), good},
		"compute far too high":        {solana.SetComputeUnitLimit(1_400_000), good},
		"a zero allowance":            {approve(p, 0)},
	}
	for name, ixs := range cases {
		m, err := solana.CompileV0(p, ixs, [32]byte{1})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		tx := solana.NewTransaction(m)
		if _, _, err := checkAllowanceTx(tx, p, owner, account, mint, DefaultAllowanceMaxMinor); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The same, well formed, is accepted (built by Go rather than the browser).
	m, _ := solana.CompileV0(p, []solana.Instruction{solana.SetComputeUnitLimit(allowanceComputeUnits), solana.SetComputeUnitPrice(10_000), good}, [32]byte{1})
	if _, amount, err := checkAllowanceTx(solana.NewTransaction(m), p, owner, account, mint, DefaultAllowanceMaxMinor); err != nil || amount != 5_000_000 {
		t.Fatalf("well formed: %d %v", amount, err)
	}
	// The fee payer must be the payer: a vault paying its own fee isn't one
	// the payer signs at all.
	m, _ = solana.CompileV0(owner, []solana.Instruction{good}, [32]byte{1})
	if _, _, err := checkAllowanceTx(solana.NewTransaction(m), p, owner, account, mint, DefaultAllowanceMaxMinor); err == nil {
		t.Fatal("a transaction the vault pays for was accepted")
	}
}

func TestAllowanceCode(t *testing.T) {
	payer, _ := solana.NewKeypair()
	other, _ := solana.NewKeypair()
	d, o := payer.PublicKey(), other.PublicKey()
	acct := func(amount, delegated uint64, delegate *solana.PublicKey) *solana.TokenAccount {
		return &solana.TokenAccount{Amount: amount, DelegatedAmount: delegated, Delegate: delegate, State: solana.TokenAccountInitialized}
	}
	for _, c := range []struct {
		name string
		ta   *solana.TokenAccount
		want string
	}{
		{"no USDC account", nil, DenyVaultUnfunded},
		{"enough", acct(10, 10, &d), ""},
		{"allowance below the payment", acct(10, 4, &d), DenyVaultAllowanceLow},
		{"another delegate", acct(10, 10, &o), DenyVaultAllowanceLow},
		{"no delegate (revoked or used up)", acct(10, 0, nil), DenyVaultAllowanceLow},
		{"USDC below the payment", acct(4, 10, &d), DenyVaultUnfunded},
		{"frozen", &solana.TokenAccount{Amount: 10, DelegatedAmount: 10, Delegate: &d, State: solana.TokenAccountFrozen}, DenyVaultUnfunded},
	} {
		if got := allowanceCode(c.ta, d, 5); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// allowanceNode is a Solana node for these tests: it keeps token accounts
// and applies ApproveChecked, Revoke and TransferChecked the way the token
// program does, delegate rules included, so what it refuses the chain would.
type allowanceNode struct {
	mu       sync.Mutex
	tokens   map[solana.PublicKey]*solana.TokenAccount
	statuses map[string]string // signature -> "ok" | "failed"
	sent     []*solana.Transaction
}

func newAllowanceNode() *allowanceNode {
	return &allowanceNode{tokens: map[solana.PublicKey]*solana.TokenAccount{}, statuses: map[string]string{}}
}

func (n *allowanceNode) apply(tx *solana.Transaction) error {
	m := &tx.Message
	signer := func(pk solana.PublicKey) bool {
		for i, k := range m.AccountKeys {
			if k == pk && m.IsSigner(i) {
				return true
			}
		}
		return false
	}
	// On a copy: a failed transaction changes nothing.
	work := map[solana.PublicKey]solana.TokenAccount{}
	get := func(pk solana.PublicKey) (*solana.TokenAccount, error) {
		if w, ok := work[pk]; ok {
			return &w, nil
		}
		ta := n.tokens[pk]
		if ta == nil {
			return nil, errors.New("account not found")
		}
		return ta, nil
	}
	put := func(pk solana.PublicKey, ta *solana.TokenAccount) { work[pk] = *ta }
	for _, ci := range m.Instructions {
		if m.ProgramOf(ci) != solana.TokenProgram {
			continue
		}
		a := m.InstructionAccounts(ci)
		switch ci.Data[0] {
		case 13: // ApproveChecked: source, mint, delegate, owner
			src, err := get(a[0])
			if err != nil || src.Owner != a[3] || !signer(a[3]) {
				return errors.New("approve: owner must sign")
			}
			c := *src
			d := a[2]
			c.Delegate, c.DelegatedAmount = &d, binary.LittleEndian.Uint64(ci.Data[1:9])
			put(a[0], &c)
		case 5: // Revoke: source, owner
			src, err := get(a[0])
			if err != nil || src.Owner != a[1] || !signer(a[1]) {
				return errors.New("revoke: owner must sign")
			}
			c := *src
			c.Delegate, c.DelegatedAmount = nil, 0
			put(a[0], &c)
		case 12: // TransferChecked: source, mint, dest, authority
			amount := binary.LittleEndian.Uint64(ci.Data[1:9])
			src, err := get(a[0])
			if err != nil || !signer(a[3]) {
				return errors.New("transfer: authority must sign")
			}
			dst, err := get(a[2])
			if err != nil {
				return err
			}
			s, d := *src, *dst
			switch {
			case s.Owner == a[3]:
			case s.Delegate != nil && *s.Delegate == a[3]:
				if s.DelegatedAmount < amount {
					return errors.New("custom program error: 0x1") // InsufficientFunds
				}
				s.DelegatedAmount -= amount
				if s.DelegatedAmount == 0 {
					s.Delegate = nil
				}
			default:
				return errors.New("custom program error: 0x4") // OwnerMismatch
			}
			if s.Amount < amount {
				return errors.New("custom program error: 0x1")
			}
			s.Amount -= amount
			d.Amount += amount
			put(a[0], &s)
			put(a[2], &d)
		}
	}
	for pk, w := range work {
		c := w
		n.tokens[pk] = &c
	}
	return nil
}

func (n *allowanceNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     any               `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	str := func(i int) string {
		var s string
		_ = json.Unmarshal(req.Params[i], &s)
		return s
	}
	reply := func(result any, rpcErr map[string]any) {
		body := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
		if rpcErr != nil {
			body = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": rpcErr}
		}
		_ = json.NewEncoder(w).Encode(body)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	ctx := map[string]any{"slot": 100}
	switch req.Method {
	case "getLatestBlockhash":
		reply(map[string]any{"context": ctx, "value": map[string]any{"blockhash": solana.EncodeBase58(make([]byte, 32)), "lastValidBlockHeight": 1_150}}, nil)
	case "getBlockHeight":
		reply(1_000, nil)
	case "getAccountInfo":
		pk, _ := solana.ParsePublicKey(str(0))
		ta := n.tokens[pk]
		if ta == nil {
			reply(map[string]any{"context": ctx, "value": nil}, nil)
			return
		}
		reply(map[string]any{"context": ctx, "value": map[string]any{"lamports": 2_039_280, "owner": solana.TokenProgram.String(),
			"executable": false, "data": []string{base64.StdEncoding.EncodeToString(ta.Encode()), "base64"}}}, nil)
	case "sendTransaction":
		tx, err := solana.DecodeTransactionBase64(str(0))
		if err != nil {
			reply(nil, map[string]any{"code": -32602, "message": err.Error()})
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(str(0))
		rt, _ := solana.ParseRawTransaction(raw)
		for i, k := range rt.Signers() {
			if !ed25519.Verify(k[:], rt.Message(), tx.Signatures[i][:]) {
				reply(nil, map[string]any{"code": -32003, "message": "Transaction signature verification failure"})
				return
			}
		}
		sig := solana.EncodeBase58(tx.Signatures[0][:])
		if _, seen := n.statuses[sig]; seen {
			reply(sig, nil)
			return
		}
		if err := n.apply(tx); err != nil {
			// Preflight: the node refuses it and never forwards it.
			reply(nil, map[string]any{"code": -32002, "message": "Transaction simulation failed: " + err.Error()})
			return
		}
		n.statuses[sig] = "ok"
		n.sent = append(n.sent, tx)
		reply(sig, nil)
	case "getSignatureStatuses":
		var sigs []string
		_ = json.Unmarshal(req.Params[0], &sigs)
		out := make([]any, len(sigs))
		for i, s := range sigs {
			if n.statuses[s] == "ok" {
				out[i] = map[string]any{"slot": 100, "confirmations": nil, "err": nil, "confirmationStatus": "confirmed"}
			}
		}
		reply(map[string]any{"context": ctx, "value": out}, nil)
	default:
		reply(nil, map[string]any{"code": -32601, "message": "method not found: " + req.Method})
	}
}

type memAllowances struct{ m map[string]*VaultAllowance }

func (s *memAllowances) VaultAllowance(_ context.Context, userID, network string) (*VaultAllowance, error) {
	if a, ok := s.m[userID+"|"+network]; ok {
		c := *a
		return &c, nil
	}
	return nil, shared.ErrNotFound
}

func (s *memAllowances) SaveVaultAllowance(_ context.Context, a *VaultAllowance) error {
	c := *a
	s.m[a.UserID+"|"+a.Network] = &c
	return nil
}

type allowanceRig struct {
	svc        *VaultAllowanceService
	node       *allowanceNode
	store      *memAllowances
	pulls      *memOnchainStore
	payer      *solana.Keypair
	account    solana.PublicKey
	payerUSDC  solana.PublicKey
	mint       solana.PublicKey
	ownerKey   ed25519.PrivateKey
	ownerPK    solana.PublicKey
	userID     string
	passID     string
	railName   string
	network    string
	reserveSeq int
}

func newAllowanceRig(t *testing.T) *allowanceRig {
	t.Helper()
	node := newAllowanceNode()
	srv := httptest.NewServer(node)
	t.Cleanup(srv.Close)
	payer := fixturePayerKey(t)
	_, ownerKey, _ := ed25519.GenerateKey(nil)
	var owner solana.PublicKey
	copy(owner[:], ownerKey.Public().(ed25519.PublicKey))
	mint := solana.MustPublicKey(fixtureMint)
	account, _ := solana.AssociatedTokenAddress(owner, mint, solana.TokenProgram)
	payerUSDC, _ := solana.AssociatedTokenAddress(payer.PublicKey(), mint, solana.TokenProgram)
	node.tokens[account] = &solana.TokenAccount{Mint: mint, Owner: owner, Amount: 3_000_000, State: solana.TokenAccountInitialized}
	node.tokens[payerUSDC] = &solana.TokenAccount{Mint: mint, Owner: payer.PublicKey(), Amount: 0, State: solana.TokenAccountInitialized}

	vaults := &memVaults{m: map[string]*OwnerVaultRecord{"user_1": {UserID: "user_1", SolanaAddress: owner.String(), EVMAddress: "0x0000000000000000000000000000000000000001"}}}
	store := &memAllowances{m: map[string]*VaultAllowance{}}
	pulls := newMemOnchainStore()
	n := &OnchainNetwork{Network: chain.SolanaDevnet, Cluster: "devnet", RailName: "solana-x402-devnet", RPC: solana.NewRPC(srv.URL, nil),
		Payer: payer, Mint: mint, PriorityMicroLamports: 10_000}
	svc := NewVaultAllowanceService(store, vaults, onePass{spendpass.Pass{ID: "pass_1", UserID: "user_1"}}, pulls, nil, []*OnchainNetwork{n}, 0, nil)
	return &allowanceRig{svc: svc, node: node, store: store, pulls: pulls, payer: payer, account: account, payerUSDC: payerUSDC, mint: mint,
		ownerKey: ownerKey, ownerPK: owner, userID: "user_1", passID: "pass_1", railName: n.RailName, network: n.Network}
}

// signed builds what the browser would: the instructions with the payer as
// fee payer, signed by the vault only.
func (r *allowanceRig) signed(t *testing.T, ixs ...solana.Instruction) string {
	t.Helper()
	m, err := solana.CompileV0(r.payer.PublicKey(), ixs, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	tx := solana.NewTransaction(m)
	i := -1
	for j, k := range m.AccountKeys {
		if k == r.ownerPK {
			i = j
		}
	}
	copy(tx.Signatures[i][:], ed25519.Sign(r.ownerKey, m.Serialize()))
	return tx.Base64()
}

func (r *allowanceRig) approve(amount uint64) solana.Instruction {
	data := make([]byte, 10)
	data[0] = 13
	binary.LittleEndian.PutUint64(data[1:], amount)
	data[9] = chain.USDCDecimals
	return solana.Instruction{ProgramID: solana.TokenProgram, Data: data, Accounts: []solana.AccountMeta{
		{Pubkey: r.account, IsWritable: true}, {Pubkey: r.mint}, {Pubkey: r.payer.PublicKey()}, {Pubkey: r.ownerPK, IsSigner: true}}}
}

func (r *allowanceRig) revoke() solana.Instruction {
	return solana.Instruction{ProgramID: solana.TokenProgram, Data: []byte{5}, Accounts: []solana.AccountMeta{
		{Pubkey: r.account, IsWritable: true}, {Pubkey: r.ownerPK, IsSigner: true}}}
}

func (r *allowanceRig) reservation() *econ.Reservation {
	r.reserveSeq++
	return &econ.Reservation{ID: fmt.Sprintf("rsv_%d", r.reserveSeq), IntentID: "eint_1", Rail: r.railName}
}

func TestVaultAllowance_ApprovePullRefuseRevoke(t *testing.T) {
	ctx := context.Background()
	r := newAllowanceRig(t)

	// Before any allowance: payments don't come from the vault at all.
	if from, err := r.svc.Funding(ctx, r.passID, r.railName); from || err != nil {
		t.Fatalf("funding before an allowance: %v %v", from, err)
	}
	if p, err := r.svc.Fund(ctx, r.passID, r.reservation(), 100_000); p != nil || err != nil {
		t.Fatalf("fund before an allowance: %+v %v", p, err)
	}

	// The owner allows 1 USDC; Algebra pays the fee.
	params, err := r.svc.Prepare(ctx, r.userID, "solana-devnet")
	if err != nil || params.Delegate != r.payer.PublicKey().String() || params.TokenAccount != r.account.String() {
		t.Fatalf("prepare: %+v %v", params, err)
	}
	res, err := r.svc.Submit(ctx, r.userID, "solana-devnet", r.signed(t, solana.SetComputeUnitLimit(allowanceComputeUnits), solana.SetComputeUnitPrice(params.ComputeUnitPrice), r.approve(1_000_000)))
	if err != nil || res.Pending {
		t.Fatalf("submit approve: %+v %v", res, err)
	}
	if a := res.Allowance; !a.Active || a.ApprovedMinor != 1_000_000 || a.LeftMinor != 1_000_000 || a.BalanceMinor != 3_000_000 {
		t.Fatalf("after approve: %+v", a)
	}
	if from, _ := r.svc.Funding(ctx, r.passID, r.railName); !from {
		t.Fatal("payments don't come from the vault after the allowance")
	}

	// A payment pulls exactly its amount into the payer, as the delegate.
	rv := r.reservation()
	p, err := r.svc.Fund(ctx, r.passID, rv, 400_000)
	if err != nil || p == nil || p.State != PullPulled || p.Source != PullSourceVault || p.PassAddress != r.account.String() || p.OwnerWallet != r.ownerPK.String() {
		t.Fatalf("pull: %+v %v", p, err)
	}
	if got := r.node.tokens[r.payerUSDC].Amount; got != 400_000 {
		t.Fatalf("payer got %d", got)
	}
	if v := r.node.tokens[r.account]; v.Amount != 2_600_000 || v.DelegatedAmount != 600_000 {
		t.Fatalf("vault after the pull: %+v", v)
	}
	// Asked again for the same attempt: the same pull, nothing moved twice.
	if again, err := r.svc.Fund(ctx, r.passID, rv, 400_000); err != nil || again.PullSignature != p.PullSignature {
		t.Fatalf("second fund for one attempt: %+v %v", again, err)
	}
	if got := r.node.tokens[r.payerUSDC].Amount; got != 400_000 {
		t.Fatalf("pulled twice: payer has %d", got)
	}
	// The dry run sees what is left: 0.6 USDC of allowance.
	if code := r.svc.VaultStatus(ctx, r.passID, "solana-devnet").For(700_000); code != DenyVaultAllowanceLow {
		t.Fatalf("dry run over the allowance: %q", code)
	}
	if code := r.svc.VaultStatus(ctx, r.passID, "solana-devnet").For(500_000); code != "" {
		t.Fatalf("dry run within the allowance: %q", code)
	}

	// More than is left is refused before anything is signed.
	_, err = r.svc.Fund(ctx, r.passID, r.reservation(), 700_000)
	var denied *AuthorityDenied
	if !errors.As(err, &denied) || denied.ReasonCodes[0] != DenyVaultAllowanceLow {
		t.Fatalf("over the allowance: %v", err)
	}
	// Even if Algebra's own check were skipped, the token program refuses it:
	// the chain is the limit, not this server.
	over := solana.TransferChecked(solana.TokenProgram, r.account, r.mint, r.payerUSDC, r.payer.PublicKey(), 700_000, 6)
	m, _ := solana.CompileV0(r.payer.PublicKey(), []solana.Instruction{over}, [32]byte{})
	tx := solana.NewTransaction(m)
	_ = tx.PartialSign(r.payer)
	if _, err := r.svc.nets[r.network].RPC.SendTransaction(ctx, tx.Base64(), solana.SendOptions{}); err == nil {
		t.Fatal("the chain let the delegate move more than the allowance")
	}

	// The owner stops it; payments no longer come from the vault.
	res, err = r.svc.Submit(ctx, r.userID, "solana-devnet", r.signed(t, r.revoke()))
	if err != nil || res.Allowance.Active || res.Allowance.LeftMinor != 0 {
		t.Fatalf("revoke: %+v %v", res, err)
	}
	if from, _ := r.svc.Funding(ctx, r.passID, r.railName); from {
		t.Fatal("still funding from the vault after a revoke")
	}
	if r.node.tokens[r.account].Delegate != nil {
		t.Fatal("the chain still names a delegate")
	}
}

func TestVaultAllowance_SubmitRefusesWhatItWontSign(t *testing.T) {
	ctx := context.Background()
	r := newAllowanceRig(t)
	thief, _ := solana.NewKeypair()

	// A transfer of the payer's own USDC hidden beside the approve.
	steal := solana.TransferChecked(solana.TokenProgram, r.payerUSDC, r.mint, r.account, r.payer.PublicKey(), 1, 6)
	if _, err := r.svc.Submit(ctx, r.userID, "solana-devnet", r.signed(t, r.approve(1_000_000), steal)); !errors.Is(err, ErrAllowanceTx) {
		t.Fatalf("hidden transfer: %v", err)
	}
	// An approve to someone else.
	bad := r.approve(1_000_000)
	bad.Accounts[2].Pubkey = thief.PublicKey()
	if _, err := r.svc.Submit(ctx, r.userID, "solana-devnet", r.signed(t, bad)); !errors.Is(err, ErrAllowanceTx) {
		t.Fatalf("other delegate: %v", err)
	}
	// Signed by a key that isn't this account's vault.
	_, otherKey, _ := ed25519.GenerateKey(nil)
	r2 := *r
	r2.ownerKey = otherKey
	if _, err := r.svc.Submit(ctx, r.userID, "solana-devnet", r2.signed(t, r.approve(1_000_000))); !errors.Is(err, ErrVaultNotYours) {
		t.Fatalf("not the vault's signature: %v", err)
	}
	if len(r.node.sent) != 0 {
		t.Fatalf("%d transactions were sent", len(r.node.sent))
	}
	if _, err := r.store.VaultAllowance(ctx, r.userID, r.network); !errors.Is(err, shared.ErrNotFound) {
		t.Fatal("a refused allowance was recorded")
	}
	// On a network allowances don't run on.
	if _, err := r.svc.Prepare(ctx, r.userID, "solana"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mainnet: %v", err)
	}
}
