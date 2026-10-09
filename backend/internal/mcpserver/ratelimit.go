package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
)

const (
	defaultMCPRateLimit       = 120
	defaultMCPRateLimitWindow = time.Minute
)

// rateLimitMiddleware runs on every incoming MCP method (mandate §49). It
// only ever limits tools/call requests — other methods (initialize,
// tools/list, ...) pass straight through, since they carry no per-agent
// identity to key on and aren't the mutating surface the mandate is
// concerned with.
//
// Keying prefers the calling agent (extracted from the tool call's own
// agent_token argument — every mutating tool already carries one, see
// server.go's package doc) over any transport-level address, since stdio
// (this build's default transport) has no per-connection remote address to
// key on in the first place.
func (srv *Server) rateLimitMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
		if srv.Limiter == nil {
			return next(ctx, method, req)
		}
		params, ok := req.GetParams().(*gomcp.CallToolParamsRaw)
		if !ok {
			return next(ctx, method, req)
		}

		key := "mcp:tool:" + params.Name
		var args struct {
			AgentToken string `json:"agent_token"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err == nil && args.AgentToken != "" {
			key = "mcp:agent:" + agentpkg.HashToken(args.AgentToken)
		} else if token := bearerFromContext(ctx); token != "" {
			key = "mcp:agent:" + agentpkg.HashToken(token)
		}

		allowed, retryAfter, err := srv.Limiter.Allow(ctx, "ratelimit:"+key, defaultMCPRateLimit, defaultMCPRateLimitWindow)
		if err != nil {
			// Fail open — see internal/api/v1's identical rationale: a
			// Redis hiccup must not take down tool calls over a
			// defense-in-depth control.
			return next(ctx, method, req)
		}
		if !allowed {
			return nil, fmt.Errorf("rate limit exceeded for %q, retry after %s", params.Name, retryAfter.Round(time.Second))
		}
		return next(ctx, method, req)
	}
}
