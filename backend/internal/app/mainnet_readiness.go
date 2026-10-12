package app

// Mainnet readiness: what stands between this server and paying with real
// money, said plainly. Reading it moves nothing and changes nothing.

// MainnetFacts is what the server knows about its own mainnet set-up.
type MainnetFacts struct {
	// ExecutionEnabled: MAINNET_EXECUTION=on.
	ExecutionEnabled bool
	// RailConfigured: a mainnet wallet is loaded. Payer is its address.
	RailConfigured bool
	Payer          string
	// ProgramDeployed: the Spend Pass program answers on mainnet.
	ProgramDeployed bool
	ProgramID       string
	// PassesRequired: SPEND_PASS_REQUIRED includes mainnet.
	PassesRequired bool
	// AuthorityKnown: the program's upgrade authority could be read.
	// Immutable: it has none. UpgradeAuthority: the key, when it has one.
	AuthorityKnown   bool
	Immutable        bool
	UpgradeAuthority string
}

// MainnetGate is one requirement. Blocking gates are enforced by the server:
// while one fails no mainnet payment is signed. The others cannot be checked
// by code and are the operator's to settle before customers' funds go in.
type MainnetGate struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Passed   bool   `json:"passed"`
	Blocking bool   `json:"blocking"`
	Detail   string `json:"detail,omitempty"`
}

// MainnetReadiness is the answer to "can this server pay on mainnet, and
// should it?".
type MainnetReadiness struct {
	Network string `json:"network"`
	// CanPay: every blocking gate passes, so a mainnet payment can be signed.
	CanPay bool `json:"can_pay"`
	// ReadyForCustomerFunds: every gate passes, including the ones only a
	// person can settle.
	ReadyForCustomerFunds bool `json:"ready_for_customer_funds"`
	// WorksToday is what mainnet offers whatever the gates say.
	WorksToday []string      `json:"works_today"`
	Gates      []MainnetGate `json:"gates"`
}

// AssessMainnet turns the facts into gates.
func AssessMainnet(f MainnetFacts) MainnetReadiness {
	gate := func(id, label string, passed, blocking bool, whyNot string) MainnetGate {
		g := MainnetGate{ID: id, Label: label, Passed: passed, Blocking: blocking}
		if !passed {
			g.Detail = whyNot
		}
		return g
	}
	authorityOK, authorityWhy := false, "the program is not deployed on mainnet, so there is no authority to check"
	switch {
	case !f.ProgramDeployed:
	case !f.AuthorityKnown:
		authorityWhy = "the upgrade authority could not be read from the cluster"
	case f.Immutable:
		authorityOK = true
	case f.UpgradeAuthority == f.Payer:
		authorityWhy = "the upgrade authority is this server's own payer key: whoever takes the server could replace the program"
	default:
		authorityOK = true
	}
	out := MainnetReadiness{
		Network: "solana",
		WorksToday: []string{
			"discover mainnet providers",
			"ask them for unpaid quotes",
			"compare cost, speed and health",
			"dry-run a request against a Spend Pass",
		},
		Gates: []MainnetGate{
			gate("rail", "A mainnet wallet is configured", f.RailConfigured, true, "no mainnet wallet is loaded (SOLANA_MAINNET_KEYPAIR_FILE with SOLANA_ALLOW_MAINNET=yes)"),
			gate("program", "The Spend Pass program is deployed on mainnet", f.ProgramDeployed, true, "the program does not answer on mainnet"),
			gate("passes_required", "Every mainnet payment must come from an on-chain pass", f.PassesRequired, true, "SPEND_PASS_REQUIRED does not include mainnet, so the payer could spend money that is no pass's"),
			gate("upgrade_authority", "The program's upgrade authority is not a key this server holds", authorityOK, true, authorityWhy),
			gate("execution", "Mainnet execution is switched on", f.ExecutionEnabled, true, "MAINNET_EXECUTION is off: every mainnet payment is refused with MAINNET_EXECUTION_DISABLED"),
			gate("review", "The program has had an independent security review", false, false, "not done: nobody outside the team has reviewed the program"),
			gate("signer", "The payer key is held outside the server (KMS or HSM)", false, false, "the payer key is a file or an environment variable on the server"),
		},
	}
	out.CanPay, out.ReadyForCustomerFunds = true, true
	for _, g := range out.Gates {
		if !g.Passed {
			out.ReadyForCustomerFunds = false
			if g.Blocking {
				out.CanPay = false
			}
		}
	}
	return out
}
