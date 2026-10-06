package solana

import (
	"context"
	"strconv"
)

// TokenDelta is how one token account's balance changed in a transaction.
type TokenDelta struct {
	Account string
	Mint    string
	Owner   string
	Pre     uint64
	Post    uint64
}

// Change is Post minus Pre, signed.
func (d TokenDelta) Change() int64 { return int64(d.Post) - int64(d.Pre) }

// TxBalances is a landed transaction's token balance changes: what moved,
// whether the transfers were top-level or made by a program (as a payment
// channel's distribute makes them).
type TxBalances struct {
	Slot       uint64
	BlockTime  *int64
	Err        any
	Signatures []string
	Deltas     map[string]TokenDelta // by token account address
}

// GetTransactionBalances fetches a transaction's token balance changes, or nil
// when the node doesn't have it at that commitment.
func (r *RPC) GetTransactionBalances(ctx context.Context, signature, commitment string) (*TxBalances, error) {
	type bal struct {
		AccountIndex  int    `json:"accountIndex"`
		Mint          string `json:"mint"`
		Owner         string `json:"owner"`
		UITokenAmount struct {
			Amount string `json:"amount"`
		} `json:"uiTokenAmount"`
	}
	var out *struct {
		Slot      uint64 `json:"slot"`
		BlockTime *int64 `json:"blockTime"`
		Meta      *struct {
			Err               any   `json:"err"`
			PreTokenBalances  []bal `json:"preTokenBalances"`
			PostTokenBalances []bal `json:"postTokenBalances"`
		} `json:"meta"`
		Transaction struct {
			Signatures []string `json:"signatures"`
			Message    struct {
				AccountKeys []struct {
					Pubkey string `json:"pubkey"`
				} `json:"accountKeys"`
			} `json:"message"`
		} `json:"transaction"`
	}
	params := []any{signature, map[string]any{"encoding": "jsonParsed", "commitment": commitment, "maxSupportedTransactionVersion": 255}}
	if err := r.call(ctx, "getTransaction", params, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	tb := &TxBalances{Slot: out.Slot, BlockTime: out.BlockTime, Signatures: out.Transaction.Signatures, Deltas: map[string]TokenDelta{}}
	if out.Meta == nil {
		return tb, nil
	}
	tb.Err = out.Meta.Err
	keys := out.Transaction.Message.AccountKeys
	amount := func(s string) uint64 { n, _ := strconv.ParseUint(s, 10, 64); return n }
	addr := func(i int) string {
		if i >= 0 && i < len(keys) {
			return keys[i].Pubkey
		}
		return ""
	}
	for _, b := range out.Meta.PreTokenBalances {
		a := addr(b.AccountIndex)
		d := tb.Deltas[a]
		d.Account, d.Mint, d.Owner, d.Pre = a, b.Mint, b.Owner, amount(b.UITokenAmount.Amount)
		tb.Deltas[a] = d
	}
	for _, b := range out.Meta.PostTokenBalances {
		a := addr(b.AccountIndex)
		d := tb.Deltas[a]
		d.Account, d.Mint, d.Owner, d.Post = a, b.Mint, b.Owner, amount(b.UITokenAmount.Amount)
		tb.Deltas[a] = d
	}
	return tb, nil
}

// Failed reports whether the transaction landed and failed.
func (t *TxBalances) Failed() bool { return t.Err != nil }
