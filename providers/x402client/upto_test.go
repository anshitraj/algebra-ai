package x402client

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
)

func uptoOption(ceiling string) string {
	return `{"scheme":"upto","network":"solana","maxAmountRequired":"` + ceiling + `","payTo":"FvZ2jvMmVveZYNvsQEQbpkPnHtUuBWburtHAYAWW4SLg","asset":"` + solUSDC +
		`","extra":{"feePayer":"2thyaZ4skNqYyMdR4HSkkQ1VPUCnzi884AahjmXy6fXu","receiverAuthorizer":"2thyaZ4skNqYyMdR4HSkkQ1VPUCnzi884AahjmXy6fXu","withdrawDelay":900}}`
}

func TestQuoteTakesAnUptoOptionAsAMeteredCeiling(t *testing.T) {
	p := newProvider(t)
	p.challenge = func(w http.ResponseWriter, _ *http.Request) { writeChallenge(w, uptoOption("50000")) }
	r := newRunner(2*time.Second, 0)
	q, err := r.Quote(context.Background(), candidateFor(t, p, "POST"), input)
	if err != nil {
		t.Fatal(err)
	}
	if q.Cost.ProviderMinor != 50_000 || q.Semantics != econ.SemanticsMeteredCapture {
		t.Fatalf("an upto option is priced at its ceiling, settled as metered: %+v", q)
	}
}

func TestQuotePrefersExactOverUptoAtTheSamePrice(t *testing.T) {
	p := newProvider(t)
	p.challenge = func(w http.ResponseWriter, _ *http.Request) {
		writeChallenge(w, uptoOption("4000"), solOption(4_000, "PayeeAddr2", solUSDC))
	}
	r := newRunner(2*time.Second, 0)
	q, err := r.Quote(context.Background(), candidateFor(t, p, "POST"), input)
	if err != nil {
		t.Fatal(err)
	}
	if q.Semantics != econ.SemanticsPrepaidExact || q.PayTo != "PayeeAddr2" {
		t.Fatalf("at equal price the exact option wins: %+v", q)
	}
}
