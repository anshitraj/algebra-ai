package solana

import (
	"context"
	"errors"
	"fmt"
)

// AccountsSimulation is a simulation that also returns what the named accounts
// would look like afterwards: how a transaction somebody else built is judged
// before it is signed.
type AccountsSimulation struct {
	Err           any
	Logs          []string
	UnitsConsumed uint64
	// Accounts line up with the addresses asked about; nil is an account that
	// would not exist.
	Accounts []*AccountInfo
}

// SimulateWithAccounts runs a transaction against the node's state without
// sending it and returns the state of the given accounts after it. Signatures
// aren't verified, so an unsigned transaction can be checked, and the blockhash
// is replaced with a current one.
func (r *RPC) SimulateWithAccounts(ctx context.Context, txBase64 string, addresses []PublicKey, commitment string) (*AccountsSimulation, error) {
	if len(addresses) == 0 || len(addresses) > maxMultiple {
		return nil, fmt.Errorf("solana: a simulation can return between 1 and %d accounts", maxMultiple)
	}
	addrs := make([]string, len(addresses))
	for i, a := range addresses {
		addrs[i] = a.String()
	}
	var out struct {
		Value struct {
			Err           any            `json:"err"`
			Logs          []string       `json:"logs"`
			UnitsConsumed uint64         `json:"unitsConsumed"`
			Accounts      []*accountJSON `json:"accounts"`
		} `json:"value"`
	}
	opts := map[string]any{
		"encoding": "base64", "sigVerify": false, "replaceRecentBlockhash": true, "commitment": commitment,
		"accounts": map[string]any{"encoding": "base64", "addresses": addrs},
	}
	if err := r.call(ctx, "simulateTransaction", []any{txBase64, opts}, &out); err != nil {
		return nil, err
	}
	sim := &AccountsSimulation{Err: out.Value.Err, Logs: out.Value.Logs, UnitsConsumed: out.Value.UnitsConsumed}
	if out.Value.Err != nil {
		// A transaction that fails has no state to judge, and the node may not
		// return any.
		return sim, nil
	}
	if len(out.Value.Accounts) != len(addresses) {
		return nil, errors.New("solana: the simulation returned a different number of accounts than were asked for")
	}
	sim.Accounts = make([]*AccountInfo, len(addresses))
	for i, a := range out.Value.Accounts {
		var err error
		if sim.Accounts[i], err = a.info(); err != nil {
			return nil, err
		}
	}
	return sim, nil
}
