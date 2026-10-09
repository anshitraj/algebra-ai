package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/internal/domain/spendpass"
	"github.com/project-algebra/algebra/policy"
)

// SpendPassStore persists passes (migrations/0013_spend_passes.sql).
type SpendPassStore interface {
	Create(ctx context.Context, p *spendpass.Pass) error
	Get(ctx context.Context, id string) (*spendpass.Pass, error)
	GetByAgent(ctx context.Context, agentID string) (*spendpass.Pass, error) // shared.ErrNotFound if the agent has none
	ListByUser(ctx context.Context, userID string) ([]spendpass.Pass, error)
	Revoke(ctx context.Context, id string, at time.Time) error
}

// PassSpendLedger answers what a pass's agent has already spent, from real
// orders — never a number the agent reports.
type PassSpendLedger interface {
	SpentByAgentSince(ctx context.Context, agentID, currency string, since time.Time) (int64, error)
}

// PassEconomicSpend answers what a pass has committed through economic
// intents since a time, plus what those intents still hold while their
// outcome is unknown. A pass has one budget, so the pass's own view and the
// shopping flow's check count this next to its orders; the coordinator reads
// the same number under the pass's row lock when it grants a reservation.
type PassEconomicSpend interface {
	PassSpend(ctx context.Context, passID string, since time.Time) (int64, error)
}

// PassGate is how policy and execution consult Spend Passes. A nil decision
// means the agent has no pass (the console's own agent, say) and only the
// person's guardrails apply.
type PassGate interface {
	CheckPurchase(ctx context.Context, agentID, merchant, category string, amount money.Amount) (*policy.PolicyDecision, *spendpass.Pass, error)
}

// SpendPassPermissions are what a pass's agent may do: shop and pay within
// policy, read its orders. Never anything human-only — approving, changing
// guardrails, adding addresses or payment methods all stay with the person —
// and not even writing their shopping profile.
var SpendPassPermissions = []agentpkg.Permission{
	agentpkg.PermShoppingRead, agentpkg.PermShoppingCreateIntent, agentpkg.PermShoppingExecute,
	agentpkg.PermPaymentsRequest, agentpkg.PermOrdersRead, agentpkg.PermProfilesRead, agentpkg.PermPolicyRead,
}

// SpendPassService issues, lists and revokes Spend Passes, and checks
// purchases against them.
type SpendPassService struct {
	store    SpendPassStore
	agents   *AgentService
	ledger   PassSpendLedger
	economic PassEconomicSpend
	now      func() time.Time
}

func NewSpendPassService(store SpendPassStore, agents *AgentService, ledger PassSpendLedger) *SpendPassService {
	return &SpendPassService{store: store, agents: agents, ledger: ledger, now: time.Now}
}

// CountEconomicSpend makes a pass's spend through economic intents count
// against its budget in the pass's view and in the shopping check. Without
// it only orders count. Called once during wiring.
func (s *SpendPassService) CountEconomicSpend(e PassEconomicSpend) { s.economic = e }

// spent is everything the pass has used in its current window: orders, and
// economic intents committed or still held.
func (s *SpendPassService) spent(ctx context.Context, p spendpass.Pass) (int64, error) {
	start := p.WindowStart(s.now())
	total, err := s.ledger.SpentByAgentSince(ctx, p.AgentID, p.Currency, start)
	if err != nil {
		return 0, fmt.Errorf("app: reading the pass's spend: %w", err)
	}
	if s.economic != nil {
		viaIntents, err := s.economic.PassSpend(ctx, p.ID, start)
		if err != nil {
			return 0, fmt.Errorf("app: reading the pass's economic spend: %w", err)
		}
		total += viaIntents
	}
	return total, nil
}

// PassView is a pass with where its budget stands right now.
type PassView struct {
	spendpass.Pass
	Active          bool      `json:"active"`
	SpentMinorUnits int64     `json:"spent_minor_units"`
	RemainingMinor  int64     `json:"remaining_minor_units"`
	WindowStartsAt  time.Time `json:"window_starts_at"`
}

// IssuedPass is a new pass plus its agent token — shown exactly once.
type IssuedPass struct {
	PassView
	Token string `json:"token"`
}

// Create issues a pass for the signed-in person: a fresh agent identity
// scoped to SpendPassPermissions, bound one-to-one to the pass.
func (s *SpendPassService) Create(ctx context.Context, userID string, in spendpass.Pass) (*IssuedPass, error) {
	now := s.now()
	p, err := in.Normalize(now, account.KnownCategories)
	if err != nil {
		return nil, err
	}
	token, ag, err := s.agents.CreateAgent(ctx, userID, "spend-pass:"+string(p.AgentKind), p.Label, SpendPassPermissions)
	if err != nil {
		return nil, fmt.Errorf("app: creating the pass's agent: %w", err)
	}
	p.ID, p.UserID, p.AgentID, p.CreatedAt, p.RevokedAt = newID("pass"), userID, ag.ID, now, nil
	if err := s.store.Create(ctx, &p); err != nil {
		_ = s.agents.Revoke(ctx, userID, ag.ID)
		return nil, err
	}
	view, err := s.view(ctx, p)
	if err != nil {
		return nil, err
	}
	return &IssuedPass{PassView: *view, Token: token}, nil
}

// List returns the person's passes, newest first, with budgets.
func (s *SpendPassService) List(ctx context.Context, userID string) ([]PassView, error) {
	passes, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]PassView, 0, len(passes))
	for _, p := range passes {
		v, err := s.view(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// ForAgent is the pass an agent is spending under, with its budget — what
// an external agent reads to know its own limits.
func (s *SpendPassService) ForAgent(ctx context.Context, agentID string) (*PassView, error) {
	p, err := s.store.GetByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.view(ctx, *p)
}

// OwnedActive returns the person's pass by ID, if it is theirs and can still
// spend. Someone else's pass is "not found", never "forbidden": its existence
// isn't confirmed to anyone but its owner.
func (s *SpendPassService) OwnedActive(ctx context.Context, userID, passID string) (*spendpass.Pass, error) {
	p, err := s.store.Get(ctx, passID)
	if err != nil {
		return nil, err
	}
	if p.UserID != userID {
		return nil, fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	if !p.Active(s.now()) {
		return nil, fmt.Errorf("%w: that Spend Pass is revoked or expired", shared.ErrUnauthorized)
	}
	return p, nil
}

// Revoke ends a pass and its agent token at once. Only the pass's owner can.
func (s *SpendPassService) Revoke(ctx context.Context, userID, passID string) error {
	p, err := s.store.Get(ctx, passID)
	if err != nil {
		return err
	}
	if p.UserID != userID {
		return fmt.Errorf("%w: pass %s", shared.ErrNotFound, passID)
	}
	if p.RevokedAt != nil {
		return nil
	}
	if err := s.store.Revoke(ctx, passID, s.now()); err != nil {
		return err
	}
	return s.agents.Revoke(ctx, userID, p.AgentID)
}

// CheckPurchase implements PassGate.
func (s *SpendPassService) CheckPurchase(ctx context.Context, agentID, merchant, category string, amount money.Amount) (*policy.PolicyDecision, *spendpass.Pass, error) {
	p, err := s.store.GetByAgent(ctx, agentID)
	if errors.Is(err, shared.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	spent, err := s.spent(ctx, *p)
	if err != nil {
		return nil, nil, err
	}
	d := p.Evaluate(now, spendpass.Purchase{
		Merchant: merchant, Category: category, AmountMinor: amount.MinorUnits, Currency: amount.Currency, SpentInWindow: spent,
	})
	return d, p, nil
}

func (s *SpendPassService) view(ctx context.Context, p spendpass.Pass) (*PassView, error) {
	now := s.now()
	spent, err := s.spent(ctx, p)
	if err != nil {
		return nil, err
	}
	return &PassView{Pass: p, Active: p.Active(now), SpentMinorUnits: spent, RemainingMinor: p.Remaining(spent), WindowStartsAt: p.WindowStart(now)}, nil
}
