package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-algebra/algebra/internal/domain/tenant"
	"github.com/project-algebra/algebra/internal/platform/safehttp"
)

type fakeEndpointStore struct {
	mu        sync.Mutex
	endpoints []tenant.WebhookEndpoint
}

func (f *fakeEndpointStore) Create(_ context.Context, w *tenant.WebhookEndpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints = append(f.endpoints, *w)
	return nil
}

func (f *fakeEndpointStore) ListActiveByTenant(_ context.Context, tenantID string) ([]tenant.WebhookEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tenant.WebhookEndpoint
	for _, e := range f.endpoints {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	return out, nil
}

// A webhook URL is chosen by a tenant, and Algebra then POSTs to it from
// inside its own network: only public https destinations may be registered.
func TestValidateWebhookURL(t *testing.T) {
	for _, ok := range []string{
		"https://hooks.example.com/algebra",
		"https://hooks.example.com:8443/a/b?x=1",
		"https://8.8.8.8/hook",
		"https://[2606:4700:4700::1111]/hook",
	} {
		if err := ValidateWebhookURL(ok); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"hooks.example.com/algebra",
		"//hooks.example.com/algebra",
		"http://hooks.example.com/algebra",
		"ftp://hooks.example.com/algebra",
		"file:///etc/passwd",
		"https:///path-without-host",
		"https://user:secret@hooks.example.com/algebra",
		"https://localhost/hook",
		"https://app.localhost/hook",
		"https://127.0.0.1/hook",
		"https://127.1.2.3:8443/hook",
		"https://[::1]/hook",
		"https://[::ffff:127.0.0.1]/hook",
		"https://0.0.0.0/hook",
		"https://10.1.2.3/hook",
		"https://172.16.0.9/hook",
		"https://192.168.0.10/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://[fe80::1%25eth0]/hook",
		"https://[fd00::1]/hook",
		"https://100.100.100.200/latest/meta-data/", // carrier-grade NAT range, Alibaba Cloud's metadata address
		"https://metadata.google.internal/computeMetadata/v1/",
		"https://printer.local/hook",
		"https://redis/hook", // a bare service name resolves inside the cluster
		"https://2130706433/hook",
		"https://hooks.example.com:0/hook",
		"https://hooks.example.com:99999/hook",
		"https://exa mple.com/hook",
		"https://hooks.example.com/" + strings.Repeat("a", 3000),
	} {
		err := ValidateWebhookURL(bad)
		if !errors.Is(err, ErrInvalidWebhookURL) {
			t.Errorf("%.80q: got %v, want ErrInvalidWebhookURL", bad, err)
		}
	}
}

func TestCreateEndpointRefusesAnInternalDestination(t *testing.T) {
	store := &fakeEndpointStore{}
	svc := NewWebhookDispatchService(store)
	for _, bad := range []string{"http://169.254.169.254/latest/meta-data/", "https://127.0.0.1:6379/", "http://hooks.example.com/x"} {
		if _, secret, err := svc.CreateEndpoint(context.Background(), "tnt_1", bad, nil); !errors.Is(err, ErrInvalidWebhookURL) || secret != "" {
			t.Errorf("%s: err=%v secret=%q, want ErrInvalidWebhookURL and no secret issued", bad, err, secret)
		}
	}
	if len(store.endpoints) != 0 {
		t.Fatalf("a refused endpoint was stored: %+v", store.endpoints)
	}
	ep, secret, err := svc.CreateEndpoint(context.Background(), "tnt_1", "https://hooks.example.com/algebra", nil)
	if err != nil || ep == nil || !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("a public https endpoint should register: %v %v %q", ep, err, secret)
	}
}

// Rows registered before the check existed may still hold an internal URL; the
// delivery client itself must refuse to connect to one, and must not follow a
// redirect from a public endpoint to somewhere internal.
func TestDeliveryNeverReachesAnInternalAddress(t *testing.T) {
	var hits atomic.Int64
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	svc := NewWebhookDispatchService(&fakeEndpointStore{})
	ep := tenant.WebhookEndpoint{ID: "whep_1", TenantID: "tnt_1", URL: internal.URL, Secret: "whsec_x"}
	if svc.deliverOnce(context.Background(), ep, "evt_1", "payment_intent.created", []byte(`{}`)) {
		t.Error("a delivery to a loopback address reported success")
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the loopback server was reached %d times", n)
	}
}

// A delivery is signed with the endpoint's secret, carries the event's identity
// and counts as delivered only on a 2xx.
func TestDeliverySignsAndReportsStatus(t *testing.T) {
	var gotSig, gotID, gotType, gotBody string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig, gotID, gotType = r.Header.Get("X-Algebra-Signature"), r.Header.Get("X-Algebra-Event-ID"), r.Header.Get("X-Algebra-Event-Type")
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(status)
	}))
	defer srv.Close()

	svc := NewWebhookDispatchService(&fakeEndpointStore{})
	// Tests reach a loopback server over plain http; production never gets this client.
	svc.SetHTTPClient(safehttp.New(safehttp.Options{AllowLoopback: true, Timeout: 5 * time.Second}))

	ep := tenant.WebhookEndpoint{ID: "whep_1", TenantID: "tnt_1", URL: srv.URL, Secret: "whsec_x"}
	body := []byte(`{"event_type":"payment_intent.created"}`)
	if !svc.deliverOnce(context.Background(), ep, "evt_9", "payment_intent.created", body) {
		t.Fatal("a 200 should count as delivered")
	}
	if !VerifyHMACSignature(body, gotSig, "whsec_x") {
		t.Errorf("signature %q doesn't verify", gotSig)
	}
	if gotID != "evt_9" || gotType != "payment_intent.created" || gotBody != string(body) {
		t.Errorf("event headers/body wrong: %q %q %q", gotID, gotType, gotBody)
	}
	status = http.StatusInternalServerError
	if svc.deliverOnce(context.Background(), ep, "evt_9", "payment_intent.created", body) {
		t.Error("a 500 must not count as delivered")
	}
}
