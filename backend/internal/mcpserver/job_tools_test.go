package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/app"
)

func toolsByName(t *testing.T, cs *gomcp.ClientSession) map[string]*gomcp.Tool {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*gomcp.Tool{}
	for _, tool := range list.Tools {
		out[tool.Name] = tool
	}
	return out
}

func TestJobToolsAreListedAndTellAnAgentHowToUseThem(t *testing.T) {
	tools := toolsByName(t, connect(t, &Server{}))
	open, status, closing, execute := tools["algebra_job_open"], tools["algebra_job_status"], tools["algebra_job_close"], tools["algebra_execute"]
	if open == nil || status == nil || closing == nil {
		t.Fatalf("the three job tools must be listed, got %d tools", len(tools))
	}
	for _, want := range []string{"label", "budget_usdc"} {
		if !slices.Contains(requiredOf(t, open), want) {
			t.Errorf("algebra_job_open must require %s", want)
		}
	}
	// A sub-agent with a job token may leave the job out to mean its own.
	for _, tool := range []*gomcp.Tool{status, closing} {
		if len(requiredOf(t, tool)) != 0 {
			t.Errorf("%s: nothing is required, a job token names its own job: %v", tool.Name, requiredOf(t, tool))
		}
	}
	// What an agent reads before it opens a job and hands its token on.
	for _, phrase := range []string{"job's `token`, once", "sub-agent", "never widens", "moves no money", "micro-USDC", "profit and loss", "isn't opened"} {
		if !strings.Contains(open.Description, phrase) {
			t.Errorf("algebra_job_open must tell the agent about %q", phrase)
		}
	}
	if !strings.Contains(status.Description, "moves no money") || !strings.Contains(closing.Description, "goes back") {
		t.Errorf("status and close say what they do: %q / %q", status.Description, closing.Description)
	}
	schema, _ := execute.InputSchema.(map[string]any)
	if asMap(schema["properties"])["job_id"] == nil || !strings.Contains(execute.Description, "algebra_job_open") {
		t.Error("algebra_execute must take a job_id and point at algebra_job_open")
	}
}

func TestJobToolsReportWhenJobPassesAreOff(t *testing.T) {
	cs := connect(t, &Server{})
	for name, args := range map[string]map[string]any{
		"algebra_job_open":   {"label": "x", "budget_usdc": "0.05"},
		"algebra_job_status": {"job_id": "job_x"},
		"algebra_job_close":  {"job_id": "job_x"},
	} {
		res, err := cs.CallTool(context.Background(), &gomcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(textOf(res), "not enabled") {
			t.Errorf("%s on a server without job passes must say so: %+v", name, res)
		}
	}
}

// A job token is not an agent's: every tool but the ones it is for says so and
// says what it can do, instead of "invalid agent_token".
func TestAJobTokenIsToldWhatItCanDo(t *testing.T) {
	srv := &Server{}
	_, err := srv.resolveAgent(context.Background(), "ajob_abcdef")
	for _, want := range []string{"job token", "algebra_execute", "algebra_job_open", "algebra_job_status", "algebra_job_close"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("a job token used as an agent's: %v, want %q", err, want)
		}
	}
	// The same through the bearer a connection sends.
	ctx := context.WithValue(context.Background(), bearerKey{}, "ajob_abcdef")
	if _, err := srv.resolveAgent(ctx, ""); err == nil || !strings.Contains(err.Error(), "job token") {
		t.Errorf("a job token in the Authorization header: %v", err)
	}
}

func TestDescribeJobError(t *testing.T) {
	short := describeJobError(&app.JobShort{LeftMinor: 20_000, WantedMinor: 50_000})
	for _, want := range []string{"JOB_PARENT_SHORT", "0.02 USDC", "0.05 USDC", "close a job"} {
		if short == nil || !strings.Contains(short.Error(), want) {
			t.Errorf("a budget the parent can't cover: %v, want %q", short, want)
		}
	}
	if err := describeJobError(&app.AuthorityDenied{ReasonCodes: []string{"PASS_FROZEN"}}); err == nil || !strings.Contains(err.Error(), "PASS_FROZEN") {
		t.Errorf("a frozen pass: %v", err)
	}
	// The prefix of an invalid request is for logs, not for an agent.
	if err := describeJobError(fmt.Errorf("%w: give the job a name", app.ErrInvalidRequest)); err == nil || strings.Contains(err.Error(), "algebra:") || !strings.Contains(err.Error(), "give the job a name") {
		t.Errorf("an invalid request: %v", err)
	}
}

func TestUSDCOfReadsAnOptionalAmount(t *testing.T) {
	if v, err := usdcOf("value_usdc", "  "); v != nil || err != nil {
		t.Errorf("nothing is nothing: %v %v", v, err)
	}
	if v, err := usdcOf("value_usdc", "0.25"); err != nil || v == nil || *v != 250_000 {
		t.Errorf("a quarter of a USDC: %v %v", v, err)
	}
	if v, err := usdcOf("value_usdc", "0"); err != nil || v == nil || *v != 0 {
		t.Errorf("a task can be worth nothing: %v %v", v, err)
	}
	for _, bad := range []string{"abc", "-1", "1.2345678", "0.5 USDC"} {
		if v, err := usdcOf("value_usdc", bad); err == nil || v != nil || !strings.Contains(err.Error(), "value_usdc") {
			t.Errorf("%q must be refused naming the field: %v %v", bad, v, err)
		}
	}
}
