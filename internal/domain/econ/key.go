package econ

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Canonicalize returns a deterministic encoding of a JSON value: object keys
// sorted, no insignificant whitespace, integers as integers, other numbers
// in shortest round-trip form, and no HTML escaping. Two inputs that mean
// the same JSON value always canonicalize to the same bytes, which is what
// makes an effect key deterministic. Empty input canonicalizes to "{}".
func Canonicalize(raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value")
	}
	norm, err := normalize(v, 0)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(norm); err != nil {
		return nil, err
	}
	b := bytes.TrimRight(out.Bytes(), "\n")
	if len(b) > 16<<10 {
		return nil, errors.New("input is larger than 16 KiB")
	}
	return b, nil
}

// normalize rewrites numbers into canonical form; maps are sorted by
// encoding/json itself.
func normalize(v any, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("input nests more than 32 levels")
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			n, err := normalize(val, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			n, err := normalize(val, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case json.Number:
		if i, err := strconv.ParseInt(t.String(), 10, 64); err == nil {
			return json.Number(strconv.FormatInt(i, 10)), nil
		}
		f, err := strconv.ParseFloat(t.String(), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("number %s out of range", t)
		}
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			return json.Number(strconv.FormatInt(int64(f), 10)), nil
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	default:
		return v, nil
	}
}

// EffectKey is the deterministic economic identity of an outcome.
func EffectKey(capability, inputHash, window string, quantity int) string {
	h := inputHash
	if len(h) > 24 {
		h = h[:24]
	}
	return fmt.Sprintf("%s:%s:%s:%d", capability, h, window, quantity)
}

// Hash commits to every field that must never change after creation. The
// verify tool recomputes it; a receipt carries it.
func (in *Intent) Hash() string {
	v := struct {
		V          int         `json:"v"`
		Principal  string      `json:"principal"`
		Pass       string      `json:"pass"`
		Capability string      `json:"capability"`
		InputHash  string      `json:"input_hash"`
		Quantity   int         `json:"quantity"`
		Window     string      `json:"window"`
		EffectKey  string      `json:"effect_key"`
		Currency   string      `json:"currency"`
		BudgetMax  int64       `json:"budget_max_minor"`
		Cons       Constraints `json:"constraints"`
		ExpiresAt  int64       `json:"expires_at"`
		CreatedAt  int64       `json:"created_at"`
	}{1, in.PrincipalID, in.PassID, in.Capability, in.InputHash, in.Quantity, in.Window, in.EffectKey,
		in.Currency, in.BudgetMaxMinor, in.Constraints, in.ExpiresAt.Unix(), in.CreatedAt.Unix()}
	b, _ := json.Marshal(v)
	return "sha256:" + sha256Hex(b)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashBytes is the "sha256:<hex>" form used for request and result hashes.
func HashBytes(b []byte) string { return "sha256:" + sha256Hex(b) }
