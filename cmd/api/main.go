// Command api runs Algebra's versioned REST API (mandate §45), sharing the
// exact same application services as cmd/mcp via internal/platform/wiring.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	v1 "github.com/project-algebra/algebra/internal/api/v1"
	"github.com/project-algebra/algebra/internal/platform/config"
	"github.com/project-algebra/algebra/internal/platform/logging"
	"github.com/project-algebra/algebra/internal/platform/wiring"
)

func main() {
	logger := logging.New(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("loading config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	bundle, err := wiring.Build(ctx, cfg, "migrations")
	if err != nil {
		logger.Error("building application", "error", err)
		os.Exit(1)
	}
	defer bundle.DB.Close()

	handler := v1.NewRouter(bundle, bundle.Limiter, cfg.CORSAllowedOrigins)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Discovery fans out to merchants with a per-connector budget
		// (CONNECTOR_TIMEOUT, 20s default) — leave room above it.
		WriteTimeout:   90 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Expired sessions and used/expired reset tokens are dead weight; sweep
	// them hourly. Failures are logged, never fatal.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if n, err := bundle.Accounts.PurgeExpired(ctx); err != nil {
				logger.Warn("purging expired sessions", "error", err)
			} else if n > 0 {
				logger.Info("purged expired sessions and reset tokens", "rows", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	// Economic coordination sweeper: expired leases are released, overdue
	// executions become UNKNOWN (never FAILED), UNKNOWN outcomes are
	// reconciled against the rail, and stale intents expire.
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if n, err := bundle.Economic.Sweep(ctx); err != nil {
				logger.Warn("economic sweep", "error", err)
			} else if n > 0 {
				logger.Info("economic sweep", "handled", n)
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("algebra API listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}
