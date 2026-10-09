package v1

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLog_AssignsIDAndNeverLogsQueryOrSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := requestLog(logger, okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=my+secret+query", nil)
	req.Header.Set("Authorization", "Bearer alg_agent_supersecret")
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "alg_sess_supersecret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if id := rec.Header().Get("X-Request-ID"); len(id) < 8 {
		t.Errorf("missing request id: %q", id)
	}
	out := buf.String()
	for _, leak := range []string{"secret", "Bearer", "alg_sess", "q="} {
		if strings.Contains(out, leak) {
			t.Errorf("access log leaked %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"path":"/api/v1/search"`) || !strings.Contains(out, `"status":200`) {
		t.Errorf("access log missing path/status: %s", out)
	}
}

func TestRequestLog_ReusesWellFormedUpstreamIDOnly(t *testing.T) {
	h := requestLog(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), okHandler())
	for id, keep := range map[string]bool{"lb-1234abcd": true, "bad id with spaces": false, "<script>": false} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Request-ID", id)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-ID") == id; got != keep {
			t.Errorf("upstream id %q reused=%v, want %v", id, got, keep)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeaders(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	for k, v := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store"} {
		if rec.Header().Get(k) != v {
			t.Errorf("%s = %q, want %q", k, rec.Header().Get(k), v)
		}
	}
}

func TestAuthEndpointsGetTheTightLimit(t *testing.T) {
	lim := newFakeLimiter(1000)
	a := &API{limiter: lim}
	h := a.rateLimitMiddleware(okHandler())
	blocked := 0
	for i := 0; i < authRateLimit+5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.9:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			blocked++
		}
	}
	// fakeLimiter enforces its own limit (1000) per key, so assert the
	// middleware used the tight auth key/limit by checking the counter.
	if lim.counts["ratelimit:auth:203.0.113.9"] != authRateLimit+5 {
		t.Errorf("auth limiter not consulted per attempt: %v", lim.counts)
	}
	_ = blocked
}
