package solanax402

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/platform/solana"
	"github.com/project-algebra/algebra/providers/paychan"
	"github.com/project-algebra/algebra/providers/x402"
)

// x402 "upto" on Solana: usage-based payment through a payment channel
// (providers/paychan). The wallet escrows the provider's ceiling in a channel
// it can always get back out of; the provider settles what the call actually
// cost from a signed voucher, and the same final instruction refunds the rest.
// Algebra signs only the channel's open, as the payer, and holds the
// reservation to the ceiling; what it records as spent is what the chain shows
// the provider was paid, never the ceiling.

// buildUpto builds the payer-signed open for an upto option.
func (r *Rail) buildUpto(ctx context.Context, rv *econ.Reservation, sel x402.Selected, resource string) (*app.PaymentAuthority, []string, error) {
	t, err := paychan.ParseUptoTerms(sel.Requirements)
	if err != nil {
		return nil, nil, fmt.Errorf("solanax402: %w", err)
	}
	max := int64(t.MaxAmount)
	switch {
	case t.Mint != r.mint:
		return nil, nil, fmt.Errorf("solanax402: the provider asks for asset %s, which is not Circle's USDC on %s", t.Mint, r.network)
	case t.TokenProgram != solana.TokenProgram:
		return nil, nil, errors.New("solanax402: USDC lives under the SPL Token program; refusing another")
	case max > r.cfg.MaxPaymentMinor:
		return nil, nil, fmt.Errorf("solanax402: a ceiling of %d exceeds this rail's hard ceiling of %d per payment", max, r.cfg.MaxPaymentMinor)
	case rv != nil && max > rv.HoldMinor:
		return nil, nil, fmt.Errorf("solanax402: the provider's ceiling %d is more than this attempt's hold of %d", max, rv.HoldMinor)
	case t.FeePayer == r.payer || t.PayTo == r.payer || t.ReceiverAuthorizer == r.payer:
		return nil, nil, errors.New("solanax402: the provider names this wallet as its sponsor, payee or authorizer; refusing")
	}
	src, err := solana.AssociatedTokenAddress(r.payer, r.mint, solana.TokenProgram)
	if err != nil {
		return nil, nil, err
	}
	var problems []string
	switch bal, err := r.cfg.RPC.GetTokenAccountBalance(ctx, src, solana.Confirmed); {
	case errors.Is(err, solana.ErrAccountNotFound):
		problems = append(problems, fmt.Sprintf("solanax402: this wallet (%s) has no USDC account on %s", r.payer, r.network))
	case err != nil:
		return nil, nil, err
	case bal.Amount < t.MaxAmount:
		problems = append(problems, fmt.Sprintf("solanax402: insufficient USDC to escrow the ceiling: the wallet holds %d, the ceiling is %d", bal.Amount, t.MaxAmount))
	}
	// The open is valid within OpenSlotWindow slots of open_slot, and the
	// blockhash within 150 blocks: take both from the chain now.
	slot, err := r.cfg.RPC.GetSlot(ctx, solana.Confirmed)
	if err != nil {
		return nil, nil, err
	}
	bh, err := r.cfg.RPC.GetLatestBlockhash(ctx, solana.Confirmed)
	if err != nil {
		return nil, nil, err
	}
	tx, payload, err := paychan.BuildUptoOpen(t, r.cfg.Signer, bh.Blockhash, slot, r.now(), r.cfg.ComputePriceMicroLamports)
	if err != nil {
		return nil, nil, fmt.Errorf("solanax402: %w", err)
	}
	sig, _ := tx.SignerSignature(r.payer)
	pb, _ := json.Marshal(payload)
	pp := x402.PaymentPayload{Version: sel.Version, Payload: pb}
	header := x402.HeaderPayment
	if sel.Version >= 2 {
		header = x402.HeaderPaymentV2
		pp.Accepted = sel.Raw
		pp.Resource = sel.Resource
		if len(pp.Resource) == 0 && resource != "" {
			pp.Resource, _ = json.Marshal(map[string]string{"url": resource})
		}
	} else {
		pp.Scheme, pp.Network = paychan.Scheme, sel.Requirements.Network
	}
	value, err := pp.Encode()
	if err != nil {
		return nil, nil, err
	}
	return &app.PaymentAuthority{
		Header: header, Value: value, AmountMinor: max,
		Evidence: econ.Evidence{
			Rail: r.Name(), Protocol: "x402", Scheme: paychan.Scheme, Network: r.network, Asset: r.mint.String(),
			PaymentID: solana.EncodeBase58(sig[:]), AmountMinor: max, Payer: r.payer.String(), PayTo: t.PayTo.String(),
			Channel: payload.ChannelID, ValidUntilHeight: bh.LastValidBlockHeight, Test: chain.IsTestNetwork(r.network),
		},
	}, problems, nil
}

// uptoSettlement proves what an upto payment did, from the chain:
//
//  1. the distribute the provider reported, by the token balances it changed:
//     what the channel's escrow paid the payee is what was spent;
//  2. else the channel account, while it exists: open or closing means the
//     ceiling is escrowed and nothing is settled yet; sealed or distributed
//     gives the settled amount;
//  3. else the open itself: never landed (blockhash expired, finalized) means
//     nothing moved; landed and failed means nothing moved.
func (r *Rail) uptoSettlement(ctx context.Context, ev econ.Evidence) (app.Settlement, error) {
	base := app.Settlement{Network: ev.Network, Asset: ev.Asset, Payer: ev.Payer, PayTo: ev.PayTo, Test: ev.Test}
	status := func(s app.SettlementStatus, amount int64, tx, detail string) (app.Settlement, error) {
		base.Status, base.AmountMinor, base.Transaction, base.Detail = s, amount, tx, detail
		return base, nil
	}
	ch, err1 := solana.ParsePublicKey(ev.Channel)
	payTo, err2 := solana.ParsePublicKey(ev.PayTo)
	if err1 != nil || err2 != nil || ev.Payer != r.payer.String() {
		return status(app.SettlementUnknown, 0, "", "the evidence doesn't identify a channel opened by this wallet")
	}
	escrow, err := solana.AssociatedTokenAddress(ch, r.mint, solana.TokenProgram)
	if err != nil {
		return base, err
	}
	payToATA, err := solana.AssociatedTokenAddress(payTo, r.mint, solana.TokenProgram)
	if err != nil {
		return base, err
	}

	// 1. The provider's settlement transaction.
	if ev.Transaction != "" {
		tb, err := r.cfg.RPC.GetTransactionBalances(ctx, ev.Transaction, r.cfg.Commitment)
		if err != nil {
			return base, err
		}
		if tb != nil && !tb.Failed() {
			if d, ok := tb.Deltas[escrow.String()]; ok && d.Change() < 0 {
				paid := tb.Deltas[payToATA.String()].Change()
				if paid <= 0 {
					return status(app.SettlementNotSettled, 0, ev.Transaction, "the channel closed with nothing paid to the provider; the ceiling was refunded")
				}
				return status(app.SettlementSettled, paid, ev.Transaction,
					fmt.Sprintf("the channel paid the provider %d of a %d ceiling; the rest was refunded", paid, ev.AmountMinor))
			}
		}
	}

	// 2. The channel account.
	info, err := r.cfg.RPC.GetAccountInfo(ctx, ch, r.cfg.Commitment)
	if err != nil {
		return base, err
	}
	if info != nil && info.Owner == paychan.ProgramID {
		c, err := paychan.DecodeChannel(info.Data)
		if err != nil {
			return status(app.SettlementUnknown, 0, "", "the channel account can't be read: "+err.Error())
		}
		switch c.Status {
		case paychan.StatusOpen, paychan.StatusClosing:
			return status(app.SettlementPending, 0, "", fmt.Sprintf("the ceiling (%d) is escrowed in channel %s; the provider hasn't settled yet", c.Deposit, ch))
		default:
			if c.Settled == 0 {
				return status(app.SettlementNotSettled, 0, "", "the channel was sealed with nothing settled; the ceiling is refundable")
			}
			return status(app.SettlementSettled, int64(c.Settled), "", fmt.Sprintf("the channel settled %d of a %d ceiling", c.Settled, c.Deposit))
		}
	}

	// 3. No channel: did the open ever land?
	src, err := solana.AssociatedTokenAddress(r.payer, r.mint, solana.TokenProgram)
	if err != nil {
		return base, err
	}
	m := match{ev: ev, src: src.String(), mint: r.mint.String(), payer: r.payer.String()}
	tx, _, err := r.scan(ctx, src, m, r.cfg.Commitment)
	if err != nil {
		return base, err
	}
	if tx != nil {
		if tx.Err != nil {
			return status(app.SettlementNotSettled, 0, tx.Signatures[0], "the channel open landed and failed: no money moved")
		}
		// It opened and the account is gone: it was distributed and closed.
		// Without the settlement transaction the amount can't be read here.
		return status(app.SettlementUnknown, 0, tx.Signatures[0], "the channel opened and has since closed; the provider's settlement transaction is needed to read the amount")
	}
	if ev.ValidUntilHeight == 0 {
		return status(app.SettlementUnknown, 0, "", "the open's expiry isn't recorded, so its absence can't be proven")
	}
	final, err := r.cfg.RPC.GetBlockHeight(ctx, solana.Finalized)
	if err != nil {
		return base, err
	}
	if final <= ev.ValidUntilHeight {
		return status(app.SettlementPending, 0, "", "the channel open isn't on chain yet and can still land")
	}
	return status(app.SettlementNotSettled, 0, "", "the channel open's blockhash expired at a finalized height and it never landed: nothing moved")
}
