package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// browserCall is a request the way the web app makes it: cookie, no token.
func browserCall(t *testing.T, method, url string, cookie *http.Cookie, body any) (int, *http.Cookie, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "algebra_session" && c.Value != "" {
			session = c
		}
	}
	return resp.StatusCode, session, out
}

func userOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	u, _ := out["user"].(map[string]any)
	if u == nil {
		t.Fatalf("no user in %v", out)
	}
	return u
}
