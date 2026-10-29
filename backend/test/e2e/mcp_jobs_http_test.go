package e2e

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Job Passes over MCP, against Postgres and the sandbox sellers: an agent sets a
// task's budget aside, pays under it, hands the job's token to a sub-agent, and
// closes the job. The sub-agent reaches its own job and nothing else.
func TestMCPOverHTTP_AnAgentSetsABudgetAsideForATaskAndHandsItToASubAgent(t *testing.T) {
	b, base := serve(t)
	token := issuePass(t, b)
	cs := mcpSession(t, base, token)

	ask := func(extra map[string]any) map[string]any {
		req := map[string]any{
			"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
			"max_price_usdc": "0.05", "window": "mcp-job-" + uuid.NewString(), "fresh": true,
		}
		for k, v := range extra {
			req[k] = v
		}
		return req
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	num := func(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }

	// A job of 0.05 USDC, worth 0.10.
	job, text, isErr := callTool(t, cs, "algebra_job_open", map[string]any{"label": "Check a token", "budget_usdc": "0.05", "value_usdc": "0.10"})
	if isErr || job["state"] != "ACTIVE" || num(job, "budget_minor") != 50_000 || num(job, "left_minor") != 50_000 || job["live"] != true {
		t.Fatalf("open: %v %s", job, text)
	}
	jobID, jobToken := str(job, "id"), str(job, "token")
	if !strings.HasPrefix(jobToken, "ajob_") || jobID == "" {
		t.Fatalf("the answer carries the job's token once: %v", job)
	}
	if again, _, _ := callTool(t, cs, "algebra_job_status", map[string]any{"job_id": jobID}); again["token"] != nil {
		t.Errorf("the token is shown once, never on a later read: %v", again["token"])
	}

	// The agent pays under the job.
	paid, text, isErr := callTool(t, cs, "algebra_execute", ask(map[string]any{"job_id": jobID}))
	if isErr || paid["delivered"] != true {
		t.Fatalf("pay under the job: %v %s", paid, text)
	}
	status, text, isErr := callTool(t, cs, "algebra_job_status", map[string]any{"job_id": jobID})
	attempts, _ := status["attempts"].([]any)
	pnl, _ := status["pnl"].(map[string]any)
	if isErr || num(status, "paid_minor") <= 0 || len(attempts) != 1 || num(pnl, "value_minor") != 100_000 || pnl["profit_minor"] == nil {
		t.Fatalf("status after one payment: %v %s", status, text)
	}
	afterOne := num(status, "paid_minor")

	// A sub-agent holding the job token pays under that job without naming it.
	sub := mcpSession(t, base, jobToken)
	if paid, text, isErr := callTool(t, sub, "algebra_execute", ask(nil)); isErr || paid["delivered"] != true {
		t.Fatalf("the sub-agent pays under its job: %v %s", paid, text)
	}
	own, text, isErr := callTool(t, sub, "algebra_job_status", map[string]any{})
	attempts, _ = own["attempts"].([]any)
	if isErr || str(own, "id") != jobID || len(attempts) != 2 || num(own, "paid_minor") <= afterOne {
		t.Fatalf("the sub-agent reads its own job, and both payments are in it: %v %s", own, text)
	}

	// It is not an agent: every other tool says what a job token is for.
	if _, text, isErr := callTool(t, sub, "algebra_spend_pass", map[string]any{}); !isErr || !strings.Contains(text, "job token only works with") {
		t.Errorf("a job token reading the Spend Pass: %v %s", isErr, text)
	}

	// It opens a smaller job inside its own, and closing it gives the budget back.
	small, text, isErr := callTool(t, sub, "algebra_job_open", map[string]any{"label": "Sub-task", "budget_usdc": "0.01"})
	if isErr || str(small, "parent_id") != jobID || !strings.HasPrefix(str(small, "token"), "ajob_") || small["token"] == jobToken {
		t.Fatalf("a smaller job inside the sub-agent's: %v %s", small, text)
	}
	inside, _, _ := callTool(t, sub, "algebra_job_status", map[string]any{})
	if num(inside, "set_aside_minor") != 10_000 {
		t.Errorf("the smaller job's budget is set aside from the job: %v", inside["set_aside_minor"])
	}
	closedSmall, text, isErr := callTool(t, sub, "algebra_job_close", map[string]any{"job_id": str(small, "id")})
	if isErr || closedSmall["state"] != "CLOSED" {
		t.Fatalf("close the smaller job: %v %s", closedSmall, text)
	}
	back, _, _ := callTool(t, sub, "algebra_job_status", map[string]any{})
	if num(back, "set_aside_minor") != 0 {
		t.Errorf("what the smaller job didn't use went back: %v", back["set_aside_minor"])
	}

	// A smaller job the job can't cover isn't opened, and the refusal says what is left.
	_, text, isErr = callTool(t, sub, "algebra_job_open", map[string]any{"label": "Too much", "budget_usdc": "0.05"})
	if !isErr || !strings.Contains(text, "JOB_PARENT_SHORT") || !strings.Contains(text, "USDC is left") {
		t.Errorf("a job the parent can't cover: %v %s", isErr, text)
	}

	// The sub-agent can't reach a job outside its own tree, nor can another person's agent reach any.
	other, _, _ := callTool(t, cs, "algebra_job_open", map[string]any{"label": "Another task", "budget_usdc": "0.01"})
	if _, text, isErr := callTool(t, sub, "algebra_job_status", map[string]any{"job_id": str(other, "id")}); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("a job outside the sub-agent's tree: %v %s", isErr, text)
	}
	stranger := mcpSession(t, base, issuePass(t, b))
	if _, text, isErr := callTool(t, stranger, "algebra_job_status", map[string]any{"job_id": jobID}); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("another person's agent reading the job: %v %s", isErr, text)
	}
	if _, text, isErr := callTool(t, stranger, "algebra_execute", ask(map[string]any{"job_id": jobID})); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("another person's agent paying under the job: %v %s", isErr, text)
	}
	if _, _, isErr := callTool(t, mcpSession(t, base, "ajob_not-a-real-token"), "algebra_job_status", map[string]any{}); !isErr {
		t.Error("a job token that was never minted")
	}

	// Closing says what the task turned out to be worth, and gives back what it didn't use.
	closed, text, isErr := callTool(t, cs, "algebra_job_close", map[string]any{"job_id": jobID, "value_usdc": "0.20"})
	closedPnL, _ := closed["pnl"].(map[string]any)
	if isErr || closed["state"] != "CLOSED" || closed["live"] != false || num(closed, "returned_minor") <= 0 || num(closedPnL, "value_minor") != 200_000 {
		t.Fatalf("close the job: %v %s", closed, text)
	}
	// Nothing more is paid under a job that has ended, by the agent or by the sub-agent.
	for who, s := range map[string]*struct{ call func() (string, bool) }{
		"the agent": {func() (string, bool) {
			_, text, isErr := callTool(t, cs, "algebra_execute", ask(map[string]any{"job_id": jobID}))
			return text, isErr
		}},
		"the sub-agent": {func() (string, bool) {
			_, text, isErr := callTool(t, sub, "algebra_execute", ask(nil))
			return text, isErr
		}},
	} {
		if text, isErr := s.call(); !isErr || !strings.Contains(text, "JOB_CLOSED") {
			t.Errorf("%s paying under a closed job: %v %s", who, isErr, text)
		}
	}
	// Closing it again changes nothing.
	if again, _, isErr := callTool(t, cs, "algebra_job_close", map[string]any{"job_id": jobID}); isErr || again["state"] != "CLOSED" || num(again, "returned_minor") != num(closed, "returned_minor") {
		t.Errorf("closing twice: %v", again)
	}
}

// A job can't spend more than it was given, whatever the agent asks for.
func TestMCPOverHTTP_ATaskCannotSpendMoreThanItsBudget(t *testing.T) {
	b, base := serve(t)
	cs := mcpSession(t, base, issuePass(t, b))
	job, text, isErr := callTool(t, cs, "algebra_job_open", map[string]any{"label": "Tiny", "budget_usdc": "0.000001"})
	if isErr || job["budget_minor"] != float64(1) {
		t.Fatalf("open: %v %s", job, text)
	}
	_, text, isErr = callTool(t, cs, "algebra_execute", map[string]any{
		"capability": "solana.token-risk", "input": map[string]any{"mint": "So11111111111111111111111111111111111111112"},
		"max_price_usdc": "0.05", "window": "mcp-tiny-" + uuid.NewString(), "job_id": job["id"],
	})
	if !isErr || !strings.Contains(text, "JOB_BUDGET_EXCEEDED") {
		t.Errorf("a payment over the job's budget: %v %s", isErr, text)
	}
	// And a job larger than the Spend Pass has left isn't opened.
	if _, text, isErr := callTool(t, cs, "algebra_job_open", map[string]any{"label": "Huge", "budget_usdc": "5"}); !isErr || !strings.Contains(text, "JOB_PARENT_SHORT") {
		t.Errorf("a job the pass can't cover: %v %s", isErr, text)
	}
	// What can't be read as an amount is refused before anything is set aside.
	for _, bad := range []map[string]any{
		{"label": "x", "budget_usdc": "lots"},
		{"label": "x", "budget_usdc": "0"},
		{"label": "x", "budget_usdc": "0.05", "value_usdc": "-1"},
		{"label": "", "budget_usdc": "0.05"},
	} {
		if _, text, isErr := callTool(t, cs, "algebra_job_open", bad); !isErr {
			t.Errorf("%v must be refused: %s", bad, text)
		}
	}
}
