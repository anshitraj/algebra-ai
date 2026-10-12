package solana

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// UpgradeableLoader owns every upgradeable program.
var UpgradeableLoader = MustPublicKey("BPFLoaderUpgradeab1e11111111111111111111111")

// Upgradeable-loader account states, as their first four bytes (u32 LE).
const (
	loaderStateProgram     = 2
	loaderStateProgramData = 3
)

// ErrNotUpgradeable: the account is not a program the upgradeable loader owns.
var ErrNotUpgradeable = errors.New("solana: not an upgradeable program")

// programDataAddress reads a program account: the address of the account
// that holds its code and its upgrade authority.
func programDataAddress(data []byte) (PublicKey, error) {
	var pk PublicKey
	if len(data) < 4+32 || binary.LittleEndian.Uint32(data) != loaderStateProgram {
		return pk, ErrNotUpgradeable
	}
	copy(pk[:], data[4:36])
	return pk, nil
}

// programDataAuthority reads a program-data account's header: the key that
// may upgrade the program, or nil when it has been made immutable.
func programDataAuthority(data []byte) (*PublicKey, error) {
	// u32 state, u64 slot, then Option<Pubkey>: one byte, and 32 when it is 1.
	if len(data) < 4+8+1 || binary.LittleEndian.Uint32(data) != loaderStateProgramData {
		return nil, ErrNotUpgradeable
	}
	switch data[12] {
	case 0:
		return nil, nil
	case 1:
		if len(data) < 13+32 {
			return nil, ErrNotUpgradeable
		}
		var pk PublicKey
		copy(pk[:], data[13:45])
		return &pk, nil
	}
	return nil, ErrNotUpgradeable
}

// UpgradeAuthority returns the key that can replace program's code, or nil
// when the program is immutable. Whoever holds that key can change what the
// program does with every vault, so it matters as much as the code itself.
func (r *RPC) UpgradeAuthority(ctx context.Context, program PublicKey, commitment string) (*PublicKey, error) {
	acct, err := r.GetAccountInfo(ctx, program, commitment)
	if err != nil {
		return nil, err
	}
	if acct == nil || !acct.Executable || acct.Owner != UpgradeableLoader {
		return nil, ErrNotUpgradeable
	}
	dataAddr, err := programDataAddress(acct.Data)
	if err != nil {
		return nil, err
	}
	data, err := r.GetAccountInfo(ctx, dataAddr, commitment)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("%w: its program data account %s is missing", ErrNotUpgradeable, dataAddr)
	}
	return programDataAuthority(data.Data)
}
