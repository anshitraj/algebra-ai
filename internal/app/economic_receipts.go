package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// IntentReceiptService signs an Intent Receipt when an intent reaches its
// final economic state, and verifies receipts for anyone.
type IntentReceiptService struct {
	signer *receipt.Signer
	issuer string
	agents AgentStore
	store  EconStore
	now    func() time.Time

	// executions, when set, lets a receipt carry how the provider was chosen
	// and how the result was judged.
	executions ExecutionStore
}

// SetExecutions attaches the executor's records.
func (s *IntentReceiptService) SetExecutions(e ExecutionStore) { s.executions = e }

func NewIntentReceiptService(signer *receipt.Signer, issuer string, agents AgentStore, store EconStore) *IntentReceiptService {
	return &IntentReceiptService{signer: signer, issuer: issuer, agents: agents, store: store, now: time.Now}
}

// Sign builds the receipt from what was recorded — the committed attempt,
// its evidence, and the event history — and signs it. It claims only what
// was observed.
func (s *IntentReceiptService) Sign(ctx context.Context, v *IntentView) (jws, id string, err error) {
	var r *econ.Reservation
	for i := range v.Reservations {
		if v.Reservations[i].State == econ.ReservationCommitted {
			r = &v.Reservations[i]
		}
	}
	if r == nil {
		return "", "", errors.New("app: no committed attempt to sign for")
	}
	c := receipt.IntentClaims{
		Issuer: s.issuer, ID: newID("ircpt"), IssuedAt: s.now().Unix(), Subject: s.signer.Pseudonym(v.PrincipalID),
		Intent: receipt.IntentRef{
			ID: v.ID, Hash: v.IntentHash, Capability: v.Capability, EffectKey: v.EffectKey, Quantity: v.Quantity, Window: v.Window,
			BudgetMax: receipt.Money{MinorUnits: v.BudgetMaxMinor, Currency: v.Currency},
		},
		Authority:   receipt.AuthorityRef{PassID: r.ExecutorPassID, PolicyVersion: r.PolicyVersion, Method: receipt.MethodPolicy},
		Reservation: receipt.ReservationRef{ID: r.ID, Attempt: r.Attempt, Executor: receipt.Agent{ID: r.ExecutorAgentID}},
		Provider:    receipt.ProviderRef{ID: r.ProviderID, Semantics: string(r.Semantics)},
		Execution: receipt.ExecutionRef{
			Protocol: r.Evidence.Protocol, Scheme: r.Evidence.Scheme, RequestHash: r.Evidence.RequestHash,
			ResultHash: r.Evidence.ResultHash, ProviderOperationID: r.Evidence.ProviderOperationID, Status: "result_unknown",
		},
		Coordination: receipt.CoordinationRef{Attempts: v.Attempts, DuplicateCommitAttemptsBlocked: v.BlockedAttempts},
		Final:        receipt.FinalState{Lifecycle: string(v.State), Commitment: string(v.Commitment), Fulfillment: string(v.Fulfillment)},
		Test:         r.Evidence.Test,
	}
	if v.ApprovedAt != nil {
		c.Authority.Method = receipt.MethodHuman
	}
	switch v.Fulfillment {
	case econ.FulfillmentFulfilled:
		c.Execution.Status = "fulfilled"
	case econ.FulfillmentNotFulfilled:
		c.Execution.Status = "not_fulfilled"
	}
	if r.QuoteMinor > 0 {
		c.Provider.Quote = &receipt.Money{MinorUnits: r.QuoteMinor, Currency: v.Currency}
	}
	if ag, err := s.agents.Get(ctx, r.ExecutorAgentID); err == nil {
		c.Reservation.Executor = receipt.Agent{ID: ag.ID, Name: ag.Name, Client: ag.ClientID}
	}
	if s.executions != nil {
		if rec, err := s.executions.ForReservation(ctx, r.ID); err == nil {
			x := rec.Result
			if x.Mode != "" {
				c.Routing = &receipt.RoutingRef{
					Mode: string(x.Mode), PlanHash: x.PlanHash, QuoteHash: x.QuoteHash, CandidateID: x.CandidateID,
					Rank: x.PlanRank, Fallback: x.PlanRank > 1,
				}
			}
			if q := rec.Quality; q != nil {
				c.Execution.Quality = &receipt.QualityRef{Evaluator: q.Evaluator, SchemaValid: q.SchemaValid, Score: q.FinalQuality}
			}
		}
	}
	c.Settlement = &receipt.SettlementRef{
		Rail: r.Rail, Network: r.Evidence.Network, Asset: r.Evidence.Asset,
		Amount:      receipt.Money{MinorUnits: r.Evidence.AmountMinor, Currency: v.Currency},
		Transaction: r.Evidence.Transaction, PaymentID: r.Evidence.PaymentID, Payer: r.Evidence.Payer, PayTo: r.Evidence.PayTo,
	}
	for _, e := range v.Events {
		if e.Event == "reconciliation.started" {
			c.Coordination.ReconciliationRequired = true
		}
	}
	jws, err = s.signer.SignIntent(c)
	if err != nil {
		return "", "", fmt.Errorf("app: signing intent receipt: %w", err)
	}
	return jws, c.ID, nil
}

// IntentVerification is the answer to "is this Intent Receipt real?".
type IntentVerification struct {
	Valid    bool                  `json:"valid"`
	Recorded bool                  `json:"recorded"`
	Claims   *receipt.IntentClaims `json:"claims,omitempty"`
	Reason   string                `json:"reason,omitempty"`
}

// Verify checks the signature and that Algebra has this exact receipt on
// file for the intent it names.
func (s *IntentReceiptService) Verify(ctx context.Context, jws string) *IntentVerification {
	c, err := receipt.VerifyIntent(jws, s.signer.JWKS())
	if err != nil {
		return &IntentVerification{Reason: "The signature doesn't check out — this wasn't issued by this Algebra, or it was changed after signing."}
	}
	v := &IntentVerification{Valid: true, Claims: c}
	stored, err := s.store.GetReceipt(ctx, c.Intent.ID)
	switch {
	case err == nil && stored == jws:
		v.Recorded = true
	case err != nil && !errors.Is(err, shared.ErrNotFound):
		v.Reason = "Signature valid; couldn't check the receipt log right now."
	}
	return v
}

// JWKS is the key set that verifies every Algebra receipt.
func (s *IntentReceiptService) JWKS() receipt.JWKS { return s.signer.JWKS() }
