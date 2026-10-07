// Command kite-wallet is a runnable demonstration of a third-party
// application — "Kite," a hypothetical wallet that stores and provides
// cards — integrating Algebra's standalone policy-evaluation surface to
// gate its own users' card charges to any store. It makes real HTTP calls
// against a running cmd/api; nothing here is a canned transcript.
//
// It also imports policy.Rules directly (github.com/project-algebra/algebra/policy,
// a public package — see docs/legacy/INTEGRATING.md) to build its own budget
// policy, proving that surface works too, not just the REST endpoint.
//
// Run:
//
//	go run ./cmd/api            # in one terminal
//	go run ./examples/kite-wallet   # in another, defaults to http://localhost:8080
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/project-algebra/algebra/policy"
)

type evalRequest struct {
	Who struct {
		UserRef string `json:"user_ref"`
	} `json:"who"`
	What struct {
		Category string `json:"category,omitempty"`
	} `json:"what"`
	Where struct {
		Merchant      string `json:"merchant"`
		International bool   `json:"international,omitempty"`
	} `json:"where"`
	HowMuch struct {
		AmountMinorUnits     int64  `json:"amount_minor_units"`
		Currency             string `json:"currency"`
		SpendTodayMinorUnits int64  `json:"spend_today_minor_units,omitempty"`
	} `json:"how_much"`
	WithWhat struct {
		PaymentRef string `json:"payment_ref,omitempty"`
	} `json:"with_what"`
	// Conditions is policy.Rules straight from the public package — Kite's
	// own budget policy, built with Algebra's real types rather than a
	// hand-rolled JSON shape guessed from docs.
	Conditions policy.Rules `json:"conditions"`
}

type decisionResponse struct {
	Decision      string   `json:"decision"`
	ReasonCodes   []string `json:"reason_codes"`
	PolicyVersion string   `json:"policy_version"`
}

func main() {
	baseURL := flag.String("api", "http://localhost:8080", "Algebra API base URL")
	flag.Parse()

	fmt.Println("Kite — a wallet that stores and provides cards — registering with Algebra...")
	token, integratorID := registerIntegrator(*baseURL)
	fmt.Printf("Registered as integrator %s\n\n", integratorID)

	// Kite's own budget policy: no single charge over $200, anything at or
	// above $50 needs the user's own in-app approval, gambling is blocked
	// outright. This is Kite's rules, not Algebra's — Algebra evaluates
	// whatever an integrator sends, it never assumes a default.
	kiteRules := policy.Rules{
		MaxPerTransactionMinorUnits: 20000, // $200.00
		ApprovalThresholdMinorUnits: 5000,  // $50.00
		BlockedCategories:           []string{"gambling"},
	}

	scenarios := []struct {
		label    string
		merchant string
		category string
		amount   int64
	}{
		{"Coffee run, well under budget", "Blue Bottle Coffee", "dining", 850},
		{"New shoes, at Kite's own approval threshold", "Target", "retail", 5000},
		{"A casino chip top-up — blocked category", "Lucky Star Casino", "gambling", 10000},
	}

	for i, sc := range scenarios {
		fmt.Printf("%d. %s\n   %s — $%.2f\n", i+1, sc.label, sc.merchant, float64(sc.amount)/100)
		dec := evaluateTransaction(*baseURL, token, kiteRules, sc.merchant, sc.category, sc.amount)
		fmt.Printf("   -> %s  (%v)\n\n", dec.Decision, dec.ReasonCodes)
	}
}

func registerIntegrator(baseURL string) (token, integratorID string) {
	body, _ := json.Marshal(map[string]string{"name": "Kite"})
	resp := mustPost(baseURL+"/api/v1/integrators", "", body)
	var out struct {
		IntegratorID string `json:"integrator_id"`
		Token        string `json:"token"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		fail(fmt.Errorf("decoding integrator registration response: %w", err))
	}
	return out.Token, out.IntegratorID
}

func evaluateTransaction(baseURL, token string, rules policy.Rules, merchant, category string, amountMinorUnits int64) decisionResponse {
	var req evalRequest
	req.Who.UserRef = "kite-user-8891"
	req.What.Category = category
	req.Where.Merchant = merchant
	req.HowMuch.AmountMinorUnits = amountMinorUnits
	req.HowMuch.Currency = "USD"
	req.WithWhat.PaymentRef = "kite-card-visa-4242"
	req.Conditions = rules

	body, _ := json.Marshal(req)
	resp := mustPost(baseURL+"/api/v1/policy/evaluate-transaction", token, body)
	var dec decisionResponse
	if err := json.Unmarshal(resp, &dec); err != nil {
		fail(fmt.Errorf("decoding decision response: %w", err))
	}
	return dec
}

func mustPost(url, bearerToken string, body []byte) []byte {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(fmt.Errorf("calling %s (is `go run ./cmd/api` running?): %w", url, err))
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fail(err)
	}
	if resp.StatusCode >= 300 {
		fail(fmt.Errorf("%s returned HTTP %d: %s", url, resp.StatusCode, respBody))
	}
	return respBody
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kite-wallet:", err)
	os.Exit(1)
}
