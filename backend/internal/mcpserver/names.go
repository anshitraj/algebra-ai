package mcpserver

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// earlierNames maps the names the execution tools were listed under until
// October 2026 (algebra.execute) to today's (algebra_execute).
//
// MCP itself allows a dot in a tool name, but a client hands the tools to a
// model as functions, and the function names of OpenAI's and Anthropic's APIs
// take only letters, digits, "_" and "-". A client then has to rewrite a
// dotted name (and each does it its own way) or leave the tool out. Today's
// names need no rewriting anywhere, so an agent sees the same name in Claude,
// Cursor, Codex and Antigravity.
var earlierNames = map[string]string{
	"algebra.discover":     "algebra_discover",
	"algebra.quote":        "algebra_quote",
	"algebra.simulate":     "algebra_simulate",
	"algebra.execute":      "algebra_execute",
	"algebra.status":       "algebra_status",
	"algebra.receipt":      "algebra_receipt",
	"algebra.spend_pass":   "algebra_spend_pass",
	"algebra.classes":      "algebra_classes",
	"algebra.discover_web": "algebra_discover_web",
}

// earlierNameMiddleware serves a tools/call that still uses a dotted name
// with today's tool, so an integration written before the rename keeps
// working. The dotted names are answered but never listed: listing both
// would show an agent every tool twice, and one dotted name in the list is
// enough for a strict client to refuse the whole server.
func earlierNameMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
		if params, ok := req.GetParams().(*gomcp.CallToolParamsRaw); ok {
			if name, ok := earlierNames[params.Name]; ok {
				params.Name = name
			}
		}
		return next(ctx, method, req)
	}
}
