package v1

import (
	"errors"
	"net/http"
)

type createWebhookEndpointRequest struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types,omitempty"` // empty = every event type
}

type createWebhookEndpointResponse struct {
	WebhookEndpointID string `json:"webhook_endpoint_id"`
	Secret            string `json:"secret"` // shown exactly once
}

// createWebhookEndpoint is POST /api/v1/tenants/{id}/webhook-endpoints —
// the sending half of what internal/api/v1/webhooks.go only ever received.
func (a *API) createWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	t, err := a.resolveTenant(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := requireOwnTenant(t, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	var req createWebhookEndpointRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.URL == "" {
		writeError(w, errors.New("url is required"))
		return
	}

	ep, secret, err := a.b.WebhookDispatch.CreateEndpoint(r.Context(), t.ID, req.URL, req.EventTypes)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createWebhookEndpointResponse{WebhookEndpointID: ep.ID, Secret: secret})
}
