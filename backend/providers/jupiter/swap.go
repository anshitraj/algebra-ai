package jupiter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/chain"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

const (
	// RailName is how reservations name the swap rail.
	RailName = "solana-swap"
	// Capability is what an agent asks for to buy a token with USDC.
	Capability = "solana.swap"
	// Provider is the provider ID policy and receipts use for Jupiter.
	Provider = "jupiter"

	// DefaultSlippageBps is the tolerance used when the agent names none: 0.5%.
	DefaultSlippageBps = 50
	// DefaultMaxSlippageBps is the most slippage an agent may ask for: 1%.
	DefaultMaxSlippageBps = 100
	// DefaultMaxSwapMinor is the most one swap may spend, in micro-USDC: $5, a
	// ceiling for a capability that is new. Raise it on purpose.
	DefaultMaxSwapMinor = 5_000_000

	// quoteValidFor is how long a quote stays payable. Short: a token's price
	// moves in seconds.
	quoteValidFor = 20 * time.Second
)

// wrappedSOL is the mint of wrapped SOL. Swapping into it delivers native SOL,
// which the rail cannot account for the way it does a token, so it is refused.
const wrappedSOL = "So11111111111111111111111111111111111111112"

// usdcMint is Circle's USDC on Solana mainnet, the only thing a swap spends.
func usdcMint() string {
	m, _ := chain.AssetAddress(chain.Solana, "USDC")
	return m
}

// Swap is what an agent asks for: buy a token with USDC.
type Swap struct {
	OutputMint  string
	AmountMinor int64 // micro-USDC
	SlippageBps int
}

// inputJSON is the capability's input.
type inputJSON struct {
	OutputMint     string          `json:"output_mint"`
	AmountUSDC     json.RawMessage `json:"amount_usdc"`
	MaxSlippageBps *int            `json:"max_slippage_bps"`
}

// ParseInput reads and checks the input of a solana.swap request. maxSlippage
// is the most this server allows; an agent may ask for less.
func ParseInput(raw json.RawMessage, maxSlippage int) (Swap, error) {
	if len(raw) == 0 {
		return Swap{}, errors.New(`input needs output_mint and amount_usdc, for example {"output_mint":"<token mint>","amount_usdc":"2.50"}`)
	}
	var in inputJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return Swap{}, fmt.Errorf("input must be an object with output_mint, amount_usdc and optionally max_slippage_bps: %v", err)
	}
	pk, err := solana.ParsePublicKey(strings.TrimSpace(in.OutputMint))
	if err != nil || pk.IsZero() {
		return Swap{}, errors.New("output_mint must be a token's mint address")
	}
	out := pk.String()
	switch out {
	case usdcMint():
		return Swap{}, errors.New("output_mint is USDC, which is what the swap spends")
	case wrappedSOL:
		return Swap{}, errors.New("swapping into SOL isn't supported: only tokens can be bought")
	}
	amount, err := chain.ParseUnits(strings.Trim(strings.TrimSpace(string(in.AmountUSDC)), `"`), chain.USDCDecimals)
	if err != nil || amount <= 0 {
		return Swap{}, errors.New(`amount_usdc must be a positive amount of USDC such as "2.50"`)
	}
	slippage := DefaultSlippageBps
	if in.MaxSlippageBps != nil {
		slippage = *in.MaxSlippageBps
	}
	if maxSlippage <= 0 {
		maxSlippage = DefaultMaxSlippageBps
	}
	if slippage < 1 || slippage > maxSlippage {
		return Swap{}, fmt.Errorf("max_slippage_bps must be between 1 and %d (basis points: 50 is 0.5%%)", maxSlippage)
	}
	return Swap{OutputMint: out, AmountMinor: amount, SlippageBps: slippage}, nil
}

// Terms is what the rail is asked to sign: the swap, and the transaction
// Jupiter built for it. It travels as the payment requirements.
type Terms struct {
	InputMint   string `json:"input_mint"`
	OutputMint  string `json:"output_mint"`
	AmountMinor int64  `json:"amount_minor"`
	SlippageBps int    `json:"slippage_bps"`
	// OutAmount is the output Jupiter quoted, in atoms of the output mint; the
	// least acceptable is derived from it and the slippage.
	OutAmount            string `json:"out_amount"`
	RequestID            string `json:"request_id"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
	// Transaction is the unsigned transaction, base64.
	Transaction string `json:"transaction"`
}

// MinOut is the least output the swap may deliver: the quote less the
// slippage, rounded in the wallet's favour (the slippage rounds down, so the
// minimum rounds up).
func MinOut(outAmount uint64, slippageBps int) uint64 {
	s := uint64(min(max(slippageBps, 0), 10_000))
	hi, lo := bits.Mul64(outAmount, s)
	slip, _ := bits.Div64(hi, lo, 10_000) // hi < 10_000, as s <= 10_000
	return outAmount - slip
}
