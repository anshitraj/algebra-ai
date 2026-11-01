package mcpserver

import "testing"

// A tool that can pay or change a job is not marked read-only, so a client
// asks before it; every other tool is, and is not asked about.
func TestOnlyToolsThatMoveNoMoneyAreMarkedReadOnly(t *testing.T) {
	tools := toolsByName(t, connect(t, &Server{}))
	for _, name := range []string{"algebra_execute", "algebra_job_open", "algebra_job_close"} {
		if tool := tools[name]; tool == nil || (tool.Annotations != nil && tool.Annotations.ReadOnlyHint) {
			t.Errorf("%s spends or changes a job: it must not be read-only (%v)", name, tool)
		}
	}
	for name := range readOnlyTools {
		tool := tools[name]
		if tool == nil {
			t.Errorf("%s is not listed", name)
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s moves no money: it should be read-only", name)
		}
	}
	// Every listed tool is in one group or the other.
	for name := range tools {
		if !readOnlyTools[name] && name != "algebra_execute" && name != "algebra_job_open" && name != "algebra_job_close" {
			t.Errorf("%s is new: say whether it is read-only in readOnlyTools", name)
		}
	}
}
