package e2e

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func getRaw(t *testing.T, url, token string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// A committed request's receipt as a PDF: for its own person and keys only,
// naming what was paid, to whom, on which network, and carrying the signed
// receipt so it can be verified again.
func TestReceiptPDFOverHTTP(t *testing.T) {
	_, base := serve(t)
	alice := signUp(t, base, "Alice")
	_, project := workspace(t, base, alice)
	projectPass(t, base, alice, project, "sandbox")
	secret, _ := issueKey(t, base, alice, project, map[string]any{"name": "ci", "environment": "sandbox"})

	status, _, out := call(t, "POST", base+"/api/v1/execute", secret, priceRequest("pdf-"+uuid.NewString()))
	if status != 200 || out["delivered"] != true {
		t.Fatalf("execute: %d %v", status, out)
	}
	intent := out["intent"].(map[string]any)["id"].(string)

	status, h, body := getRaw(t, base+"/api/v1/economic-intents/"+intent+"/receipt.pdf", secret)
	if status != 200 || h.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("pdf: %d %s %q", status, h.Get("Content-Type"), body[:min(len(body), 20)])
	}
	for _, want := range []string{intent, "Algebra sandbox", "Valid", "token.price"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("the PDF should say %q", want)
		}
	}
	if !strings.Contains(h.Get("Content-Disposition"), intent) {
		t.Errorf("file name: %q", h.Get("Content-Disposition"))
	}

	// Nobody else's.
	bob := signUp(t, base, "Bob")
	_, bobProject := workspace(t, base, bob)
	projectPass(t, base, bob, bobProject, "sandbox")
	bobKey, _ := issueKey(t, base, bob, bobProject, map[string]any{"name": "ci", "environment": "sandbox"})
	if status, _, _ := getRaw(t, base+"/api/v1/economic-intents/"+intent+"/receipt.pdf", bobKey); status != 404 {
		t.Fatalf("another person's receipt must be not found: %d", status)
	}
	if status, _, _ := getRaw(t, base+"/api/v1/economic-intents/"+intent+"/receipt.pdf", ""); status != 401 && status != 404 {
		t.Fatalf("no credentials, no receipt: %d", status)
	}
}
