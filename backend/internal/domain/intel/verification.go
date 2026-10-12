package intel

import (
	"net"
	"net/url"
	"strings"
)

// Three things can be known about an endpoint, and none implies the next:
//
//   - Payment tested: Algebra paid it and it delivered (TestedAt). It proves
//     that one transaction worked. It says nothing about who the seller is:
//     anyone can stand up an endpoint, charge a tenth of a cent and answer.
//   - Identity verified: every check in VerificationChecks passed, and a
//     person reviewed evidence of who runs the endpoint and recorded it
//     (Trust == TrustVerified). This is the only thing the check mark means.
//   - Execution approved: one request passed the Spend Pass and the router's
//     guards at the moment it was made. It is decided per request and is
//     never stored on an endpoint.
//
// Nothing automatic verifies an endpoint: not a listing, not a quote, not a
// paid delivery, not a search result.

// Check is one requirement of the verification standard.
type Check struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// MinEvidence is the least a person must write when verifying an endpoint:
// who runs it and how they know.
const MinEvidence = 20

// sandboxNetwork is Algebra's simulated rail: its sellers are this server's
// own, so there is no outside host to check.
const sandboxNetwork = "sandbox"

// VerificationChecks are the automatic requirements of the standard, in the
// order a person would read them. All must pass before a person may verify.
func (e Endpoint) VerificationChecks() []Check {
	out := []Check{{ID: "listed", Label: "Listed by its catalog now", Passed: e.Status == StatusActive}}
	if e.Status != StatusActive {
		out[0].Detail = "its status is " + string(e.Status)
	}

	host := Check{ID: "host", Label: "Served over HTTPS from a named host"}
	switch u, err := url.Parse(e.URL); {
	case e.Network == sandboxNetwork:
		host.Passed, host.Detail = true, "a simulated seller on Algebra's own sandbox"
	case err != nil || u.Hostname() == "":
		host.Detail = "its address is not a web address"
	case u.Scheme != "https":
		host.Detail = "it is not served over HTTPS"
	case net.ParseIP(u.Hostname()) != nil || u.Hostname() == "localhost" || !strings.Contains(u.Hostname(), "."):
		host.Detail = "it is served from an address, not a domain someone answers for"
	default:
		host.Passed, host.Detail = true, u.Hostname()
	}
	out = append(out, host)

	quoted := Check{ID: "quoted", Label: "An unpaid request returned payment terms", Passed: e.QuotedAt != nil}
	if !quoted.Passed {
		quoted.Detail = "Algebra has not been quoted by it"
	}
	out = append(out, quoted)

	payee := Check{ID: "payee", Label: "Its payee is pinned, and is the one it names"}
	switch {
	case e.PinnedPayTo == "":
		payee.Detail = "no payee is pinned yet"
	case e.PayTo != "" && e.PayTo != e.PinnedPayTo:
		payee.Detail = "it names a payee other than the pinned one"
	default:
		payee.Passed = true
	}
	out = append(out, payee)

	tested := Check{ID: "tested", Label: "Payment tested: Algebra paid it and it delivered", Passed: e.TestedAt != nil}
	if !tested.Passed {
		tested.Detail = "no paid call has delivered"
	}
	out = append(out, tested)

	blocked := Check{ID: "not_blocked", Label: "Not blocked", Passed: e.Trust != TrustBlocked}
	if !blocked.Passed {
		blocked.Detail = "blocked (" + e.TrustReason + "): unblock it first, and it must be tested again"
	}
	return append(out, blocked)
}

// Unmet are the checks that stand between e and verification.
func (e Endpoint) Unmet() []Check {
	var out []Check
	for _, c := range e.VerificationChecks() {
		if !c.Passed {
			out = append(out, c)
		}
	}
	return out
}
