package v1

import (
	"net/http"

	"github.com/project-algebra/algebra/internal/domain/spendpass"
)

// --- The kill switch and pass controls: the person's side (session only) ---
//
//	GET  /me/killswitch                  is it on, and for how many passes
//	POST /me/killswitch {"engaged":true}  freeze (or thaw) every pass at once
//	POST /me/passes/{id}/freeze {"frozen":true}
//	PUT  /me/passes/{id}/controls        calls per minute, new-provider rule
//
// Freezing takes effect on the next check, including the one made right before
// a payment is released, so an attempt already running pays nothing.

func (a *API) getKillSwitch(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	frozen, live, err := a.b.SpendPasses.KillSwitchState(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"engaged": live > 0 && frozen == live, "frozen_passes": frozen, "live_passes": live})
}

func (a *API) setKillSwitch(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Engaged *bool `json:"engaged"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Engaged == nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `send {"engaged": true} to freeze every pass, or false to lift it`})
		return
	}
	changed, err := a.b.SpendPasses.KillSwitch(r.Context(), sess.UserID, *req.Engaged)
	if err != nil {
		writeError(w, err)
		return
	}
	frozen, live, err := a.b.SpendPasses.KillSwitchState(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"engaged": live > 0 && frozen == live, "changed": changed, "frozen_passes": frozen, "live_passes": live,
	})
}

func (a *API) freezeMyPass(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Frozen *bool `json:"frozen"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Frozen == nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: `send {"frozen": true} or {"frozen": false}`})
		return
	}
	p, err := a.b.SpendPasses.Freeze(r.Context(), sess.UserID, r.PathValue("id"), *req.Frozen)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) setMyPassControls(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	var c spendpass.Controls
	if err := decodeJSON(r, &c); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	p, err := a.b.SpendPasses.UpdateControls(r.Context(), sess.UserID, r.PathValue("id"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
