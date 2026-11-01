package mcpserver

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// readOnlyTools are the tools that move no money and change nothing: they
// read, price for free or compare. A client that asks the person before every
// tool without readOnlyHint (ChatGPT treats such a tool as a write action)
// then asks only before algebra_execute and opening or closing a job.
var readOnlyTools = map[string]bool{
	"algebra_classes":      true,
	"algebra_discover":     true,
	"algebra_discover_web": true,
	"algebra_quote":        true,
	"algebra_simulate":     true,
	"algebra_status":       true,
	"algebra_receipt":      true,
	"algebra_spend_pass":   true,
	"algebra_cross_check":  true,
	"algebra_job_status":   true,
}

// toolHintsMiddleware adds readOnlyHint to the read-only tools in tools/list.
// The listed tools are the server's own, so each is copied before it is
// marked.
func toolHintsMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
		res, err := next(ctx, method, req)
		list, ok := res.(*gomcp.ListToolsResult)
		if err != nil || !ok {
			return res, err
		}
		tools := make([]*gomcp.Tool, len(list.Tools))
		for i, t := range list.Tools {
			if readOnlyTools[t.Name] && (t.Annotations == nil || !t.Annotations.ReadOnlyHint) {
				c := *t
				c.Annotations = &gomcp.ToolAnnotations{ReadOnlyHint: true}
				t = &c
			}
			tools[i] = t
		}
		out := *list
		out.Tools = tools
		return &out, nil
	}
}
