package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/intel"
	"github.com/project-algebra/algebra/providers/catalog"
)

// --- Provider Intelligence: the endpoint registry ---
//
//	GET  /intel/status                 the refresh job, recent runs, each catalog's freshness, counts
//	GET  /intel/endpoints              the registry: source, network, protocol, trust, level, capability, q, sort, limit, offset
//	GET  /intel/endpoints/{id}         one endpoint, what is known about it, and its history
//	GET  /intel/changes                what changed, newest first: kind, suspicious, endpoint, limit
//	POST /intel/refresh                read every catalog now
//	PUT  /intel/endpoints/{id}/trust   {"trust","note"}: a person's decision about an endpoint
//
// Reading is open, like the catalogs it is built from. Refreshing and trust
// decisions need a signed-in person (never an agent token or API key), and in
// production the operator token as well.

func (a *API) intelService(w http.ResponseWriter) (*app.IntelService, bool) {
	if a.b.Intel == nil {
		writeJSON(w, http.StatusNotImplemented, errorBody{Error: "provider intelligence isn't running on this server"})
		return nil, false
	}
	return a.b.Intel, true
}

// intelAdmin returns the person deciding, or answers the request itself.
func (a *API) intelAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Header.Get("Authorization") != "" {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "trust decisions are a person's; sign in to the console"})
		return "", false
	}
	sess, ok := a.requireSession(w, r)
	if !ok {
		return "", false
	}
	if err := a.requireOperator(r); err != nil {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "only the operator can change the provider registry here (" + operatorHeader + ")"})
		return "", false
	}
	return sess.UserID, true
}

// canManageIntel reports, without answering, whether this caller could
// refresh or set trust.
func (a *API) canManageIntel(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" || sessionFromContext(r.Context()) == nil {
		return false
	}
	return a.requireOperator(r) == nil
}

type intelEndpointView struct {
	intel.Endpoint
	Levels intel.Levels `json:"levels"`
}

func viewsOf(eps []intel.Endpoint) []intelEndpointView {
	out := make([]intelEndpointView, len(eps))
	for i, e := range eps {
		out[i] = intelEndpointView{Endpoint: e, Levels: e.Levels()}
	}
	return out
}

func (a *API) intelStatus(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.intelService(w)
	if !ok {
		return
	}
	st, err := svc.Status(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "can_manage": a.canManageIntel(r)})
}

func (a *API) listIntelEndpoints(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.intelService(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := app.IntelFilter{
		Source: q.Get("source"), Network: q.Get("network"), Protocol: q.Get("protocol"), Trust: q.Get("trust"),
		Level: q.Get("level"), Capability: strings.TrimSpace(q.Get("capability")), Query: strings.TrimSpace(q.Get("q")), Sort: q.Get("sort"),
	}
	if f.Trust != "" && !intel.Trust(f.Trust).Valid() {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "trust must be verified, unverified or blocked"})
		return
	}
	switch f.Level {
	case "", "listed", "quoted", "tested", "verified", "executable", "new", "removed":
	default:
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "level must be listed, quoted, tested, verified, executable, new or removed"})
		return
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	eps, total, err := svc.Endpoints(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": viewsOf(eps), "total": total, "providers": a.providerFaces(r.Context(), eps)})
}

// intelProvider is how its catalog presents an endpoint's provider: name, a
// line about it, its own site and its logo. Third-party text, shown as text.
type intelProvider struct {
	FQN         string `json:"fqn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	LogoURL     string `json:"logo_url,omitempty"`
}

// providerFaces finds the listed endpoints' providers in the catalogs, by
// provider ID. Best effort: a catalog that can't be read in time leaves its
// providers out, and the registry still answers.
func (a *API) providerFaces(ctx context.Context, eps []intel.Endpoint) map[string]intelProvider {
	out := map[string]intelProvider{}
	if a.b.Directory == nil || len(eps) == 0 {
		return out
	}
	want := make(map[string]bool, len(eps))
	for _, e := range eps {
		want[e.ProviderID] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	l, err := a.b.Directory.List(ctx, catalog.Filter{})
	if err != nil {
		return out
	}
	for _, p := range l.Providers {
		if want[p.ID] {
			out[p.ID] = intelProvider{FQN: p.FQN, Name: p.Name, Description: p.Description, Website: p.Website, LogoURL: p.LogoURL}
		}
	}
	return out
}

func (a *API) getIntelEndpoint(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.intelService(w)
	if !ok {
		return
	}
	d, err := svc.Endpoint(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) listIntelChanges(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.intelService(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	changes, err := svc.Changes(r.Context(), app.ChangeFilter{
		EndpointID: q.Get("endpoint"), Kind: q.Get("kind"), Suspicious: q.Get("suspicious") == "true", Limit: limit,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

// refreshIntel starts a refresh in the background: reading every catalog
// takes a minute or more. The status shows it running and how it ended.
func (a *API) refreshIntel(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.intelService(w)
	if !ok {
		return
	}
	userID, ok := a.intelAdmin(w, r)
	if !ok {
		return
	}
	st, err := svc.Status(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if j := st.Job; j != nil && j.LeaseOwner != "" && j.LeaseUntil != nil && j.LeaseUntil.After(time.Now()) {
		writeJSON(w, http.StatusConflict, errorBody{Error: "a refresh is already running"})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
		defer cancel()
		if _, err := svc.RunDue(ctx, "manual:"+userID, true); err != nil && !errors.Is(err, app.ErrNotDue) {
			slog.Warn("provider intelligence: manual refresh failed", "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (a *API) setIntelTrust(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.intelService(w)
	if !ok {
		return
	}
	userID, ok := a.intelAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Trust string `json:"trust"`
		Note  string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `send {"trust":"blocked","note":"why"} (verified, unverified or blocked)`})
		return
	}
	e, err := svc.SetTrust(r.Context(), r.PathValue("id"), intel.Trust(req.Trust), req.Note, userID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intelEndpointView{Endpoint: *e, Levels: e.Levels()})
}
