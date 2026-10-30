package v1

import (
	"net/http"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/mcpserver"
)

// mcpHandler serves the MCP tools (algebra_execute, algebra_simulate,
// algebra_classes and the rest) over streamable HTTP at /mcp, from this
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
		// The SDK refuses a request whose Host isn't loopback when the server listens on a loopback address, a guard
		// against DNS rebinding for tools a browser can reach on someone's own machine. This API listens on
		// 127.0.0.1 behind a proxy (Caddy, a Cloudflare tunnel, the web app's rewrite) that forwards the public
		// hostname, so every real request would be refused with 403. Nothing here relies on the guard: a call needs
		// a bearer token, which a page in a browser doesn't hold, and no cookie ever authorizes /mcp.
		DisableLocalhostProtection: true,
	})
}
