package tenancy

import "time"

// Plan IDs, as organizations.plan stores them.
const (
	PlanFree       = "free"
	PlanBuilder    = "builder"
	PlanStartup    = "startup"
	PlanBusiness   = "business"
	PlanEnterprise = "enterprise"
)

// TrialPlan is the plan a trial gives, and TrialLength how long it lasts. A
// person gets one trial, whichever of their organizations takes it.
const (
	TrialPlan   = PlanStartup
	TrialLength = 7 * 24 * time.Hour
)

// MaxOwnedOrganizations is how many organizations one account may own. Plans
// count projects per organization, so this bounds what one person can get on
// the Free plan.
const MaxOwnedOrganizations = 3

// Unlimited marks a limit a plan doesn't have.
const Unlimited = -1

// Plan is one commercial plan and what it allows. Prices are Algebra's own
// fee in US cents a month; what a provider charges and what a network costs
// are the customer's, paid from their own Spend Pass, and never in here.
type Plan struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// MonthlyUSDCents is the list price; Unlimited means "ask us".
	MonthlyUSDCents int `json:"monthly_usd_cents"`
	// Projects is how many live projects the organization may have.
	Projects int `json:"projects"`
	// ManagedExecutions is how many paid executions a month the plan
	// includes on devnet and mainnet. Sandbox runs are never counted.
	ManagedExecutions int `json:"managed_executions"`
	// Members is how many people the organization may have.
	Members int `json:"members"`
	// Trial reports whether the plan can be tried for TrialLength.
	Trial bool `json:"trial"`
}

// Plans in the order the pricing page shows them.
var Plans = []Plan{
	{ID: PlanFree, Name: "Free", MonthlyUSDCents: 0, Projects: 1, ManagedExecutions: 100, Members: 1},
	{ID: PlanBuilder, Name: "Builder", MonthlyUSDCents: 1900, Projects: 3, ManagedExecutions: 2_000, Members: 3},
	{ID: PlanStartup, Name: "Startup", MonthlyUSDCents: 5900, Projects: 10, ManagedExecutions: 10_000, Members: 10, Trial: true},
	{ID: PlanBusiness, Name: "Business", MonthlyUSDCents: 19900, Projects: 50, ManagedExecutions: 100_000, Members: 50},
	{ID: PlanEnterprise, Name: "Enterprise", MonthlyUSDCents: Unlimited, Projects: Unlimited, ManagedExecutions: Unlimited, Members: Unlimited},
}

// PlanByID returns the plan with that ID; an unknown ID is the free plan, so
// a bad row can only ever give less.
func PlanByID(id string) Plan {
	for _, p := range Plans {
		if p.ID == id {
			return p
		}
	}
	return Plans[0]
}

// TrialActive reports whether o's trial is running at now.
func (o Organization) TrialActive(now time.Time) bool {
	return o.TrialPlan != "" && o.TrialEndsAt != nil && now.Before(*o.TrialEndsAt)
}

// TrialUsed reports whether o has ever started a trial: there is one each.
func (o Organization) TrialUsed() bool { return o.TrialEndsAt != nil }

// EffectivePlan is the plan o has at now: its trial's plan while the trial
// runs (unless its own plan is already higher), otherwise its own. A trial
// that ends needs no job to undo it.
func (o Organization) EffectivePlan(now time.Time) Plan {
	own := PlanByID(o.Plan)
	if o.TrialActive(now) {
		if trial := PlanByID(o.TrialPlan); planRank(trial.ID) > planRank(own.ID) {
			return trial
		}
	}
	return own
}

func planRank(id string) int {
	for i, p := range Plans {
		if p.ID == id {
			return i
		}
	}
	return 0
}

// Allows reports whether a limit admits one more when used are taken.
func Allows(limit, used int) bool { return limit == Unlimited || used < limit }
