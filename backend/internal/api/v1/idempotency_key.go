package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

// An Idempotency-Key makes one request one request: send the same key again (after a timeout, a dropped
// connection, a retry in the client's code) and the answer is the first one, never a second payment.
//
// The key becomes the request's window, the part of its identity that says "the same one as before": the
// coordinator already pays one outcome once and hands the kept answer back, so a retry that changes the
// window by accident can't buy twice. With a key the request's own `window` is ignored. The same key
// with a different request is refused, because one of the two is not what its sender meant.

// maxIdempotencyKey is the longest key accepted (Stripe's limit, and what clients generate: UUIDs, ULIDs).
const maxIdempotencyKey = 255

var idempotencyKeyRE = regexp.MustCompile(`^[\x21-\x7e]+(?: [\x21-\x7e]+)*$`)

// idempotencyKeyReused is the refusal of a key already used for a different request.
type idempotencyKeyReused struct{}

func (idempotencyKeyReused) Error() string {
	return "this Idempotency-Key was already used for a different request: send the same request again to get its answer, or a new key to make a new one"
}

// applyIdempotencyKey makes the key the request's window and refuses a key already used for a different
// request. The returned func records the request under the key once it was accepted for processing (it is
// not called for one refused as malformed, so correcting it and trying again with the same key works).
func (a *API) applyIdempotencyKey(r *http.Request, userID string, req *executeRequest) (finish func(err error), err error) {
	noop := func(error) {}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return noop, nil
	}
	if len(key) > maxIdempotencyKey || !idempotencyKeyRE.MatchString(key) {
		return noop, badRequest("Idempotency-Key must be 1 to 255 printable characters, such as a UUID")
	}
	fp := requestFingerprint(*req)
	sum := sha256.Sum256([]byte(userID + "\x00" + key))
	req.Window = "idem-" + hex.EncodeToString(sum[:])[:24]
	if a.b.Idempotency == nil {
		return noop, nil
	}
	scope := "execute:" + userID
	ctx := r.Context()
	prev, done, err := a.b.Idempotency.Begin(ctx, key, scope)
	if err != nil {
		return noop, err
	}
	if done {
		if string(prev) != fp {
			return noop, idempotencyKeyReused{}
		}
		return noop, nil
	}
	return func(procErr error) {
		var badSpec *econ.InvalidSpecError
		if errors.As(procErr, &badSpec) {
			return
		}
		// Not tied to the request's context: a hung-up client must not leave the key open.
		_ = a.b.Idempotency.Complete(context.WithoutCancel(ctx), key, scope, []byte(fp))
	}, nil
}

// requestFingerprint identifies what a request asks for, apart from how it is spelled: the same capability,
// input and limits give the same fingerprint, whatever the whitespace and whether the amount was sent as
// max_usd or budget_max_minor. The window is left out, since the key replaces it.
func requestFingerprint(req executeRequest) string {
	req.Window, req.MaxUSD = "", ""
	if c, err := econ.Canonicalize(req.Input); err == nil {
		req.Input = c
	}
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeIdempotencyError answers a key problem: a reused key is a conflict, anything else is as writeError says.
func writeIdempotencyError(w http.ResponseWriter, err error) {
	var reused idempotencyKeyReused
	if errors.As(err, &reused) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "reason": "idempotency_key_reused"})
		return
	}
	writeError(w, err)
}
