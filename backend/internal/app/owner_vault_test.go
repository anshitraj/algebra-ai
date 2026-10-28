package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/shared"
)

// A proof made by the browser wallet (frontend/lib/wallet/proof.ts) from the
// well-known "abandon … about" phrase, for account user_test.
var browserProof = OwnershipProof{
	SolanaAddress:   "HAgk14JpMQLgt6rVgv7cBQFJWFto5Dqxi472uT3DKpqk",
	EVMAddress:      "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
	Message:         "Algebra vault ownership\nAccount: user_test\nSolana: HAgk14JpMQLgt6rVgv7cBQFJWFto5Dqxi472uT3DKpqk\nEVM: 0x9858EfFD232B4033E47d90003D41EC34EcaEda94\nIssued: 2026-10-29T10:00:00.000Z",
	SolanaSignature: "4JQuwgvRus42yrLVA6LfDwcTmg6L57djJ7qyGwnag3yJMaf8XpAhxTx4GdUVkDdEyGeoMZQvy6VvBVSXT8ifpgRC",
	EVMSignature:    "0xef49bb5ebf3bf1d3dd0ac70d335278658ee2bd137d8f1fc50c61c5c84c56cc95353f1fab79c918f39d0598667da90715b91a7db7eb09b62cbb5aed7c3b0396711c",
}

type memVaults struct{ m map[string]*OwnerVaultRecord }

func (s *memVaults) OwnerVault(_ context.Context, id string) (*OwnerVaultRecord, error) {
	if v, ok := s.m[id]; ok {
		c := *v
		return &c, nil
	}
	return nil, shared.ErrNotFound
}

func (s *memVaults) SaveOwnerVault(_ context.Context, v *OwnerVaultRecord) error {
	for id, x := range s.m {
		if id != v.UserID && (x.SolanaAddress == v.SolanaAddress || x.EVMAddress == v.EVMAddress) {
			return shared.ErrConflict
		}
	}
	c := *v
	s.m[v.UserID] = &c
	return nil
}

func vaultService(at time.Time) (*OwnerVaultService, *memVaults) {
	store := &memVaults{m: map[string]*OwnerVaultRecord{}}
	s := NewOwnerVaultService(store, nil, nil, false, nil)
	s.now = func() time.Time { return at }
	return s, store
}

func TestOwnerVault_RegisterChecksBothSignaturesAndTheAccount(t *testing.T) {
	ctx := context.Background()
	s, store := vaultService(time.Date(2026, 10, 29, 10, 5, 0, 0, time.UTC))

	v, err := s.Register(ctx, "user_test", browserProof)
	if err != nil {
		t.Fatal(err)
	}
	if v.EVMAddress != "0x9858effd232b4033e47d90003d41ec34ecaeda94" || v.SolanaAddress != browserProof.SolanaAddress {
		t.Fatalf("recorded %+v", v)
	}

	// The same proof for another account: the message names user_test.
	if _, err := s.Register(ctx, "user_other", browserProof); !errors.Is(err, ErrVaultProof) {
		t.Fatalf("another account: %v", err)
	}
	// A signature from another key, or an edited address, is refused.
	bad := browserProof
	bad.EVMSignature = strings.Replace(bad.EVMSignature, "ef49", "ef48", 1)
	if _, err := s.Register(ctx, "user_test", bad); !errors.Is(err, ErrVaultProof) {
		t.Fatalf("tampered EVM signature: %v", err)
	}
	bad = browserProof
	bad.SolanaSignature = "4JQuwgvRus42yrLVA6LfDwcTmg6L57djJ7qyGwnag3yJMaf8XpAhxTx4GdUVkDdEyGeoMZQvy6VvBVSXT8ifpgRD"
	if _, err := s.Register(ctx, "user_test", bad); !errors.Is(err, ErrVaultProof) {
		t.Fatalf("tampered Solana signature: %v", err)
	}
	// An old proof is refused.
	late, _ := vaultService(time.Date(2026, 10, 29, 11, 0, 0, 0, time.UTC))
	if _, err := late.Register(ctx, "user_test", browserProof); !errors.Is(err, ErrVaultProof) {
		t.Fatalf("stale proof: %v", err)
	}
	// An address another account already holds is not taken over.
	store.m["user_test"].UserID = "user_test"
	store.m["user_x"] = &OwnerVaultRecord{UserID: "user_x", SolanaAddress: browserProof.SolanaAddress, EVMAddress: "0x01"}
	delete(store.m, "user_test")
	if _, err := s.Register(ctx, "user_test", browserProof); !errors.Is(err, ErrVaultTaken) {
		t.Fatalf("taken: %v", err)
	}
}

func TestOwnerVault_NoChainNoVault(t *testing.T) {
	ctx := context.Background()
	s, _ := vaultService(time.Date(2026, 10, 29, 10, 5, 0, 0, time.UTC))
	if _, err := s.Get(ctx, "nobody"); !errors.Is(err, ErrVaultNone) {
		t.Fatalf("no vault: %v", err)
	}
	if _, err := s.Register(ctx, "user_test", browserProof); err != nil {
		t.Fatal(err)
	}
	// No node for any chain here: nothing to withdraw on or relay to.
	if _, err := s.Broadcast(ctx, "user_test", "base-sepolia", "0x02"); !errors.Is(err, ErrVaultChain) {
		t.Fatalf("no node: %v", err)
	}
	for _, c := range s.Chains() {
		if c.Available || c.Deposits {
			t.Fatalf("no nodes, so nothing available: %+v", c)
		}
	}
}
