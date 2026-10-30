package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/project-algebra/algebra/internal/domain/routing"
)

// ExplainRefusal says, in words a caller can act on, why a request was refused
// and what to do next. REST and MCP both use it, so the same refusal reads the
// same on either: the 409 body's "error" is what an MCP tool returns. It returns
// err unchanged when it has nothing better to say. rejected are the providers
// already ruled out before the attempt, listed with the ones the router rejected.
func ExplainRefusal(err error, rejected []routing.Rejection) error {
	var rej *ReservationRejected
	var denied *AuthorityDenied
	var nr *NoRoute
	switch {
	case errors.As(err, &rej):
		switch rej.Reason {
		case RejectApproval:
			return errors.New("the person has to approve this first: it is waiting in their Algebra console. Ask them to approve it, then send the same request again")
		case RejectCommitted:
			return errors.New("already_committed: this exact request was already paid for, and its answer is no longer kept (or you asked for it not to be). Algebra never charges twice. " +
				"Use the result you were given, look the request up (algebra_status over MCP, GET /api/v1/economic-intents/{id} over REST), or send a new `window` (or a new Idempotency-Key) to buy it again")
		case RejectUnknown, RejectExecuting, RejectHeld:
			return fmt.Errorf("%s: an earlier attempt at this request may still be settling. Do not retry; look the request up again later (algebra_status over MCP, GET /api/v1/economic-intents/{id} over REST)", rej.Reason)
		case RejectClosed:
			return errors.New("this request has expired or was cancelled")
		}
		return err
	case errors.As(err, &denied):
		return fmt.Errorf("the person's limits don't allow this (%s). Algebra will not pay it", strings.Join(denied.ReasonCodes, ", "))
	case errors.As(err, &nr):
		all := append(append([]routing.Rejection{}, rejected...), nr.Rejected...)
		parts := make([]string, 0, len(all))
		for _, r := range all {
			p := r.Code
			if r.Provider != "" {
				p = r.Provider + ": " + r.Code
			}
			if r.Detail != "" {
				p += " (" + r.Detail + ")"
			}
			parts = append(parts, p)
		}
		if len(parts) == 0 {
			parts = append(parts, "no provider is configured for this capability; pass `providers` or `candidates`")
		}
		return fmt.Errorf("no provider could do this within the limits: %s", strings.Join(parts, "; "))
	}
	return err
}
