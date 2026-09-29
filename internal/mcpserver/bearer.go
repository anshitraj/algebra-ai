package mcpserver

import (
	"context"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerKey struct{}

// bearerMiddleware lifts "Authorization: Bearer <token>" off a streamable
// HTTP request into the context, where resolveAgent finds it when a tool
// call leaves agent_token empty. This is how an MCP client — Claude,
// ChatGPT, Cursor, a company's own agent — connects with a Spend Pass token
// configured once as a header, so the token never passes through the model
// or the conversation. stdio carries no headers and is unaffected.
func bearerMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
		if extra := req.GetExtra(); extra != nil && extra.Header != nil {
			if token := parseBearer(extra.Header.Get("Authorization")); token != "" {
				ctx = context.WithValue(ctx, bearerKey{}, token)
			}
		}
		return next(ctx, method, req)
	}
}

func parseBearer(h string) string {
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func bearerFromContext(ctx context.Context) string {
	token, _ := ctx.Value(bearerKey{}).(string)
	return token
}
