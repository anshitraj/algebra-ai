package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type requestIDKey struct{}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// requestLog assigns every request an ID (reusing a well-formed upstream
// X-Request-ID, e.g. from a load balancer), echoes it in the response, and
// writes one structured access-log line: method, path, status, duration,
// client IP. Never the query string, body, cookies or Authorization — the
// path carries only resource IDs, never secrets.
func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			var b [12]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		logger.LogAttrs(r.Context(), level, "http",
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("ip", clientMeta(r).IP),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush keeps streaming responses working through the recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// securityHeaders sets the headers every API response should carry. API
// responses are per-user and dynamic, so nothing is cacheable by a shared
// cache.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// Sign-in endpoints get a much tighter per-IP budget than the general API
// limit: they're the credential-stuffing and email-bombing surface.
const (
	authRateLimit       = 10
	authRateLimitWindow = time.Minute
)

func isAuthAbuseTarget(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/auth/login", "/api/v1/auth/signup", "/api/v1/auth/password/forgot", "/api/v1/auth/password/reset",
		"/api/v1/auth/demo", "/api/v1/auth/privy":
		return true
	}
	return false
}

// readyz reports whether this instance can serve traffic: Postgres (and
// Redis, when configured) must answer within two seconds. /healthz stays a
// dependency-free liveness check so a database blip doesn't get every
// instance restarted.
func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"postgres": "ok"}
	status := http.StatusOK
	if a.b.DB == nil || a.b.DB.Pool.Ping(ctx) != nil {
		checks["postgres"] = "unavailable"
		status = http.StatusServiceUnavailable
	}
	if a.b.Redis != nil {
		checks["redis"] = "ok"
		if a.b.Redis.Ping(ctx) != nil {
			checks["redis"] = "unavailable"
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, checks)
}
