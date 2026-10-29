package payersigner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/evm"
)

// The EVM payer pays on Base and Arc (providers/evmx402): one secp256k1 key,
// the same address on every EVM chain. As on Solana, a key is generated only
// where losing it costs nothing (test networks), kept sealed in the database,
// and never silently replaced.

// EVMTestnetCluster is the stored key's name for Base Sepolia and Arc testnet.
const EVMTestnetCluster = "evm-testnet"

// EVMSource says where an EVM payer key comes from.
type EVMSource struct {
	// Cluster is EVMTestnetCluster, or "evm-mainnet" for an operator's key.
	Cluster string
	// Key is a 0x hex private key the operator holds (ModeEnv).
	Key string
	// Generate asks for a key Algebra makes and keeps (ModeGenerated, testnets only).
	Generate bool
	// ExpectAddress pins the address the key must have.
	ExpectAddress string
}

// EVMSigner is a resolved EVM payer key.
type EVMSigner struct {
	Cluster string
	Mode    Mode
	Key     *evm.Key
	Created bool
}

// ResolveEVM returns the EVM payer key. No error ever contains key material.
func ResolveEVM(ctx context.Context, src EVMSource, deps Deps) (*EVMSigner, error) {
	var out *EVMSigner
	switch {
	case src.Key != "" && src.Generate:
		return nil, fmt.Errorf("payer key for %s: choose a key or a generated signer, not both", src.Cluster)
	case src.Key != "":
		b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(src.Key), "0x"))
		if err != nil {
			return nil, fmt.Errorf("payer key for %s: the key isn't hex", src.Cluster)
		}
		k, err := evm.KeyFromBytes(b)
		if err != nil {
			return nil, fmt.Errorf("payer key for %s: %w", src.Cluster, err)
		}
		out = &EVMSigner{Cluster: src.Cluster, Mode: ModeEnv, Key: k}
	case src.Generate:
		var err error
		if out, err = resolveGeneratedEVM(ctx, src, deps); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("payer key for %s: no source configured", src.Cluster)
	}
	if want := strings.ToLower(strings.TrimSpace(src.ExpectAddress)); want != "" && out.Key.Hex() != want {
		return nil, fmt.Errorf("payer key for %s: the key has address %s but the operator pinned %s", src.Cluster, out.Key.Hex(), want)
	}
	return out, nil
}

func resolveGeneratedEVM(ctx context.Context, src EVMSource, deps Deps) (*EVMSigner, error) {
	if src.Cluster != EVMTestnetCluster {
		return nil, fmt.Errorf("payer key for %s: a generated signer is for test networks only", src.Cluster)
	}
	if deps.Store == nil || deps.Enc == nil {
		return nil, fmt.Errorf("payer key for %s: a generated signer needs the database and the master key", src.Cluster)
	}
	row, err := deps.Store.Signer(ctx, src.Cluster)
	switch {
	case err == nil:
		k, err := openEVM(src.Cluster, row, deps)
		if err != nil {
			return nil, err
		}
		return &EVMSigner{Cluster: src.Cluster, Mode: ModeGenerated, Key: k}, nil
	case !errors.Is(err, shared.ErrNotFound):
		return nil, fmt.Errorf("payer key for %s: reading the stored signer: %w", src.Cluster, err)
	}
	if pin := strings.TrimSpace(src.ExpectAddress); pin != "" {
		return nil, fmt.Errorf("payer key for %s: the operator pinned %s but no signer is stored; restore the database row or remove the pin on purpose", src.Cluster, pin)
	}
	k, err := evm.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: generating: %w", src.Cluster, err)
	}
	sealed, nonce, err := deps.Enc.Encrypt(k.Bytes(), aad(src.Cluster, k.Hex()))
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: sealing: %w", src.Cluster, err)
	}
	inserted, err := deps.Store.SaveSigner(ctx, &Stored{Cluster: src.Cluster, PublicKey: k.Hex(), Sealed: sealed, Nonce: nonce, CreatedAt: time.Now().UTC()})
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: storing: %w", src.Cluster, err)
	}
	if inserted {
		return &EVMSigner{Cluster: src.Cluster, Mode: ModeGenerated, Key: k, Created: true}, nil
	}
	// Another server stored its key first: use that one.
	row, err = deps.Store.Signer(ctx, src.Cluster)
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: reading the stored signer: %w", src.Cluster, err)
	}
	won, err := openEVM(src.Cluster, row, deps)
	if err != nil {
		return nil, err
	}
	return &EVMSigner{Cluster: src.Cluster, Mode: ModeGenerated, Key: won}, nil
}

func openEVM(cluster string, row *Stored, deps Deps) (*evm.Key, error) {
	plain, err := deps.Enc.Decrypt(row.Sealed, row.Nonce, aad(cluster, row.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: the stored signer %s can't be opened (ALGEBRA_MASTER_KEY must be the one it was sealed with); a new one was NOT generated", cluster, row.PublicKey)
	}
	k, err := evm.KeyFromBytes(plain)
	if err != nil || k.Hex() != row.PublicKey {
		return nil, fmt.Errorf("payer key for %s: the stored signer doesn't open to the address recorded (%s); the row was altered", cluster, row.PublicKey)
	}
	return k, nil
}
