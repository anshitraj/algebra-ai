package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// Defaults for how long a paid-for answer is kept and how large it may be.
const (
	DefaultResultRetention = 24 * time.Hour
	DefaultResultMaxBytes  = 1 << 20
)

// StoredResult is a provider's answer for a paid call, as handed back.
type StoredResult struct {
	IntentID      string
	ReservationID string
	ContentType   string
	HTTPStatus    int
	Body          []byte
	// SHA256 is "sha256:<hex>" of Body: the hash the receipt carries.
	SHA256    string
	StoredAt  time.Time
	ExpiresAt time.Time
}

// SealedResult is what a store holds: everything about the answer but its
// plaintext. A store never sees the body.
type SealedResult struct {
	IntentID      string
	PrincipalID   string
	ReservationID string
	ContentType   string
	HTTPStatus    int
	Size          int
	SHA256        string
	Sealed        []byte
	Nonce         []byte
	StoredAt      time.Time
	ExpiresAt     time.Time
}

// ResultBlobStore persists sealed results, one per intent.
type ResultBlobStore interface {
	// SaveSealed inserts the result for an intent, or replaces it.
	SaveSealed(ctx context.Context, r SealedResult) error
	// GetSealed returns the result for an intent that has not expired at now,
	// or shared.ErrNotFound.
	GetSealed(ctx context.Context, intentID string, now time.Time) (*SealedResult, error)
	// DeleteExpired removes what expired at or before now, and says how many.
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}

// ResultVault keeps the answers to paid calls: sealed at rest, for a limited
// time and up to a limited size. It exists so that asking again for something
// already paid for returns the answer instead of nothing.
type ResultVault struct {
	store     ResultBlobStore
	enc       privacy.Encryptor
	retention time.Duration
	maxBytes  int
	now       func() time.Time
}

// NewResultVault builds the vault. A retention of zero or less means answers
// are not kept at all; maxBytes defaults to DefaultResultMaxBytes.
func NewResultVault(store ResultBlobStore, enc privacy.Encryptor, retention time.Duration, maxBytes int) *ResultVault {
	if maxBytes <= 0 {
		maxBytes = DefaultResultMaxBytes
	}
	return &ResultVault{store: store, enc: enc, retention: retention, maxBytes: maxBytes, now: time.Now}
}

// Enabled reports whether answers are kept at all.
func (v *ResultVault) Enabled() bool { return v != nil && v.store != nil && v.retention > 0 }

// Retention is how long an answer is kept.
func (v *ResultVault) Retention() time.Duration { return v.retention }

// aad binds a sealed body to the intent, the attempt and the person it
// belongs to: a row moved to another intent or person fails to open.
func resultAAD(principalID, intentID, reservationID string) []byte {
	return []byte("algebra:intent-result:v1|" + principalID + "|" + intentID + "|" + reservationID)
}

// Kept is what Keep decided.
type Kept struct {
	Stored    bool
	ExpiresAt time.Time
	// Reason says why nothing was kept: "disabled", "empty" or "too_large".
	Reason string
	Size   int
}

// Keep seals and stores the answer for an attempt. An answer over the size
// limit is not kept (a truncated answer would pass for the real one) and says
// so; the call itself is not affected.
func (v *ResultVault) Keep(ctx context.Context, principalID, intentID, reservationID, contentType string, status int, body []byte) (Kept, error) {
	switch {
	case !v.Enabled():
		return Kept{Reason: "disabled"}, nil
	case len(body) == 0:
		return Kept{Reason: "empty"}, nil
	case len(body) > v.maxBytes:
		return Kept{Reason: "too_large", Size: len(body)}, nil
	}
	sealed, nonce, err := v.enc.Encrypt(body, resultAAD(principalID, intentID, reservationID))
	if err != nil {
		return Kept{}, fmt.Errorf("sealing a result: %w", err)
	}
	now := v.now().UTC()
	r := SealedResult{
		IntentID: intentID, PrincipalID: principalID, ReservationID: reservationID, ContentType: contentType, HTTPStatus: status,
		Size: len(body), SHA256: econ.HashBytes(body), Sealed: sealed, Nonce: nonce, StoredAt: now, ExpiresAt: now.Add(v.retention),
	}
	if err := v.store.SaveSealed(ctx, r); err != nil {
		return Kept{}, err
	}
	return Kept{Stored: true, ExpiresAt: r.ExpiresAt, Size: len(body)}, nil
}

// Get opens the kept answer for an intent: shared.ErrNotFound when there is
// none or it has expired. It checks the plaintext against the hash stored with
// it, so a body that doesn't match what was sealed is never returned.
func (v *ResultVault) Get(ctx context.Context, principalID, intentID string) (*StoredResult, error) {
	if !v.Enabled() {
		return nil, shared.ErrNotFound
	}
	r, err := v.store.GetSealed(ctx, intentID, v.now())
	if err != nil {
		return nil, err
	}
	if r.PrincipalID != principalID {
		return nil, shared.ErrNotFound
	}
	body, err := v.enc.Decrypt(r.Sealed, r.Nonce, resultAAD(r.PrincipalID, r.IntentID, r.ReservationID))
	if err != nil {
		return nil, errors.New("a kept result couldn't be opened")
	}
	if econ.HashBytes(body) != r.SHA256 {
		return nil, errors.New("a kept result doesn't match its hash")
	}
	return &StoredResult{
		IntentID: r.IntentID, ReservationID: r.ReservationID, ContentType: r.ContentType, HTTPStatus: r.HTTPStatus,
		Body: body, SHA256: r.SHA256, StoredAt: r.StoredAt, ExpiresAt: r.ExpiresAt,
	}, nil
}

// Purge removes what has expired and says how many answers that was.
func (v *ResultVault) Purge(ctx context.Context) (int, error) {
	if v == nil || v.store == nil {
		return 0, nil
	}
	return v.store.DeleteExpired(ctx, v.now())
}
