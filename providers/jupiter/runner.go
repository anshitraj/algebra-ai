package jupiter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Runner is the app.StepRunner for swaps: execution type solana_swap. It prices
// a swap with a quote-only order and runs it by asking Jupiter for an assembled
// transaction, having the Rail check and sign it, and having Jupiter land it.
// What settled is not its word: the coordinator asks the Rail, which reads the
// chain.
type Runner struct {
	client      *Client
	wallet      solana.PublicKey
	maxSwap     int64
	maxSlippage int
	now         func() time.Time
}

var _ app.StepRunner = (*Runner)(nil)

// RunnerConfig configures a Runner.
type RunnerConfig struct {
	Client *Client
	// Wallet is the taker: the account the swap spends from.
	Wallet solana.PublicKey
	// MaxSwapMinor is the most one swap may spend, in micro-USDC. Zero means
	// DefaultMaxSwapMinor.
	MaxSwapMinor int64
	// MaxSlippageBps is the most slippage an agent may ask for. Zero means
	// DefaultMaxSlippageBps.
	MaxSlippageBps int
	// Now overrides the clock (tests).
	Now func() time.Time
}

// NewRunner builds a Runner.
func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.MaxSwapMinor <= 0 {
		cfg.MaxSwapMinor = DefaultMaxSwapMinor
	}
	if cfg.MaxSlippageBps <= 0 {
		cfg.MaxSlippageBps = DefaultMaxSlippageBps
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Runner{client: cfg.Client, wallet: cfg.Wallet, maxSwap: cfg.MaxSwapMinor, maxSlippage: cfg.MaxSlippageBps, now: now}
}

// Type is the execution type this runner handles.
func (r *Runner) Type() routing.ExecutionType { return routing.ExecSolanaSwap }

// Rail names the rail that signs a swap's transaction.
func (r *Runner) Rail(q routing.Quote) (string, error) {
	if chain.NormalizeNetwork(q.Network) != chain.Solana {
		return "", fmt.Errorf("swaps are made on Solana mainnet, not %q", q.Network)
	}
	return RailName, nil
}

// Candidate is Jupiter as a provider an agent can be routed to.
func Candidate() (routing.Candidate, error) {
	return routing.Candidate{
		Capability: Capability, Provider: Provider, Name: "Jupiter", ExecutionType: routing.ExecSolanaSwap,
		Network: chain.Solana, Asset: "USDC", AssetAddress: usdcMint(), Sources: []routing.DiscoverySource{routing.SourceNative},
	}.Normalize()
}

// CapabilityInfo describes what solana.swap is: a trade, with a plain JSON
// answer saying what was spent and received.
func CapabilityInfo() routing.Capability {
	return routing.Capability{
		ID: Capability, Kind: routing.KindTrade, Title: "Buy a token with USDC",
		Description: "Swap USDC for a Solana token through Jupiter. Input: {\"output_mint\":\"<token mint>\",\"amount_usdc\":\"2.50\",\"max_slippage_bps\":50}. " +
			"The wallet signs only a swap that, simulated, spends no more than the amount and delivers at least the quote less the slippage.",
		OutputSchema: json.RawMessage(`{"type":"object","required":["signature","output_mint"],"properties":{"signature":{"type":"string"},"output_mint":{"type":"string"}}}`),
	}
}

// atoms reads a decimal string of atoms.
func atoms(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// orderError says what to do about a failure to get an order.
func orderError(err error) error {
	var he *HTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case 401, 403:
			return errors.New("jupiter rejected the API key (JUPITER_API_KEY)")
		case 429:
			return errors.New("jupiter's rate limit was reached; try again shortly")
		case 400, 404, 422:
			return fmt.Errorf("jupiter found no route for this swap: %s", he.Body)
		}
	}
	return err
}

// Quote prices a swap with a quote-only order, taking the price impact from
// Jupiter's own measure of it.
func (r *Runner) Quote(ctx context.Context, c routing.Candidate, input json.RawMessage) (routing.Quote, error) {
	sw, err := ParseInput(input, r.maxSlippage)
	if err != nil {
		return routing.Quote{}, err
	}
	if sw.AmountMinor > r.maxSwap {
		return routing.Quote{}, fmt.Errorf("%s USDC is more than the most one swap may spend, %s", chain.FormatUnits(sw.AmountMinor, chain.USDCDecimals), chain.FormatUnits(r.maxSwap, chain.USDCDecimals))
	}
	main, err := r.client.Order(ctx, OrderRequest{InputMint: usdcMint(), OutputMint: sw.OutputMint, Amount: uint64(sw.AmountMinor), SlippageBps: sw.SlippageBps})
	if err != nil {
		return routing.Quote{}, orderError(err)
	}
	if out, ok := atoms(main.OutAmount); !ok || out == 0 {
		if main.ErrorMessage != "" {
			return routing.Quote{}, fmt.Errorf("jupiter has no route for this swap: %s", main.ErrorMessage)
		}
		return routing.Quote{}, errors.New("jupiter has no route for this swap")
	}
	return routing.Quote{
		Cost: routing.Cost{ProviderMinor: sw.AmountMinor}, Asset: "USDC", AssetAddress: usdcMint(), Network: chain.Solana,
		Semantics: econ.SemanticsPrepaidExact, ExpectedOutput: main.OutAmount, SlippageBps: sw.SlippageBps, PriceImpactBps: main.PriceImpactBps,
		EstimatedLatencyMS: 4000, ValidUntil: r.now().Add(quoteValidFor),
	}, nil
}

// Run makes the swap for a reserved, begun attempt.
func (r *Runner) Run(ctx context.Context, call app.StepCall) app.StepObservation {
	var obs app.StepObservation
	fail := func(class routing.FailureClass, format string, args ...any) app.StepObservation {
		obs.Class, obs.Message = class, fmt.Sprintf(format, args...)
		return obs
	}
	q := call.Quote
	sw, err := ParseInput(call.Input, r.maxSlippage)
	if err != nil {
		return fail(routing.FailQuote, "%v", err)
	}
	if sw.AmountMinor != q.Cost.ProviderMinor || sw.SlippageBps != q.SlippageBps {
		return fail(routing.FailQuote, "the swap asked for isn't the one that was priced")
	}
	canonical, _ := json.Marshal(sw)
	obs.RequestHash = econ.HashBytes(canonical)

	ord, err := r.client.Order(ctx, OrderRequest{
		InputMint: usdcMint(), OutputMint: sw.OutputMint, Amount: uint64(sw.AmountMinor), Taker: r.wallet.String(), SlippageBps: sw.SlippageBps,
	})
	if err != nil {
		return fail(routing.FailQuote, "%v", orderError(err))
	}
	switch {
	case ord.Transaction == "":
		why := "no reason given"
		if ord.ErrorMessage != "" {
			why = ord.ErrorMessage
		}
		return fail(routing.FailQuote, "jupiter quoted the swap but couldn't build it (%s)", why)
	case ord.InAmount != strconv.FormatInt(sw.AmountMinor, 10):
		return fail(routing.FailQuote, "jupiter's order spends %s, not the %d that was priced", ord.InAmount, sw.AmountMinor)
	case ord.TransactionVersion != 0:
		return fail(routing.FailQuote, "jupiter built a version %d transaction; version 0 is what can be signed", ord.TransactionVersion)
	case ord.RequestID == "" || ord.LastValidBlockHeight == 0:
		return fail(routing.FailQuote, "jupiter's order has no request id or expiry")
	}
	// The price may have moved since the quote was approved, by no more than the
	// slippage the agent allowed.
	quoted, _ := atoms(q.ExpectedOutput)
	out, ok := atoms(ord.OutAmount)
	if !ok || out < MinOut(quoted, sw.SlippageBps) {
		return fail(routing.FailQuote, "the price moved: the swap now returns %s, below %d, the quote less the slippage", ord.OutAmount, MinOut(quoted, sw.SlippageBps))
	}
	// The transaction's own floor must be at least as tight as the slippage the
	// agent allowed (Jupiter rounds down, so one atom of slack): the rail checks
	// the simulated output, but it is this floor that holds when the swap lands.
	if floor, ok := atoms(ord.OtherAmountThreshold); ord.OtherAmountThreshold != "" && (!ok || floor+1 < MinOut(out, sw.SlippageBps)) {
		return fail(routing.FailQuote, "jupiter's own minimum output, %s, is looser than the %d basis points of slippage allowed", ord.OtherAmountThreshold, sw.SlippageBps)
	}

	terms, _ := json.Marshal(Terms{
		InputMint: usdcMint(), OutputMint: sw.OutputMint, AmountMinor: sw.AmountMinor, SlippageBps: sw.SlippageBps,
		OutAmount: ord.OutAmount, RequestID: ord.RequestID, LastValidBlockHeight: ord.LastValidBlockHeight, Transaction: ord.Transaction,
	})
	auth, err := call.Pay(ctx, app.PaymentRequest{Requirements: terms, Resource: r.client.base + "/order"})
	if err != nil {
		return fail(routing.FailPayment, "%v", err)
	}
	obs.AuthorityReleased = true

	res, err := r.client.Execute(ctx, auth.Value, ord.RequestID, ord.LastValidBlockHeight)
	if err != nil {
		// The transaction is signed and may have landed whatever Jupiter said:
		// the rail finds it by its signature.
		return fail(routing.FailAmbiguous, "couldn't tell whether jupiter landed the swap (%v)", err)
	}
	obs.HTTPStatus = 200
	if !res.Success() {
		msg := res.Error
		if msg == "" {
			msg = "no reason given"
		}
		return fail(routing.FailProvider, "jupiter reported the swap failed (code %d): %s", res.Code, msg)
	}
	body, _ := json.Marshal(map[string]any{
		"status": "success", "signature": res.Signature, "input_mint": usdcMint(), "output_mint": sw.OutputMint,
		"input_amount": res.TotalInputAmount, "output_amount": res.TotalOutputAmount, "expected_output": q.ExpectedOutput,
		"slippage_bps": sw.SlippageBps, "router": ord.Router,
	})
	obs.Delivered, obs.ContentType, obs.Body, obs.SettleTx = true, "application/json", body, res.Signature
	return obs
}
