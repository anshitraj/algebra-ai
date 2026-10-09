package spendpass

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/project-algebra/algebra/internal/platform/solana"
)

func program(t *testing.T) Program {
	t.Helper()
	p, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The address the Solana CLI derives for the same seeds:
// solana find-program-derived-address <program> string:pass pubkey:<owner> u64le:7
func TestPassAddressMatchesTheCLI(t *testing.T) {
	owner := solana.MustPublicKey("GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA")
	got, err := program(t).PassAddress(owner, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "D17Aae2jvbk6aFNDzaFJGeJSrLAbSYV6Y6XegUqxcYHX" {
		t.Fatalf("pass address %s", got)
	}
}

func TestInstructionLayouts(t *testing.T) {
	p := program(t)
	owner, agent, mint, dest := solana.MustPublicKey("GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA"), solana.TokenProgram, solana.MemoProgram, solana.ComputeBudgetProgram
	terms := Terms{PerCallCap: 5, TotalBudget: 50, WindowSecs: 60, WindowCap: 10, ExpiresAt: 1_900_000_000}
	ix, pass, err := p.CreatePass(owner, agent, mint, dest, 7, terms)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Data) != 49 || ix.Data[0] != 0 || binary.LittleEndian.Uint64(ix.Data[1:]) != 7 || binary.LittleEndian.Uint64(ix.Data[41:]) != 1_900_000_000 {
		t.Fatalf("create data %x", ix.Data)
	}
	if len(ix.Accounts) != 9 || !ix.Accounts[0].IsSigner || ix.Accounts[4].Pubkey != pass || !ix.Accounts[5].IsWritable {
		t.Fatalf("create accounts %+v", ix.Accounts)
	}
	var intent [32]byte
	intent[0] = 9
	pull, _ := p.Pull(agent, pass, mint, dest, 1234, intent)
	if len(pull.Data) != 41 || pull.Data[0] != 2 || binary.LittleEndian.Uint64(pull.Data[1:]) != 1234 || pull.Data[9] != 9 {
		t.Fatalf("pull data %x", pull.Data)
	}
	if !pull.Accounts[0].IsSigner || pull.Accounts[4].Pubkey != dest || !pull.Accounts[4].IsWritable {
		t.Fatalf("pull accounts %+v", pull.Accounts)
	}
	refund, _ := p.Refund(agent, pass, mint, dest, 1, intent)
	if refund.Data[0] != 8 {
		t.Fatalf("refund discriminator %d", refund.Data[0])
	}
	if f := p.SetFrozen(owner, pass, true); len(f.Data) != 2 || f.Data[0] != 3 || f.Data[1] != 1 {
		t.Fatalf("freeze data %x", f.Data)
	}
	if r := p.Revoke(owner, pass); len(r.Data) != 1 || r.Data[0] != 6 {
		t.Fatalf("revoke data %x", r.Data)
	}
}

func encode(s State) []byte {
	b := []byte{accountDiscriminator}
	for _, k := range []solana.PublicKey{s.Owner, s.Agent, s.Mint, s.Destination} {
		b = append(b, k[:]...)
	}
	for _, v := range []uint64{s.ID, s.PerCallCap, s.TotalBudget, s.Spent, uint64(s.WindowSecs), s.WindowCap, uint64(s.WindowStart), s.WindowSpent, uint64(s.ExpiresAt), uint64(s.CreatedAt), s.Pulls} {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	b = append(b, s.LastIntent[:]...)
	bt := func(v bool) byte {
		if v {
			return 1
		}
		return 0
	}
	return append(b, bt(s.Frozen), bt(s.Revoked), s.Bump)
}

func TestDecodeRoundTripAndLimits(t *testing.T) {
	want := State{Owner: solana.TokenProgram, Agent: solana.MemoProgram, ID: 3, PerCallCap: 100, TotalBudget: 1000, Spent: 950,
		WindowSecs: 60, WindowCap: 150, WindowStart: 1000, WindowSpent: 120, ExpiresAt: 5000, CreatedAt: 1000, Pulls: 4, Frozen: true, Bump: 254}
	data := encode(want)
	if len(data) != AccountSize {
		t.Fatalf("size %d, want %d", len(data), AccountSize)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if *got != want {
		t.Fatalf("decoded %+v", got)
	}
	if !errors.Is(got.CanPull(1010, 1), ErrFrozen) {
		t.Fatal("frozen pass allowed a pull")
	}
	got.Frozen = false
	if err := got.CanPull(1010, 31); !errors.Is(err, ErrOverLimit) { // window: 120 + 31 > 150
		t.Fatalf("window cap not enforced: %v", err)
	}
	if err := got.CanPull(1060, 50); err != nil { // the next window
		t.Fatalf("next window refused: %v", err)
	}
	if err := got.CanPull(1060, 51); !errors.Is(err, ErrOverLimit) { // budget: only 50 left
		t.Fatalf("budget not enforced: %v", err)
	}
	if !errors.Is(got.CanPull(5000, 1), ErrExpired) {
		t.Fatal("expired pass allowed a pull")
	}
	if _, err := Decode(data[:AccountSize-1]); !errors.Is(err, ErrNotAPass) {
		t.Fatal("short account decoded")
	}
	data[0] = 2
	if _, err := Decode(data); !errors.Is(err, ErrNotAPass) {
		t.Fatal("wrong discriminator decoded")
	}
}

func TestExplainNamesProgramErrors(t *testing.T) {
	err := Explain(errors.New("Transaction simulation failed: Error processing Instruction 2: custom program error: 0x1774"))
	var pe *ProgramError
	if !errors.As(err, &pe) || pe.Name != "OverPerCallCap" {
		t.Fatalf("got %v", err)
	}
	if err := Explain(errors.New(`{"InstructionError":[2,{"Custom":6001}]}`)); !errors.As(err, &pe) || pe.Name != "PassFrozen" {
		t.Fatalf("got %v", err)
	}
	plain := errors.New("connection refused")
	if Explain(plain) != plain {
		t.Fatal("unrelated error changed")
	}
}

func TestTermsValidate(t *testing.T) {
	ok := Terms{PerCallCap: 10, TotalBudget: 100, ExpiresAt: 500}
	if err := ok.Validate(100); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Terms{
		{TotalBudget: 100, ExpiresAt: 500},
		{PerCallCap: 10, TotalBudget: 9, ExpiresAt: 500},
		{PerCallCap: 10, TotalBudget: 100, ExpiresAt: 100},
		{PerCallCap: 10, TotalBudget: 100, ExpiresAt: 500, WindowCap: 5},
		{PerCallCap: 10, TotalBudget: 100, ExpiresAt: 500, WindowSecs: 60, WindowCap: 9},
	} {
		if bad.Validate(100) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
