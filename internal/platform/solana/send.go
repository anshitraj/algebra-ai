package solana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SendOptions control sendTransaction.
type SendOptions struct {
	// SkipPreflight sends without the node simulating first.
	SkipPreflight bool
	// PreflightCommitment is the commitment preflight simulates at; empty
	// means confirmed.
	PreflightCommitment string
	// MaxRetries bounds the node's own rebroadcasts; nil leaves the node's
	// default.
	MaxRetries *int
}

// SendTransaction submits a fully signed, base64-encoded transaction and
// returns its signature. A node that accepts it has only promised to forward
// it: whether it lands is for ConfirmSignature (or the chain) to say.
func (r *RPC) SendTransaction(ctx context.Context, txBase64 string, o SendOptions) (string, error) {
	opts := map[string]any{"encoding": "base64", "skipPreflight": o.SkipPreflight}
	pc := o.PreflightCommitment
	if pc == "" {
		pc = Confirmed
	}
	opts["preflightCommitment"] = pc
	if o.MaxRetries != nil {
		opts["maxRetries"] = *o.MaxRetries
	}
	var sig string
	if err := r.call(ctx, "sendTransaction", []any{txBase64, opts}, &sig); err != nil {
		return "", err
	}
	return sig, nil
}

// GetSlot returns the current slot at a commitment.
func (r *RPC) GetSlot(ctx context.Context, commitment string) (uint64, error) {
	var slot uint64
	if err := r.call(ctx, "getSlot", []any{commit(commitment)}, &slot); err != nil {
		return 0, err
	}
	return slot, nil
}

// SignatureStatus is what a node knows about one transaction signature. A nil
// status from GetSignatureStatuses means the node has never seen it (which, on
// its own, proves nothing).
type SignatureStatus struct {
	Slot               uint64          `json:"slot"`
	Confirmations      *uint64         `json:"confirmations"`
	Err                json.RawMessage `json:"err"`
	ConfirmationStatus string          `json:"confirmationStatus"`
}

// Failed reports whether the transaction landed and failed.
func (s SignatureStatus) Failed() bool {
	return len(s.Err) > 0 && string(s.Err) != "null"
}

// Reached reports whether the status is at least as final as commitment.
func (s SignatureStatus) Reached(commitment string) bool {
	rank := map[string]int{Processed: 0, Confirmed: 1, Finalized: 2}
	return rank[s.ConfirmationStatus] >= rank[commitment] && s.ConfirmationStatus != ""
}

// GetSignatureStatuses asks about up to 256 signatures. searchHistory looks
// beyond the node's recent status cache.
func (r *RPC) GetSignatureStatuses(ctx context.Context, sigs []string, searchHistory bool) ([]*SignatureStatus, error) {
	var out struct {
		Value []*SignatureStatus `json:"value"`
	}
	if err := r.call(ctx, "getSignatureStatuses", []any{sigs, map[string]any{"searchTransactionHistory": searchHistory}}, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

// ErrTransactionFailed: the transaction landed and failed on chain.
var ErrTransactionFailed = errors.New("solana: the transaction failed on chain")

// ConfirmSignature waits until a signature reaches commitment, fails, or ctx
// ends. It polls every interval (zero means 800ms).
func (r *RPC) ConfirmSignature(ctx context.Context, sig, commitment string, interval time.Duration) (*SignatureStatus, error) {
	if interval <= 0 {
		interval = 800 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		sts, err := r.GetSignatureStatuses(ctx, []string{sig}, false)
		if err == nil && len(sts) == 1 && sts[0] != nil {
			s := sts[0]
			if s.Failed() {
				return s, fmt.Errorf("%w: %s", ErrTransactionFailed, string(s.Err))
			}
			if s.Reached(commitment) {
				return s, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("solana: %s not %s before giving up: %w", sig, commitment, ctx.Err())
		case <-t.C:
		}
	}
}

// RequestAirdrop asks a test cluster's faucet for lamports. Mainnet refuses.
func (r *RPC) RequestAirdrop(ctx context.Context, to PublicKey, lamports uint64) (string, error) {
	var sig string
	if err := r.call(ctx, "requestAirdrop", []any{to.String(), lamports, commit(Confirmed)}, &sig); err != nil {
		return "", err
	}
	return sig, nil
}

// GetMinimumBalanceForRentExemption is the lamports an account of n bytes
// needs to be rent-exempt.
func (r *RPC) GetMinimumBalanceForRentExemption(ctx context.Context, n int) (uint64, error) {
	var lamports uint64
	if err := r.call(ctx, "getMinimumBalanceForRentExemption", []any{n}, &lamports); err != nil {
		return 0, err
	}
	return lamports, nil
}
