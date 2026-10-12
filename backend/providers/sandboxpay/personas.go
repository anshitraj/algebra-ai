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

// Persona is one sandbox seller. The token.price ones misbehave the way real
// providers do, so the router and its guards can be shown, and tested over
// HTTP, with no chain and no money: the same cast as the devnet demo
// (cmd/demo-provider). The others sell one other class of work each, so a
// demo account has something to do besides prices. Every answer is simulated.
type Persona struct {
	Name  string
	Title string
	// Class is the work it sells; empty means token.price.
	Class string
	// ListedMinor is what its listing says; AskMinor what its 402 asks.
	ListedMinor, AskMinor int64
	Delay                 time.Duration
	// Down answers every request with 503.
	Down bool
}

// ID is how intents, passes and receipts name the persona.
func (p Persona) ID() string { return "sandbox:" + p.Name }

// DefaultPersonas are the sandbox sellers: five of token prices that behave
// like real ones (honest, slow, down, overcharging, trap-priced), and one each
// of weather, web search, news and LLM answers.
var DefaultPersonas = []Persona{
	{Name: "alpha", Title: "Alpha prices (sandbox)", ListedMinor: 2_000, AskMinor: 2_000},
	{Name: "beta", Title: "Beta prices (sandbox, slow)", ListedMinor: 1_000, AskMinor: 1_000, Delay: 300 * time.Millisecond},
	{Name: "flaky", Title: "Flaky prices (sandbox, down)", ListedMinor: 500, AskMinor: 500, Down: true},
	{Name: "greedy", Title: "Greedy prices (sandbox, overcharges its listing)", ListedMinor: 1_000, AskMinor: 4_000},
	{Name: "trap", Title: "Trap prices (sandbox, honeypot)", ListedMinor: 25_000_000, AskMinor: 25_000_000},
	{Name: "skycast", Title: "SkyCast weather (sandbox)", Class: "weather.forecast", ListedMinor: 1_500, AskMinor: 1_500},
	{Name: "searchlight", Title: "Searchlight web search (sandbox)", Class: "web.search", ListedMinor: 3_000, AskMinor: 3_000},
	{Name: "newsdesk", Title: "Newsdesk headlines (sandbox)", Class: "news.search", ListedMinor: 2_000, AskMinor: 2_000},
	{Name: "inkwell", Title: "Inkwell LLM answers (sandbox)", Class: "llm.chat", ListedMinor: 5_000, AskMinor: 5_000, Delay: 200 * time.Millisecond},
}

// Capability is the class of work the persona sells.
func (p Persona) Capability() string {
	if p.Class == "" {
		return "token.price"
	}
	return p.Class
}

// Seller is a sandbox seller as a demo account sees it.
type Seller struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Capability  string `json:"capability"`
	PriceMinor  int64  `json:"price_minor"`
	Behaviour   string `json:"behaviour"`
	ExampleTask string `json:"example_task"`
}

// Sellers lists the personas, with how each behaves, for the demo.
func (s *PersonaServer) Sellers() []Seller {
	out := []Seller{}
	for _, name := range s.names() {
		p := s.personas[name]
		b, task := "honest", ""
		switch {
		case p.Down:
			b = "down: the health check takes it out"
		case p.AskMinor > p.ListedMinor:
			b = "asks more than its listing: refused"
		case p.ListedMinor >= 1_000_000:
			b = "trap-priced: refused"
		case p.Delay > 0 && p.Capability() == "token.price":
			b = "honest, cheapest, slow"
		}
		switch p.Capability() {
		case "token.price":
			task = "What's the price of SOL?"
		case "weather.forecast":
			task = "What's the weather in Lisbon?"
		case "web.search":
			task = "Search the web for Solana payment channels"
		case "news.search":
			task = "Latest news about stablecoins"
		case "llm.chat":
			task = "Ask an LLM to explain x402 in one sentence"
		}
		out = append(out, Seller{ID: p.ID(), Name: p.Title, Capability: p.Capability(), PriceMinor: p.ListedMinor, Behaviour: b, ExampleTask: task})
	}
	return out
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
			Capability: p.Capability(), Provider: p.ID(), Name: p.Title, ExecutionType: routing.ExecX402, Method: "POST",
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
	result := resultOf(p, body)
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

// resultOf is the persona's simulated answer to one call.
func resultOf(p Persona, input []byte) []byte {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	text := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && strings.TrimSpace(v) != "" {
				v = strings.TrimSpace(v)
				if len(v) > 200 {
					v = v[:200]
				}
				return v
			}
		}
		return ""
	}
	seed := func(s string) uint32 {
		sum := sha256.Sum256([]byte(strings.ToLower(s)))
		return binary.BigEndian.Uint32(sum[:4])
	}
	note := "Simulated by the Algebra sandbox for the demo. Not real data."
	var out any
	switch p.Capability() {
	case "weather.forecast":
		place := text("location", "city", "q", "place", "query")
		if place == "" {
			place = "Lisbon"
		}
		h := seed(place)
		conditions := []string{"sunny", "partly cloudy", "light rain", "clear", "overcast", "windy"}
		t := int(h%22) + 8
		days := []map[string]any{}
		for i := 1; i <= 3; i++ {
			days = append(days, map[string]any{"day": i, "high_c": t + int((h>>uint(i*3))%5), "low_c": t - 4 - int((h>>uint(i*5))%4), "condition": conditions[(h>>uint(i*2))%uint32(len(conditions))]})
		}
		out = map[string]any{"location": place, "temperature_c": t, "condition": conditions[h%uint32(len(conditions))], "forecast": days, "provider": p.ID(), "sandbox": true, "note": note}
	case "web.search", "news.search":
		q := text("query", "q", "topic", "keyword", "search", "text")
		if q == "" {
			q = "solana"
		}
		slug := strings.Trim(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return '-'
		}, strings.ToLower(q)), "-")
		items := []map[string]any{}
		for i, angle := range []string{"An introduction to", "What's new with", "A practical guide to"} {
			item := map[string]any{"title": fmt.Sprintf("%s %s", angle, q), "url": fmt.Sprintf("https://example.com/demo/%s-%d", slug, i+1),
				"snippet": fmt.Sprintf("A simulated result about %s, from the Algebra demo seller.", q)}
			if p.Capability() == "news.search" {
				item["source"], item["published_at"] = "Demo Wire", time.Now().UTC().Add(-time.Duration(i*3+1)*time.Hour).Format(time.RFC3339)
			}
			items = append(items, item)
		}
		key := "results"
		if p.Capability() == "news.search" {
			key = "articles"
		}
		out = map[string]any{"query": q, key: items, "provider": p.ID(), "sandbox": true, "note": note}
	case "llm.chat":
		prompt := text("prompt", "message", "input", "question", "text", "query")
		if prompt == "" {
			prompt = "ping"
		}
		out = map[string]any{"prompt": prompt, "model": "inkwell-demo",
			"reply": "This is a simulated answer from the Inkwell demo model. With a real account, Algebra routes this prompt to a real pay-per-call LLM endpoint and pays it in USDC from your Spend Pass.",
			"usage": map[string]any{"input_tokens": len(strings.Fields(prompt)), "output_tokens": 38}, "provider": p.ID(), "sandbox": true, "note": note}
	default:
		return priceOf(p, input)
	}
	b, _ := json.Marshal(out)
	return b
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
