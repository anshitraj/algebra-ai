// Package payersigner decides where Algebra's payer key comes from, once, at
// start.
//
// The payer is every on-chain Spend Pass's agent and the account every pull
// pays into, and a pass's destination is fixed when the pass is made. So a
// different payer key cannot pull from any existing vault (the owner can
// still withdraw and close it). Two rules follow, and this package is where
// they are kept:
//
//   - A key is generated only where losing it costs nothing: devnet, and only
//     when nothing in the database depends on a previous one.
//   - A key is never replaced without anyone noticing. A stored key that can't
//     be opened, or doesn't match the pin the operator set, stops the server;
//     a new one is not made to paper over it.
package payersigner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/platform/solana"
)

// Mode is where a cluster's payer key comes from.
type Mode string

const (
	// ModeEnv: a keypair in SOLANA_*_KEYPAIR or SOLANA_*_KEYPAIR_FILE. The
	// operator holds the key; local development and bring-your-own wallets.
	ModeEnv Mode = "env"
	// ModeGenerated: Algebra made the key itself and keeps it sealed in the
	// database. Devnet only.
	ModeGenerated Mode = "generated"
)

// GeneratedClusters are the clusters a generated key may serve.
var GeneratedClusters = map[string]bool{"devnet": true}

// Source says where one cluster's key comes from (from config).
type Source struct {
	// Cluster is "devnet" or "mainnet"; Network is the chain.* name passes
	// are recorded under, used to ask whether any vault depends on a key.
	Cluster, Network string
	// Keypair and KeypairFile are an operator-held key (ModeEnv).
	Keypair, KeypairFile string
	// Generate asks for a key Algebra makes and keeps itself (ModeGenerated).
	Generate bool
	// ExpectPubkey, when set, pins the address the key must have.
	ExpectPubkey string
}

// Stored is a generated key as it sits in the database.
type Stored struct {
	Cluster   string
	PublicKey string
	Sealed    []byte
	Nonce     []byte
	CreatedAt time.Time
}

// Store keeps generated keys.
type Store interface {
	// Signer returns the cluster's stored key, or an error wrapping
	// shared.ErrNotFound.
	Signer(ctx context.Context, cluster string) (*Stored, error)
	// SaveSigner stores s unless the cluster already has one, and says
	// whether it did. Two servers starting at once end up with one key.
	SaveSigner(ctx context.Context, s *Stored) (inserted bool, err error)
	// HasPasses reports whether any on-chain pass is recorded on network.
	HasPasses(ctx context.Context, network string) (bool, error)
}

// Deps are what generated keys need.
type Deps struct {
	Store Store
	Enc   privacy.Encryptor
}

// Signer is a cluster's resolved payer key.
type Signer struct {
	Cluster string
	Mode    Mode
	Key     *solana.Keypair
	// Created: this start made the key. Operators should note its address and
	// fund it; it is also worth pinning (SOLANA_DEVNET_SIGNER_PUBKEY).
	Created bool
}

// NewEncryptor seals generated keys under a key derived from the master key
// and used for nothing else.
func NewEncryptor(masterKey []byte) (*privacy.AESGCMEncryptor, error) {
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("algebra:payer-signer:v1"))
	return privacy.NewAESGCMEncryptor(mac.Sum(nil))
}

func aad(cluster, publicKey string) []byte {
	return []byte("algebra:payer-signer:" + cluster + ":" + publicKey)
}

// Resolve returns the payer key for one cluster. No error ever contains key
// material.
func Resolve(ctx context.Context, src Source, deps Deps) (*Signer, error) {
	local := src.Keypair != "" || src.KeypairFile != ""
	if local && src.Generate {
		return nil, fmt.Errorf("payer key for %s: choose a keypair or a generated signer, not both", src.Cluster)
	}
	var (
		out *Signer
		err error
	)
	switch {
	case src.Generate:
		out, err = resolveGenerated(ctx, src, deps)
	case local:
		out, err = resolveLocal(src)
	default:
		return nil, fmt.Errorf("payer key for %s: no source configured", src.Cluster)
	}
	if err != nil {
		return nil, err
	}
	if want := strings.TrimSpace(src.ExpectPubkey); want != "" && out.Key.PublicKey().String() != want {
		return nil, fmt.Errorf("payer key for %s: the key has address %s but the operator pinned %s. "+
			"Existing vaults only work with the pinned key. Restore it (the database row, or the keypair), "+
			"or change the pin on purpose, knowing every vault made for the old key has to be withdrawn and made again",
			src.Cluster, out.Key.PublicKey(), want)
	}
	return out, nil
}

func resolveLocal(src Source) (*Signer, error) {
	text := src.Keypair
	if src.KeypairFile != "" {
		b, err := os.ReadFile(src.KeypairFile)
		if err != nil {
			return nil, fmt.Errorf("the %s keypair file: %w", src.Cluster, err)
		}
		text = string(b)
	}
	kp, err := solana.ParseKeypair(text)
	if err != nil {
		return nil, fmt.Errorf("the %s Solana wallet couldn't be loaded: %w", src.Cluster, err)
	}
	return &Signer{Cluster: src.Cluster, Mode: ModeEnv, Key: kp}, nil
}

func resolveGenerated(ctx context.Context, src Source, deps Deps) (*Signer, error) {
	if !GeneratedClusters[src.Cluster] {
		return nil, fmt.Errorf("payer key for %s: a generated signer is for devnet only. "+
			"Real money needs a key held outside this server (Cloud KMS) or one the operator controls", src.Cluster)
	}
	if deps.Store == nil || deps.Enc == nil {
		return nil, fmt.Errorf("payer key for %s: a generated signer needs the database and the master key", src.Cluster)
	}
	row, err := deps.Store.Signer(ctx, src.Cluster)
	switch {
	case err == nil:
		kp, err := open(src.Cluster, row, deps.Enc)
		if err != nil {
			return nil, err
		}
		return &Signer{Cluster: src.Cluster, Mode: ModeGenerated, Key: kp}, nil
	case !errors.Is(err, shared.ErrNotFound):
		return nil, fmt.Errorf("payer key for %s: reading the stored signer: %w", src.Cluster, err)
	}

	// Nothing stored. A key is made only if nothing could depend on one that
	// existed before.
	if pin := strings.TrimSpace(src.ExpectPubkey); pin != "" {
		return nil, fmt.Errorf("payer key for %s: the operator pinned %s but no signer is stored. "+
			"The database row is missing: restore the database, or remove the pin on purpose, "+
			"knowing a new key cannot pull from vaults made for %s", src.Cluster, pin, pin)
	}
	has, err := deps.Store.HasPasses(ctx, src.Network)
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: checking for existing vaults: %w", src.Cluster, err)
	}
	if has {
		return nil, fmt.Errorf("payer key for %s: on-chain vaults exist for this network but no signer is stored, "+
			"so they were made for a key this database no longer has. A new key would not be able to use them. "+
			"Restore the database row, or have their owners withdraw and remove the vaults first", src.Cluster)
	}

	kp, err := solana.NewKeypair()
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: generating: %w", src.Cluster, err)
	}
	pub := kp.PublicKey().String()
	sealed, nonce, err := deps.Enc.Encrypt([]byte(kp.ExportKeygenJSON()), aad(src.Cluster, pub))
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: sealing: %w", src.Cluster, err)
	}
	inserted, err := deps.Store.SaveSigner(ctx, &Stored{Cluster: src.Cluster, PublicKey: pub, Sealed: sealed, Nonce: nonce, CreatedAt: time.Now().UTC()})
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: storing: %w", src.Cluster, err)
	}
	if inserted {
		return &Signer{Cluster: src.Cluster, Mode: ModeGenerated, Key: kp, Created: true}, nil
	}
	// Another server started at the same moment and stored its key first:
	// use that one, so every server agrees.
	row, err = deps.Store.Signer(ctx, src.Cluster)
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: reading the stored signer: %w", src.Cluster, err)
	}
	won, err := open(src.Cluster, row, deps.Enc)
	if err != nil {
		return nil, err
	}
	return &Signer{Cluster: src.Cluster, Mode: ModeGenerated, Key: won}, nil
}

// Load opens the key stored for a cluster and never makes one: for an
// operator's recovery tool (cmd/solana-wallet -generated).
func Load(ctx context.Context, cluster string, deps Deps) (*solana.Keypair, *Stored, error) {
	if deps.Store == nil || deps.Enc == nil {
		return nil, nil, errors.New("payer key: the database and the master key are needed")
	}
	row, err := deps.Store.Signer(ctx, cluster)
	if err != nil {
		return nil, nil, err
	}
	kp, err := open(cluster, row, deps.Enc)
	if err != nil {
		return nil, nil, err
	}
	return kp, row, nil
}

// open unseals a stored key. A failure here never leads to a new key.
func open(cluster string, row *Stored, enc privacy.Encryptor) (*solana.Keypair, error) {
	plain, err := enc.Decrypt(row.Sealed, row.Nonce, aad(cluster, row.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: the stored signer %s can't be opened. ALGEBRA_MASTER_KEY must be the "+
			"one it was sealed with (or the row was altered). A new signer was NOT generated, because vaults "+
			"made for %s could not use it", cluster, row.PublicKey, row.PublicKey)
	}
	kp, err := solana.ParseKeypair(string(plain))
	if err != nil {
		return nil, fmt.Errorf("payer key for %s: the stored signer %s isn't a keypair", cluster, row.PublicKey)
	}
	if kp.PublicKey().String() != row.PublicKey {
		return nil, fmt.Errorf("payer key for %s: the stored signer opens to a different address than the one recorded (%s); the row was altered", cluster, row.PublicKey)
	}
	return kp, nil
}
