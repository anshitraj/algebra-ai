package paychan

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/x402"
)

// Scheme is x402's usage-based scheme.
const Scheme = "upto"

// UptoTerms are one x402 "upto" option on Solana, read from its payment
// requirements (scheme_upto_svm.md §4.1).
type UptoTerms struct {
	Network            string
	Mint               solana.PublicKey
	PayTo              solana.PublicKey
	FeePayer           solana.PublicKey
	ReceiverAuthorizer solana.PublicKey
	TokenProgram       solana.PublicKey
	// MaxAmount is the ceiling the payer escrows; the provider settles what
	// the call actually cost, up to it, and the rest comes back.
	MaxAmount         uint64
	WithdrawDelay     uint32
	MaxTimeoutSeconds int
	Memo              string
	RecentSlot        uint64
	ValidAfter        int64
}

// ParseUptoTerms reads and checks an upto option.
func ParseUptoTerms(r x402.Requirements) (UptoTerms, error) {
	if !strings.EqualFold(r.Scheme, Scheme) {
		return UptoTerms{}, fmt.Errorf("paychan: scheme %q is not upto", r.Scheme)
	}
	var extra map[string]any
	if err := json.Unmarshal(r.Extra, &extra); err != nil || extra == nil {
		return UptoTerms{}, errors.New("paychan: an upto option needs its extra terms (feePayer, receiverAuthorizer, withdrawDelay)")
	}
	str := func(k string) string { s, _ := extra[k].(string); return strings.TrimSpace(s) }
	num := func(k string) (uint64, bool) {
		switch v := extra[k].(type) {
		case float64:
			if v >= 0 && v == float64(uint64(v)) {
				return uint64(v), true
			}
		case string:
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
		return 0, false
	}
	if f := str("paymentFlow"); f != "" && f != "escrow" {
		return UptoTerms{}, fmt.Errorf("paychan: payment flow %q isn't supported", f)
	}
	var t UptoTerms
	var err error
	t.Network = r.Network
	if t.Mint, err = solana.ParsePublicKey(r.Asset); err != nil {
		return t, fmt.Errorf("paychan: asset: %w", err)
	}
	if t.PayTo, err = solana.ParsePublicKey(r.PayTo); err != nil {
		return t, fmt.Errorf("paychan: payTo: %w", err)
	}
	if t.FeePayer, err = solana.ParsePublicKey(str("feePayer")); err != nil {
		return t, errors.New("paychan: an upto option must name its fee payer (extra.feePayer)")
	}
	if t.ReceiverAuthorizer, err = solana.ParsePublicKey(str("receiverAuthorizer")); err != nil {
		return t, errors.New("paychan: an upto option must name its receiver authorizer (extra.receiverAuthorizer)")
	}
	t.TokenProgram = solana.TokenProgram
	if tp := str("tokenProgram"); tp != "" {
		if t.TokenProgram, err = solana.ParsePublicKey(tp); err != nil || (t.TokenProgram != solana.TokenProgram && t.TokenProgram != solana.Token2022Program) {
			return t, errors.New("paychan: the token program must be SPL Token or Token-2022")
		}
	}
	amount, err := r.AmountMinor()
	if err != nil {
		return t, err
	}
	t.MaxAmount = uint64(amount)
	wd, ok := num("withdrawDelay")
	if !ok || wd == 0 || wd > 1<<31 {
		return t, errors.New("paychan: an upto option needs a positive withdrawDelay")
	}
	t.WithdrawDelay = uint32(wd)
	t.MaxTimeoutSeconds = r.MaxTimeoutSeconds
	if t.MaxTimeoutSeconds <= 0 {
		t.MaxTimeoutSeconds = 300
	}
	t.Memo = str("memo")
	if len(t.Memo) > 256 {
		return t, errors.New("paychan: the seller's memo is over 256 bytes")
	}
	t.RecentSlot, _ = num("recentSlot")
	if va, ok := num("validAfter"); ok {
		t.ValidAfter = int64(va)
	}
	return t, nil
}

// UptoPayload is PAYMENT-SIGNATURE's payload for upto (§4.2).
type UptoPayload struct {
	From             string `json:"from"`
	MaxAmount        string `json:"maxAmount"`
	ExpiresAt        int64  `json:"expiresAt"`
	ValidAfter       int64  `json:"validAfter"`
	Nonce            string `json:"nonce"`
	OpenSlot         uint64 `json:"openSlot"`
	ChannelID        string `json:"channelId"`
	Deposit          string `json:"deposit"`
	AuthorizedSigner string `json:"authorizedSigner"`
	OpenTransaction  string `json:"openTransaction"`
}

// uptoComputeLimit covers creating the channel PDA and escrow account and
// moving the deposit, with a margin; the spec caps it at 400k.
const uptoComputeLimit = 200_000

// BuildUptoOpen builds the open transaction for an upto authorization and
// signs it as the payer. The fee payer (the provider's sponsor) still has to
// sign and submit it. It escrows exactly the option's maximum, sends 100% of
// whatever is settled to payTo, and lets the payer take back the rest.
func BuildUptoOpen(t UptoTerms, payer *solana.Keypair, blockhash [32]byte, openSlot uint64, now time.Time, computePrice uint64) (*solana.Transaction, UptoPayload, error) {
	from := payer.PublicKey()
	if t.FeePayer == from || t.PayTo == from {
		return nil, UptoPayload{}, errors.New("paychan: the provider's fee payer or payee is the paying wallet; refusing")
	}
	var nb [8]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return nil, UptoPayload{}, err
	}
	nonce := binary.LittleEndian.Uint64(nb[:])
	open, ch, err := Open(OpenParams{
		Payer: from, RentPayer: t.FeePayer, Payee: t.FeePayer, Mint: t.Mint, AuthorizedSigner: t.ReceiverAuthorizer,
		TokenProgram: t.TokenProgram, Salt: nonce, Deposit: t.MaxAmount, GracePeriod: t.WithdrawDelay, OpenSlot: openSlot,
		Recipients: []Entry{{Recipient: t.PayTo, Bps: 10_000}},
	})
	if err != nil {
		return nil, UptoPayload{}, err
	}
	memoText := t.Memo
	if memoText == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, UptoPayload{}, err
		}
		memoText = hex.EncodeToString(b)
	}
	memo, err := solana.Memo(memoText)
	if err != nil {
		return nil, UptoPayload{}, err
	}
	if computePrice == 0 {
		computePrice = 1
	}
	msg, err := solana.CompileV0(t.FeePayer, []solana.Instruction{
		solana.SetComputeUnitLimit(uptoComputeLimit), solana.SetComputeUnitPrice(computePrice), open, memo,
	}, blockhash)
	if err != nil {
		return nil, UptoPayload{}, err
	}
	tx := solana.NewTransaction(msg)
	if err := tx.PartialSign(payer); err != nil {
		return nil, UptoPayload{}, err
	}
	validAfter := t.ValidAfter
	if validAfter == 0 {
		validAfter = now.Unix()
	}
	p := UptoPayload{
		From: from.String(), MaxAmount: strconv.FormatUint(t.MaxAmount, 10), ExpiresAt: now.Unix() + int64(t.MaxTimeoutSeconds),
		ValidAfter: validAfter, Nonce: strconv.FormatUint(nonce, 10), OpenSlot: openSlot, ChannelID: ch.String(),
		Deposit: strconv.FormatUint(t.MaxAmount, 10), AuthorizedSigner: t.ReceiverAuthorizer.String(), OpenTransaction: tx.Base64(),
	}
	return tx, p, nil
}

// VerifyUptoOpen is the provider's check of a client's open transaction before
// its sponsor signs it (§5, "acceptance policy"): the fee payer and signers,
// the instruction layout, and every field of the open instruction bound to the
// terms and the payload. It returns the decoded transaction ready for the
// sponsor's signature.
func VerifyUptoOpen(t UptoTerms, p UptoPayload) (*solana.Transaction, solana.PublicKey, error) {
	fail := func(f string, a ...any) (*solana.Transaction, solana.PublicKey, error) {
		return nil, solana.PublicKey{}, fmt.Errorf("paychan: "+f, a...)
	}
	from, err := solana.ParsePublicKey(p.From)
	if err != nil {
		return fail("from: %v", err)
	}
	if p.MaxAmount != strconv.FormatUint(t.MaxAmount, 10) || p.Deposit != p.MaxAmount {
		return fail("maxAmount and deposit must equal the requirement's amount")
	}
	if p.AuthorizedSigner != t.ReceiverAuthorizer.String() {
		return fail("authorizedSigner must be the receiver authorizer")
	}
	if p.ExpiresAt == 0 {
		return fail("expiresAt must be set")
	}
	nonce, err := strconv.ParseUint(p.Nonce, 10, 64)
	if err != nil {
		return fail("nonce: %v", err)
	}
	tx, err := solana.DecodeTransactionBase64(p.OpenTransaction)
	if err != nil {
		return fail("open transaction: %v", err)
	}
	m := tx.Message
	if len(m.AccountKeys) == 0 || m.AccountKeys[0] != t.FeePayer {
		return fail("the fee payer must be the sponsor")
	}
	if int(m.NumRequiredSignatures) != 2 {
		return fail("the open must need exactly the payer's and the sponsor's signatures")
	}
	if !tx.VerifySignature(from) {
		return fail("the payer's signature is missing or invalid")
	}
	var openIx *solana.CompiledInstruction
	opens := 0
	for i, ci := range m.Instructions {
		switch m.ProgramOf(ci) {
		case solana.ComputeBudgetProgram, solana.MemoProgram:
		case ProgramID:
			opens++
			openIx = &m.Instructions[i]
		default:
			return fail("instruction %d calls a program the open doesn't need", i)
		}
	}
	if opens != 1 {
		return fail("the transaction must open exactly one channel")
	}
	want, ch, err := Open(OpenParams{
		Payer: from, RentPayer: t.FeePayer, Payee: t.FeePayer, Mint: t.Mint, AuthorizedSigner: t.ReceiverAuthorizer,
		TokenProgram: t.TokenProgram, Salt: nonce, Deposit: t.MaxAmount, GracePeriod: t.WithdrawDelay, OpenSlot: p.OpenSlot,
		Recipients: []Entry{{Recipient: t.PayTo, Bps: 10_000}},
	})
	if err != nil {
		return fail("%v", err)
	}
	if ch.String() != p.ChannelID {
		return fail("channelId isn't the address the open derives")
	}
	if string(openIx.Data) != string(want.Data) {
		return fail("the open instruction's data doesn't match the terms")
	}
	got := m.InstructionAccounts(*openIx)
	if len(got) != len(want.Accounts) {
		return fail("the open instruction has the wrong accounts")
	}
	for i := range got {
		if got[i] != want.Accounts[i].Pubkey {
			return fail("open account %d is %s, want %s", i, got[i], want.Accounts[i].Pubkey)
		}
	}
	return tx, ch, nil
}

// SettleUptoTx builds the provider's settlement: an Ed25519 voucher for the
// actual amount (when it is more than zero), settle_and_seal signed by the
// payee (the sponsor), then distribute, which pays payTo and refunds the payer
// in the same transaction. The fee payer must sign it.
func SettleUptoTx(network string, t UptoTerms, from, channel solana.PublicKey, voucher *Voucher, blockhash [32]byte) (*solana.Message, error) {
	ixs := []solana.Instruction{solana.SetComputeUnitLimit(300_000), solana.SetComputeUnitPrice(1)}
	if voucher != nil && voucher.Cumulative > 0 {
		ed, err := voucher.Instruction()
		if err != nil {
			return nil, err
		}
		ixs = append(ixs, ed, SettleAndSeal(t.FeePayer, channel, true))
	} else {
		ixs = append(ixs, SettleAndSeal(t.FeePayer, channel, false))
	}
	dist, err := Distribute(DistributeParams{
		Channel: channel, Payer: from, RentPayer: t.FeePayer, Payee: t.FeePayer, Mint: t.Mint, TokenProgram: t.TokenProgram,
		Network: network, Recipients: []Entry{{Recipient: t.PayTo, Bps: 10_000}},
	})
	if err != nil {
		return nil, err
	}
	ixs = append(ixs, dist)
	return solana.CompileV0(t.FeePayer, ixs, blockhash)
}
