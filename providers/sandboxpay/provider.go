package sandboxpay

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/providers/x402"
)

// Provider is a SANDBOX paid API speaking x402: it answers 402 with payment
// requirements, captures a sandbox payment, and returns simulated data that
// says it's simulated. It exists to make failures reproducible:
//
//	X-Sandbox-Fault: drop_response        capture the payment, produce the
//	                                      result, then answer 504 (the
//	                                      response "got lost")
//	X-Sandbox-Fault: fail_before_capture  answer 503 without charging
//
// It honours Idempotency-Key: a retry with the same key returns the stored
// result without charging again, and GET …/operations/{key} reports what
// happened to a request — the recovery path reconciliation uses.
type Provider struct {
	rail    *Rail
	id      string
	payTo   string
	price   int64
	baseURL string

	mu  sync.Mutex
	ops map[string]*operation
}

type operation struct {
	PaymentID   string          `json:"payment_id"`
	Transaction string          `json:"transaction"`
	ResultHash  string          `json:"result_hash"`
	Result      json.RawMessage `json:"-"`
	At          time.Time       `json:"at"`
}

// ProviderID is how intents and reservations name this provider.
const ProviderID = "sandbox:token-risk"

// NewProvider: baseURL is where the provider is mounted, for the resource
// field of its 402 challenge.
func NewProvider(rail *Rail, baseURL string, priceMinor int64) *Provider {
	if priceMinor <= 0 {
		priceMinor = 3_000 // 0.003 sandbox USDC
	}
	return &Provider{rail: rail, id: ProviderID, payTo: "sandbox-provider-token-risk", price: priceMinor,
		baseURL: strings.TrimRight(baseURL, "/"), ops: map[string]*operation{}}
}

var _ app.ProviderRecovery = (*Provider)(nil)

// Capability describes the provider to the coordination layer.
func (p *Provider) Capability() app.ProviderCapability {
	return app.ProviderCapability{
		ID: p.id, Capabilities: []string{"solana.token-risk"}, Rail: "sandbox", Semantics: econ.SemanticsPrepaidExact,
		IdempotencyHeader: "Idempotency-Key", StatusEndpoint: p.baseURL + "/operations/{idempotency_key}", ReplayResult: true,
	}
}

func (p *Provider) requirements() x402.Requirements {
	return x402.Requirements{
		Scheme: "exact", Network: Network, MaxAmountRequired: itoa(p.price), Resource: p.baseURL,
		Description: "SANDBOX token risk score — simulated data, no real money moves",
		MimeType:    "application/json", PayTo: p.payTo, MaxTimeoutSeconds: 60, Asset: "USDC",
	}
}

// Serve handles POST {base}: the paid call.
func (p *Provider) Serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable body"})
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Idempotency-Key too long"})
		return
	}
	if key != "" {
		p.mu.Lock()
		op := p.ops[key]
		p.mu.Unlock()
		if op != nil { // replay: same result, no second charge
			w.Header().Set(x402.HeaderPaymentResponse, x402.SettleResponse{Success: true, Transaction: op.Transaction, Network: Network}.Encode())
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Sandbox", "true")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(op.Result)
			return
		}
	}
	payHeader := r.Header.Get(x402.HeaderPayment)
	if payHeader == "" {
		payHeader = r.Header.Get(x402.HeaderPaymentV2)
	}
	if payHeader == "" {
		writeJSON(w, http.StatusPaymentRequired, x402.Challenge{Version: 1, Error: "X-PAYMENT header is required", Accepts: []x402.Requirements{p.requirements()}})
		return
	}
	fault := strings.ToLower(r.Header.Get("X-Sandbox-Fault"))
	if fault == "fail_before_capture" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "provider unavailable (sandbox fault injection, nothing charged)"})
		return
	}
	settle, err := p.rail.Capture(payHeader, p.payTo, p.price)
	if err != nil {
		writeJSON(w, http.StatusPaymentRequired, x402.Challenge{Version: 1, Error: err.Error(), Accepts: []x402.Requirements{p.requirements()}})
		return
	}
	result := simulate(body)
	sum := sha256.Sum256(result)
	op := &operation{PaymentID: paymentIDOf(payHeader), Transaction: settle.Transaction, ResultHash: "sha256:" + hex.EncodeToString(sum[:]), Result: result, At: time.Now().UTC()}
	if key != "" {
		p.mu.Lock()
		p.ops[key] = op
		p.mu.Unlock()
	}
	if fault == "drop_response" {
		// The charge and the work happened; the caller never hears.
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "upstream timed out"})
		return
	}
	w.Header().Set(x402.HeaderPaymentResponse, settle.Encode())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Sandbox", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

// ServeOperation handles GET {base}/operations/{key}.
func (p *Provider) ServeOperation(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	op := p.ops[r.PathValue("key")]
	p.mu.Unlock()
	if op == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"status": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "fulfilled", "operation": op, "sandbox": true})
}

// Recover is the reconciliation adapter: ask the provider what happened to
// the attempt's idempotency key. Not found is UNKNOWN, not "not fulfilled":
// the provider's memory doesn't prove the request never arrived.
func (p *Provider) Recover(_ context.Context, rv econ.Reservation) (app.Recovery, error) {
	p.mu.Lock()
	op := p.ops[rv.IdempotencyKey]
	p.mu.Unlock()
	if op == nil {
		return app.Recovery{Status: app.RecoveryUnknown, Detail: "the provider has no record of this idempotency key"}, nil
	}
	return app.Recovery{Status: app.RecoveryFulfilled, ResultHash: op.ResultHash, OperationID: op.PaymentID, Detail: "the provider recorded the result (sandbox)"}, nil
}

// simulate derives a deterministic, obviously-simulated answer.
func simulate(input []byte) []byte {
	var in struct {
		Mint string `json:"mint"`
	}
	_ = json.Unmarshal(input, &in)
	sum := sha256.Sum256([]byte(strings.TrimSpace(in.Mint)))
	score := binary.BigEndian.Uint16(sum[:2]) % 100
	out, _ := json.Marshal(map[string]any{
		"mint": in.Mint, "risk_score": score, "sandbox": true,
		"note": "Simulated by the Algebra sandbox provider. Not market data.",
	})
	return out
}

func paymentIDOf(header string) string {
	p, err := x402.DecodePayment(header)
	if err != nil {
		return ""
	}
	var t struct {
		PaymentID string `json:"payment_id"`
	}
	_ = json.Unmarshal(p.Payload, &t)
	return t.PaymentID
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Sandbox", "true")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
