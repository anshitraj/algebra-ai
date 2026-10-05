// Command mcp runs Algebra's MCP server (mandate §5/§6) against the
// current 2026-07-28 spec, via github.com/modelcontextprotocol/go-sdk.
// Defaults to stdio (the SDK's own quick-start pattern, suited to local
// agent development); pass -http=:8081 to instead serve the stateless
// streamable-HTTP transport for a networked deployment. Either way, tool
// handlers share the exact same application services as cmd/api — see
// internal/platform/wiring and internal/mcpserver's package doc.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/project-algebra/algebra/internal/mcpserver"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/logging"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

func main() {
	httpAddr := flag.String("http", "", "if set, serve streamable HTTP on this address instead of stdio (e.g. :8081)")
	flag.Parse()

	logger := logging.New(os.Stderr, slog.LevelInfo) // stderr: stdout is the stdio transport's wire format
	ctx := context.Background()

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("loading config", "error", err)
		os.Exit(1)
	}

	bundle, err := wiring.Build(ctx, cfg, "migrations")
	if err != nil {
		logger.Error("building application", "error", err)
		os.Exit(1)
	}
	defer bundle.DB.Close()

	srv := &mcpserver.Server{
		Agents: bundle.Agents, Intents: bundle.Intents, Discovery: bundle.Discovery, Quotes: bundle.Quotes,
		Policy: bundle.Policy, Orders: bundle.Orders, Payments: bundle.Payments, Privacy: bundle.Privacy,
		Connectors: bundle.Connectors, Idempotency: bundle.Idempotency, Limiter: bundle.Limiter,
		Integrators: bundle.Integrators, TransactionPolicy: bundle.TransactionPolicy,
		PaymentIntents: bundle.PaymentIntentSvc, CommerceProfiles: bundle.CommerceProfileSvc, SpendPasses: bundle.SpendPasses,
		Economic: bundle.Economic, Execution: bundle.Execution, ExecutionProviders: bundle.ExecutionProviders,
	}
	server := mcpserver.NewMCPServer(srv)

	if *httpAddr == "" {
		logger.Info("algebra MCP server listening on stdio")
		if err := server.Run(ctx, &gomcp.StdioTransport{}); err != nil {
			logger.Error("mcp server exited", "error", err)
			os.Exit(1)
		}
		return
	}

	handler := gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server }, nil)
	logger.Info("algebra MCP server listening on streamable HTTP", "addr", *httpAddr)
	if err := http.ListenAndServe(*httpAddr, handler); err != nil { //nolint:gosec // dev/internal transport; TLS terminated upstream in deployment
		logger.Error("mcp http server exited", "error", err)
		os.Exit(1)
	}
}
