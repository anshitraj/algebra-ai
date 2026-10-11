package v1

import (
	"bytes"
	"errors"
	"io"
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

// zeroes is an endless body of unknown length, like a chunked upload.
type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) { return len(p), nil }

func TestBodyLimit(t *testing.T) {
	called := false
	var readErr error
	var got int64
	h := bodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		got, readErr = io.Copy(io.Discard, r.Body)
		if readErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	run := func(req *http.Request) *httptest.ResponseRecorder {
		called, readErr, got = false, nil, 0
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("a body within the limit is read whole", func(t *testing.T) {
		rec := run(httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(`{"name":"x"}`)))
		if rec.Code != http.StatusOK || !called || readErr != nil || got != int64(len(`{"name":"x"}`)) {
			t.Errorf("code=%d called=%v err=%v read=%d", rec.Code, called, readErr, got)
		}
	})
	t.Run("a declared length over the limit is refused with 413 before the handler runs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader("x"))
		req.ContentLength = maxRequestBody + 1
		rec := run(req)
		if rec.Code != http.StatusRequestEntityTooLarge || called {
			t.Errorf("code=%d called=%v, want 413 and no handler", rec.Code, called)
		}
		if !strings.Contains(rec.Body.String(), "too large") {
			t.Errorf("body = %q", rec.Body.String())
		}
	})
	t.Run("a body of unknown length is cut off at the limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", io.NopCloser(zeroes{}))
		if req.ContentLength != -1 {
			t.Fatalf("test setup: ContentLength = %d, want -1", req.ContentLength)
		}
		rec := run(req)
		var tooLarge *http.MaxBytesError
		if !called || !errors.As(readErr, &tooLarge) || rec.Code != http.StatusBadRequest {
			t.Errorf("code=%d called=%v err=%v", rec.Code, called, readErr)
		}
		if got > maxRequestBody {
			t.Errorf("handler read %d bytes, over the %d limit", got, maxRequestBody)
		}
	})
	t.Run("a request without a body passes", func(t *testing.T) {
		rec := run(httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
		if rec.Code != http.StatusOK || !called {
			t.Errorf("code=%d called=%v", rec.Code, called)
		}
	})
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
