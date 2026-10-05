// Command verify-intent checks an economic intent's Intent Receipt
// independently: it fetches the receipt and Algebra's public keys, verifies
// the signature locally, and checks the receipt against the intent's
// recorded attempts.
//
//	verify-intent -api https://algebra.example eint_…     (ALGEBRA_AGENT_TOKEN for access)
//	verify-intent -receipt <jws> -jwks https://algebra.example/.well-known/jwks.json
//
// Exit status 0 only if every check passes.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/receipt"
)

type intentDoc struct {
	ID              string `json:"id"`
	Capability      string `json:"capability"`
	IntentHash      string `json:"intent_hash"`
	EffectKey       string `json:"effect_key"`
	State           string `json:"state"`
	Commitment      string `json:"commitment"`
	Fulfillment     string `json:"fulfillment"`
	Attempts        int    `json:"attempts"`
	BlockedAttempts int    `json:"duplicate_commit_attempts_blocked"`
	Receipt         string `json:"receipt"`
	Summary         string `json:"summary"`
	Reservations    []struct {
		ID              string `json:"id"`
		Attempt         int    `json:"attempt"`
		State           string `json:"state"`
		ExecutorAgentID string `json:"executor_agent_id"`
		Evidence        struct {
			Transaction string `json:"transaction"`
			AmountMinor int64  `json:"amount_minor"`
			Test        bool   `json:"test"`
		} `json:"evidence"`
	} `json:"reservations"`
}

var client = &http.Client{Timeout: 20 * time.Second}

func main() {
	api := flag.String("api", strings.TrimRight(os.Getenv("ALGEBRA_API"), "/"), "Algebra base URL (e.g. https://algebra.example)")
	jws := flag.String("receipt", "", "verify this receipt offline instead of fetching one")
	jwksURL := flag.String("jwks", "", "JWKS URL (default <api>/.well-known/jwks.json)")
	flag.Parse()

	failed := false
	check := func(ok bool, label, detail string) {
		mark := "PASS"
		if !ok {
			mark, failed = "FAIL", true
		}
		fmt.Printf("  %s  %s", mark, label)
		if detail != "" {
			fmt.Printf("  (%s)", detail)
		}
		fmt.Println()
	}

	if *jwksURL == "" && *api != "" {
		*jwksURL = *api + "/.well-known/jwks.json"
	}
	if *jwksURL == "" {
		fatal("need -api or -jwks")
	}
	var keys receipt.JWKS
	if err := getJSON(*jwksURL, "", &keys); err != nil {
		fatal("fetching keys: " + err.Error())
	}

	var doc *intentDoc
	if *jws == "" {
		id := flag.Arg(0)
		if id == "" || *api == "" {
			fatal("usage: verify-intent -api <url> <intent_id>   (or -receipt <jws> -jwks <url>)")
		}
		token := os.Getenv("ALGEBRA_AGENT_TOKEN")
		if token == "" {
			fatal("set ALGEBRA_AGENT_TOKEN to an agent token of the intent's principal")
		}
		doc = &intentDoc{}
		if err := getJSON(*api+"/api/v1/economic-intents/"+id, token, doc); err != nil {
			fatal("fetching intent: " + err.Error())
		}
		*jws = doc.Receipt
		fmt.Printf("Intent %s  %s\n  state %s / commitment %s / fulfillment %s\n  %s\n\n", doc.ID, doc.Capability, doc.State, doc.Commitment, doc.Fulfillment, doc.Summary)
		if *jws == "" {
			check(false, "receipt exists", "none yet; one is signed when the intent commits")
			os.Exit(1)
		}
	}

	c, err := receipt.VerifyIntent(*jws, keys)
	check(err == nil, "signature verifies against the published keys", "")
	if err != nil {
		os.Exit(1)
	}
	if *api != "" {
		var v struct {
			Valid    bool   `json:"valid"`
			Recorded bool   `json:"recorded"`
			Reason   string `json:"reason"`
		}
		if err := postJSON(*api+"/api/v1/receipts/verify", map[string]string{"receipt": *jws}, &v); err == nil {
			check(v.Valid && v.Recorded, "receipt is the one on file at Algebra", v.Reason)
		} else {
			check(false, "receipt is the one on file at Algebra", err.Error())
		}
	}
	if doc != nil {
		check(c.Intent.ID == doc.ID && c.Intent.Hash == doc.IntentHash, "receipt binds this exact intent", c.Intent.Hash)
		check(c.Intent.EffectKey == doc.EffectKey, "effect key matches", c.Intent.EffectKey)
		commits := 0
		for _, r := range doc.Reservations {
			if r.State == "COMMITTED" {
				commits++
				check(r.ID == c.Reservation.ID && r.Attempt == c.Reservation.Attempt, "receipt names the committed attempt",
					fmt.Sprintf("attempt %d of %d by %s", r.Attempt, doc.Attempts, r.ExecutorAgentID))
				if c.Settlement != nil {
					check(r.Evidence.Transaction == c.Settlement.Transaction, "settlement transaction matches the record", c.Settlement.Transaction)
				}
			}
		}
		check(commits == 1, "exactly one commitment", fmt.Sprintf("%d", commits))
		// The count can only grow after signing (later duplicates are still refused).
		check(doc.BlockedAttempts >= c.Coordination.DuplicateCommitAttemptsBlocked, "duplicate attempts blocked",
			fmt.Sprintf("%d at signing, %d now", c.Coordination.DuplicateCommitAttemptsBlocked, doc.BlockedAttempts))
	}
	fmt.Println()
	fmt.Printf("  final state   %s / %s / %s\n", c.Final.Lifecycle, c.Final.Commitment, c.Final.Fulfillment)
	fmt.Printf("  authority     spend pass %s, method %s\n", c.Authority.PassID, c.Authority.Method)
	fmt.Printf("  executor      %s (%s), attempt %d\n", c.Reservation.Executor.ID, c.Reservation.Executor.Name, c.Reservation.Attempt)
	fmt.Printf("  provider      %s  result %s\n", c.Provider.ID, c.Execution.Status)
	if c.Settlement != nil {
		fmt.Printf("  settlement    %s %s  %d minor units  tx %s\n", c.Settlement.Rail, c.Settlement.Network, c.Settlement.Amount.MinorUnits, c.Settlement.Transaction)
	}
	fmt.Printf("  reconciled    %v\n", c.Coordination.ReconciliationRequired)
	if c.Test {
		fmt.Println("\n  TEST RECEIPT: sandbox evidence. The receipt is genuine; no real money moved.")
	}
	fmt.Println("\n  An Intent Receipt proves what Algebra authorized and observed. It does not prove the provider's data is correct.")
	if failed {
		os.Exit(1)
	}
}

func getJSON(url, token string, v any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(req, v)
}

func postJSON(url string, body, v any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req, v)
}

func do(req *http.Request, v any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return errors.New(res.Status + ": " + strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, v)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "verify-intent:", msg)
	os.Exit(2)
}
