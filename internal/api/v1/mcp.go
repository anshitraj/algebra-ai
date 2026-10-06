package v1

import (
	"net/http"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/mcpserver"
)

// mcpHandler serves the MCP tools (algebra.execute, algebra.simulate,
// algebra.classes and the rest) over streamable HTTP at /mcp, from this
// process. An agent connects with its Spend Pass token as
// "Authorization: Bearer <token>", so the token never passes through the model.
//
// It is stateless: each request stands alone, so any instance behind a load
// balancer can answer it, and the JSON response mode needs no streaming
// through the web app's proxy. Serving it here rather than from cmd/mcp is
// what lets execution on the sandbox rail work over MCP: the sandbox provider,
// and the loopback port the HTTP client may reach it on, live in this process.
func (a *API) mcpHandler() http.Handler {
	server := mcpserver.NewMCPServer(a.b.MCP())
	return gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server }, &gomcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true,
	})
}
