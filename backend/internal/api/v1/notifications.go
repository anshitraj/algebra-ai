package v1

import (
	"errors"
	"net/http"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
)

// Email notifications (internal/app/notification_service.go): a sign-in
// alert after each sign-in and the receipt of each payment that lands on a
// chain. Every route here is the person's own, by session: an agent token
// or an API key can't read or move where someone's mail goes.

// notifyUser is the signed-in person, when notifications are wired.
func (a *API) notifyUser(w http.ResponseWriter, r *http.Request) (*account.User, bool) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return nil, false
	}
	if a.b.Notifications == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "email notifications aren't available on this server"})
		return nil, false
	}
	u, err := a.b.Accounts.User(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return nil, false
	}
	return u, true
}

func (a *API) writeNotifications(w http.ResponseWriter, v *app.NotificationView, err error) {
	var limited *app.RateLimited
	if errors.As(err, &limited) {
		w.Header().Set("Retry-After", formatSeconds(limited.Retry))
		writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "A confirmation email was just sent. You can ask for another in a minute."})
		return
	}
	if err != nil {
		if errors.Is(err, app.ErrInvalidRequest) {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusBadGateway, errorBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// getMyNotifications: GET /api/v1/me/notifications.
func (a *API) getMyNotifications(w http.ResponseWriter, r *http.Request) {
	u, ok := a.notifyUser(w, r)
	if !ok {
		return
	}
	v, err := a.b.Notifications.View(r.Context(), u)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type notificationKindsRequest struct {
	SignIns  *bool `json:"sign_ins"`
	Receipts *bool `json:"receipts"`
}

// setMyNotificationKinds: PUT /api/v1/me/notifications turns each kind of
// email on or off; a kind left out stays as it is.
func (a *API) setMyNotificationKinds(w http.ResponseWriter, r *http.Request) {
	u, ok := a.notifyUser(w, r)
	if !ok {
		return
	}
	var req notificationKindsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	v, err := a.b.Notifications.SetKinds(r.Context(), u, req.SignIns, req.Receipts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type notificationEmailRequest struct {
	Email string `json:"email"`
}

// setMyNotificationEmail: POST /api/v1/me/notifications/email asks for
// notifications to go to an address. Only a confirmation link is sent there
// until the link is opened.
func (a *API) setMyNotificationEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.notifyUser(w, r)
	if !ok {
		return
	}
	var req notificationEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	v, err := a.b.Notifications.SetEmail(r.Context(), u, req.Email)
	a.writeNotifications(w, v, err)
}

type notificationConfirmRequest struct {
	Token string `json:"token"`
}

// confirmMyNotificationEmail: POST /api/v1/me/notifications/email/confirm
// with the token from the link, signed in as the account that asked.
func (a *API) confirmMyNotificationEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.notifyUser(w, r)
	if !ok {
		return
	}
	var req notificationConfirmRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	v, err := a.b.Notifications.ConfirmEmail(r.Context(), u, req.Token)
	a.writeNotifications(w, v, err)
}

// removeMyNotificationEmail: DELETE /api/v1/me/notifications/email drops the
// added address and any waiting to be confirmed.
func (a *API) removeMyNotificationEmail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.notifyUser(w, r)
	if !ok {
		return
	}
	v, err := a.b.Notifications.RemoveEmail(r.Context(), u)
	a.writeNotifications(w, v, err)
}

// signedIn tells the person by email that a session of theirs just started.
// It never holds up or fails the sign-in.
func (a *API) signedIn(r *http.Request, res *app.SignInResult) {
	if a.b.Notifications == nil || res == nil || res.User == nil {
		return
	}
	a.b.Notifications.SignedIn(r.Context(), res.User, clientMeta(r), res.Created)
}
