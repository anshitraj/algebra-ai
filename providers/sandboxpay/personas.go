package sandboxpay

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/routing"
	"github.com/project-algebra/algebra/providers/x402"
)

// Persona is one sandbox provider of token prices. Together they misbehave the
// way real providers do, so the router and its guards can be shown, and
// tested over HTTP, with no chain and no money: the same cast as the devnet
// demo (cmd/demo-provider).
type Persona struct {
	Name  string
	Title string
	// ListedMinor is what its listing says; AskMinor what its 402 asks.
	ListedMinor, AskMinor int64
	Delay                 time.Duration
	// Down answers every request with 503.
	Down bool
}

// ID is how intents, passes and receipts name the persona.
func (p Persona) ID() string { return "sandbox:" + p.Name }

// DefaultPersonas are the sandbox token.price providers.
var DefaultPersonas = []Persona{
	{Name: "alpha", Title: "Alpha prices (sandbox)", ListedMinor: 2_000, AskMinor: 2_000},
	{Name: "beta", Title: "Beta prices (sandbox, slow)", ListedMinor: 1_000, AskMinor: 1_000, Delay: 300 * time.Millisecond},
	{Name: "flaky", Title: "Flaky prices (sandbox, down)", ListedMinor: 500, AskMinor: 500, Down: true},
	{Name: "greedy", Title: "Greedy prices (sandbox, overcharges its listing)", ListedMinor: 1_000, AskMinor: 4_000},
	{Name: "trap", Title: "Trap prices (sandbox, honeypot)", ListedMinor: 25_000_000, AskMinor: 25_000_000},
}

// PersonaServer serves the personas at {base}/{name}, paid through the
// sandbox rail.
type PersonaServer struct {
	rail     *Rail
	base     string
	personas map[string]Persona

	mu  sync.Mutex
	ops map[string]*operation // by persona + idempotency key
}

// NewPersonaServer serves personas (DefaultPersonas when nil) under base.
func NewPersonaServer(rail *Rail, base string, personas []Persona) *PersonaServer {
	if personas == nil {
		personas = DefaultPersonas
	}
	s := &PersonaServer{rail: rail, base: strings.TrimRight(base, "/"), personas: map[string]Persona{}, ops: map[string]*operation{}}
	for _, p := range personas {
		s.personas[p.Name] = p
	}
	return s
}

// Candidates are the personas as the operator's providers of token.price,
// each with its listed price, reached at endpointBase/{name}.
func (s *PersonaServer) Candidates(endpointBase string) ([]routing.Candidate, error) {
	var out []routing.Candidate
	for _, name := range s.names() {
		p := s.personas[name]
		c, err := routing.Candidate{
			Capability: "token.price", Provider: p.ID(), Name: p.Title, ExecutionType: routing.ExecX402, Method: "POST",
			Endpoint: strings.TrimRight(endpointBase, "/") + "/" + p.Name, Network: Network, PriceMinor: p.ListedMinor, Asset: "USDC",
			Sources: []routing.DiscoverySource{routing.SourceConfigured},
		}.Normalize()
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *PersonaServer) names() []string {
	out := make([]string, 0, len(s.personas))
	for _, p := range DefaultPersonas {
		if _, ok := s.personas[p.Name]; ok {
			out = append(out, p.Name)
		}
	}
	for n := range s.personas {
		found := false
		for _, o := range out {
			found = found || o == n
		}
		if !found {
			out = append(out, n)
		}
	}
	return out
}

func (s *PersonaServer) payTo(p Persona) string { return "sandbox-provider-" + p.Name }

func (s *PersonaServer) requirements(p Persona) x402.Requirements {
	return x402.Requirements{
		Scheme: "exact", Network: Network, MaxAmountRequired: itoa(p.AskMinor), Resource: s.base + "/" + p.Name,
		Description: p.Title + " — simulated data, no real money moves",
		MimeType:    "application/json", PayTo: s.payTo(p), MaxTimeoutSeconds: 60, Asset: "USDC",
	}
}

// Serve handles POST {base}/{name}.
func (s *PersonaServer) Serve(w http.ResponseWriter, r *http.Request) {
	p, ok := s.personas[r.PathValue("name")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such sandbox provider"})
		return
	}
	if p.Down {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sandbox: this provider is down on purpose"})
		return
	}
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
	opKey := p.Name + "|" + key
	if key != "" {
		s.mu.Lock()
		op := s.ops[opKey]
		s.mu.Unlock()
		if op != nil {
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
		writeJSON(w, http.StatusPaymentRequired, x402.Challenge{Version: 1, Error: "X-PAYMENT header is required", Accepts: []x402.Requirements{s.requirements(p)}})
		return
	}
	settle, err := s.rail.Capture(payHeader, s.payTo(p), p.AskMinor)
	if err != nil {
		writeJSON(w, http.StatusPaymentRequired, x402.Challenge{Version: 1, Error: err.Error(), Accepts: []x402.Requirements{s.requirements(p)}})
		return
	}
	time.Sleep(p.Delay)
	result := priceOf(p, body)
	sum := sha256.Sum256(result)
	op := &operation{PaymentID: paymentIDOf(payHeader), Transaction: settle.Transaction, ResultHash: "sha256:" + hex.EncodeToString(sum[:]), Result: result, At: time.Now().UTC()}
	if key != "" {
		s.mu.Lock()
		s.ops[opKey] = op
		s.mu.Unlock()
	}
	w.Header().Set(x402.HeaderPaymentResponse, settle.Encode())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Sandbox", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

// Recovery is the reconciliation adapter for one persona.
func (s *PersonaServer) Recovery(name string) app.ProviderRecovery { return personaRecovery{s, name} }

type personaRecovery struct {
	s    *PersonaServer
	name string
}

func (pr personaRecovery) Recover(_ context.Context, rv econ.Reservation) (app.Recovery, error) {
	pr.s.mu.Lock()
	op := pr.s.ops[pr.name+"|"+rv.IdempotencyKey]
	pr.s.mu.Unlock()
	if op == nil {
		return app.Recovery{Status: app.RecoveryUnknown, Detail: "the provider has no record of this idempotency key"}, nil
	}
	return app.Recovery{Status: app.RecoveryFulfilled, ResultHash: op.ResultHash, OperationID: op.PaymentID, Detail: "the provider recorded the result (sandbox)"}, nil
}

// priceOf derives a deterministic, obviously simulated price.
func priceOf(p Persona, input []byte) []byte {
	var in struct {
		Mint string `json:"mint"`
	}
	_ = json.Unmarshal(input, &in)
	sum := sha256.Sum256([]byte(strings.TrimSpace(in.Mint)))
	cents := binary.BigEndian.Uint32(sum[:4]) % 100_000
	out, _ := json.Marshal(map[string]any{
		"mint": in.Mint, "price_usd": fmt.Sprintf("%d.%02d", cents/100, cents%100), "provider": p.ID(), "sandbox": true,
		"note": "Simulated by the Algebra sandbox. Not market data.",
	})
	return out
}
