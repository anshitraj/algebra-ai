package v1

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/platform/identity"
)

// SessionCookie is the HttpOnly cookie carrying a human session. The web
// app proxies /api/v1 through its own origin, so this is a first-party
// cookie; SameSite=Lax keeps it off cross-site POSTs.
const SessionCookie = "algebra_session"

const oauthStateCookie = "algebra_oauth"

type sessionCtxKey struct{}

// sessionFromContext returns the session withSession attached, if any.
func sessionFromContext(ctx context.Context) *account.Session {
	s, _ := ctx.Value(sessionCtxKey{}).(*account.Session)
	return s
}

// withSession resolves the session cookie (when present) once per request
// and attaches it to the context. A bad or expired cookie is simply "no
// session" here — each handler decides whether that's an error.
func (a *API) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.b.Accounts == nil {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := a.b.Accounts.Authenticate(r.Context(), c.Value)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey{}, sess)))
	})
}

// csrfGuard rejects a cookie-authenticated, state-changing request whose
// Origin isn't one of ours. SameSite=Lax already stops cross-site POSTs
// carrying the cookie in current browsers; this is the second layer for
// older ones. Requests with no Origin (server-to-server, curl) carry no
// ambient browser credentials to abuse, so they pass.
func (a *API) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie(SessionCookie); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !a.originAllowed(origin) {
			writeJSON(w, http.StatusForbidden, errorBody{Error: "cross-origin request rejected"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) originAllowed(origin string) bool {
	if a.allowedOrigins[origin] {
		return true
	}
	return a.b.AuthConfig.PublicWebURL != "" && origin == a.b.AuthConfig.PublicWebURL
}

func (a *API) requireSession(w http.ResponseWriter, r *http.Request) (*account.Session, bool) {
	sess := sessionFromContext(r.Context())
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "sign in required"})
		return nil, false
	}
	return sess, true
}

func clientMeta(r *http.Request) account.ClientMeta {
	return account.ClientMeta{UserAgent: r.UserAgent(), IP: clientIP(r)}
}

// trustedProxyHops is how many X-Forwarded-For entries, counted from the
// right, were written by infrastructure we run (TRUSTED_PROXY_HOPS; set by
// NewRouter). The web app's /api/v1 rewrite passes the header through
// without adding to it, so this counts the load balancer(s) in front of the
// web app: 1 for one that appends the client address.
var trustedProxyHops = 1

// clientIP is the caller's address. RemoteAddr is our own proxy, so the
// answer is in X-Forwarded-For — but only its last trustedProxyHops entries
// were written by infrastructure we run. Anything left of them came from the
// client and can be forged, so it is never used: taking the first entry
// would let anyone dodge per-IP limits by sending a fake header.
func clientIP(r *http.Request) string {
	if hops := trustedProxyHops; hops > 0 {
		var parts []string
		for _, h := range r.Header.Values("X-Forwarded-For") {
			for _, p := range strings.Split(h, ",") {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, p)
				}
			}
		}
		if len(parts) >= hops {
			return parts[len(parts)-hops]
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return r.RemoteAddr
	}
	return host
}

func (a *API) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/",
		MaxAge:   int(a.b.Accounts.SessionTTL() / time.Second),
		HttpOnly: true, Secure: a.b.AuthConfig.CookieSecure(), SameSite: http.SameSiteLaxMode,
	})
}

func (a *API) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.b.AuthConfig.CookieSecure(), SameSite: http.SameSiteLaxMode,
	})
}

// --- responses ---

type userResponse struct {
	ID              string   `json:"id"`
	Email           string   `json:"email"`
	Name            string   `json:"name"`
	AvatarURL       string   `json:"avatar_url,omitempty"`
	EmailVerified   bool     `json:"email_verified"`
	Onboarded       bool     `json:"onboarded"`
	HasPassword     bool     `json:"has_password"`
	LinkedProviders []string `json:"linked_providers"`
	// Wallets are the Solana wallets the person signed in with or was
	// given at sign-in (Privy); public addresses only.
	Wallets   []account.Wallet `json:"wallets"`
	CreatedAt string           `json:"created_at"`
	// Mode is "live" or "demo" — see account.Mode.
	Mode string `json:"mode"`
}

func (a *API) toUserResponse(ctx context.Context, u *account.User) userResponse {
	linked, _ := a.b.Accounts.LinkedProviders(ctx, u.ID)
	if linked == nil {
		linked = []string{}
	}
	wallets, _ := a.b.Accounts.Wallets(ctx, u.ID)
	if wallets == nil {
		wallets = []account.Wallet{}
	}
	email := u.Email
	if app.IsWalletOnlyEmail(email) {
		email = "" // a placeholder, not an address anyone can write to
	}
	return userResponse{
		ID: u.ID, Email: email, Name: u.Name, AvatarURL: u.AvatarURL,
		EmailVerified: u.EmailVerifiedAt != nil, Onboarded: u.OnboardedAt != nil,
		HasPassword: u.HasPassword(), LinkedProviders: linked, Wallets: wallets,
		CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
		Mode:      string(modeOrLive(u.Mode)),
	}
}

func modeOrLive(m account.Mode) account.Mode {
	if m == account.ModeDemo {
		return m
	}
	return account.ModeLive
}

type sessionResponse struct {
	User *userResponse `json:"user"`
}

// --- handlers ---

// authProviders lists which sign-in methods are available, so the sign-in
// page only renders buttons that work.
func (a *API) authProviders(w http.ResponseWriter, _ *http.Request) {
	_, google := a.b.OAuthProviders["google"]
	_, github := a.b.OAuthProviders["github"]
	out := map[string]any{
		"password": a.b.AuthConfig.PasswordLogin, "google": google, "github": github,
		"demo": a.b.AuthConfig.DemoAccounts && a.b.Demo != nil, "privy": a.b.Privy != nil,
	}
	if a.b.Privy != nil {
		// Public: Privy's sign-in runs in the browser and needs the app ID.
		out["privy_app_id"] = a.b.Privy.AppID()
	}
	writeJSON(w, http.StatusOK, out)
}

type privySignInRequest struct {
	IdentityToken string `json:"identity_token"`
}

// privySignIn trades a Privy identity token for an Algebra session. The web
// app runs Privy's sign-in (email code, Google, or a Solana wallet; Privy
// gives anyone without a wallet an embedded Solana one), then posts the
// identity token Privy issued, which names the person and their wallets
// and is signed by Privy for this app.
func (a *API) privySignIn(w http.ResponseWriter, r *http.Request) {
	if a.b.Privy == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "Privy sign-in isn't configured on this server"})
		return
	}
	var req privySignInRequest
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.IdentityToken) == "" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "identity_token is required"})
		return
	}
	id, err := a.b.Privy.VerifyIdentityToken(r.Context(), req.IdentityToken)
	if err != nil {
		if errors.Is(err, identity.ErrPrivyToken) {
			log.Printf("auth: privy sign-in refused: %v", err)
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "We couldn't verify your Privy sign-in. Please try again."})
			return
		}
		log.Printf("auth: privy sign-in: %v", err)
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "Privy isn't answering right now. Please try again in a moment."})
		return
	}
	res, err := a.b.Accounts.SignInWithPrivy(r.Context(), id.Profile, id.Wallets, clientMeta(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	a.setSessionCookie(w, res.Token)
	resp := a.toUserResponse(r.Context(), res.User)
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, sessionResponse{User: &resp})
}

// passwordLoginOff answers every email + password endpoint once
// PASSWORD_LOGIN=off (real accounts sign in with Google/GitHub only).
func (a *API) passwordLoginOff(w http.ResponseWriter) bool {
	if a.b.AuthConfig.PasswordLogin {
		return false
	}
	writeJSON(w, http.StatusNotFound, errorBody{Error: "email and password sign-in is turned off — continue with Google or GitHub"})
	return true
}

// startDemo creates a fresh demo account and signs it in: real product
// listings, simulated checkout, fake money. No signup, no password.
func (a *API) startDemo(w http.ResponseWriter, r *http.Request) {
	if !a.b.AuthConfig.DemoAccounts || a.b.Demo == nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "the demo is turned off on this server"})
		return
	}
	// Demo accounts need no signup, so cap how many one address can open a
	// day — each one is real LLM and web-search spend.
	if n := a.b.AuthConfig.DemoAccountsPerIPPerDay; a.limiter != nil && n > 0 {
		ok, retry, err := a.limiter.Allow(r.Context(), "quota:demo-accounts:"+clientMeta(r).IP, n, 24*time.Hour)
		if err == nil && !ok {
			w.Header().Set("Retry-After", formatSeconds(retry))
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "you've started several demos today — try again tomorrow, or create an account"})
			return
		}
	}
	res, err := a.b.Demo.Start(r.Context(), clientMeta(r))
	if err != nil {
		writeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: res.Token, Path: "/",
		MaxAge:   int(app.DemoSessionTTL / time.Second),
		HttpOnly: true, Secure: a.b.AuthConfig.CookieSecure(), SameSite: http.SameSiteLaxMode,
	})
	resp := a.toUserResponse(r.Context(), res.User)
	writeJSON(w, http.StatusCreated, sessionResponse{User: &resp})
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	if sess == nil {
		writeJSON(w, http.StatusOK, sessionResponse{User: nil})
		return
	}
	u, err := a.b.Accounts.User(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	resp := a.toUserResponse(r.Context(), u)
	writeJSON(w, http.StatusOK, sessionResponse{User: &resp})
}

type signUpRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) signUp(w http.ResponseWriter, r *http.Request) {
	if a.passwordLoginOff(w) {
		return
	}
	var req signUpRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	res, err := a.b.Accounts.SignUp(r.Context(), req.Name, req.Email, req.Password, clientMeta(r))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, account.ErrEmailTaken) {
			status = http.StatusConflict
		}
		writeJSON(w, status, errorBody{Error: err.Error()})
		return
	}
	a.setSessionCookie(w, res.Token)
	resp := a.toUserResponse(r.Context(), res.User)
	writeJSON(w, http.StatusCreated, sessionResponse{User: &resp})
}

type signInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) signIn(w http.ResponseWriter, r *http.Request) {
	if a.passwordLoginOff(w) {
		return
	}
	var req signInRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	res, err := a.b.Accounts.SignIn(r.Context(), req.Email, req.Password, clientMeta(r))
	if err != nil {
		if errors.Is(err, account.ErrInvalidCredentials) {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: err.Error()})
			return
		}
		writeError(w, err)
		return
	}
	a.setSessionCookie(w, res.Token)
	resp := a.toUserResponse(r.Context(), res.User)
	writeJSON(w, http.StatusOK, sessionResponse{User: &resp})
}

func (a *API) signOut(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFromContext(r.Context()); sess != nil {
		if err := a.b.Accounts.SignOut(r.Context(), sess); err != nil {
			writeError(w, err)
			return
		}
	}
	a.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

func (a *API) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if a.passwordLoginOff(w) {
		return
	}
	var req forgotPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	if err := a.b.Accounts.RequestPasswordReset(r.Context(), req.Email, a.b.AuthConfig.PublicWebURL); err != nil {
		writeError(w, err)
		return
	}
	// Same answer whether or not the email has an account.
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	if a.passwordLoginOff(w) {
		return
	}
	var req resetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid request body"})
		return
	}
	if err := a.b.Accounts.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
		return
	}
	a.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, okResponse{OK: true})
}

// agentToken hands the session's console-agent bearer token to the web
// app's server-side agent loop, so every LLM tool call authenticates as an
// agent — which can shop within policy but can never approve — rather than
// as the human session. Session-only: an agent token can't fetch it.
func (a *API) agentToken(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.requireSession(w, r)
	if !ok {
		return
	}
	token, err := a.b.Accounts.AgentToken(sess)
	if err != nil {
		writeError(w, err)
		return
	}
	mode, _ := a.b.Accounts.UserMode(r.Context(), sess.UserID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"agent_id": sess.AgentID, "token": token, "mode": string(modeOrLive(mode))})
}

// --- OAuth ---

func (a *API) oauthRedirectURL(provider string) string {
	return a.b.AuthConfig.PublicWebURL + "/api/v1/auth/oauth/" + provider + "/callback"
}

// signState/verifyState protect the state+verifier cookie with an HMAC so
// it can't be forged or swapped between providers.
func (a *API) signState(payload string) string {
	mac := hmac.New(sha256.New, a.b.OAuthStateKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *API) verifyState(v string) (string, bool) {
	dot := strings.LastIndexByte(v, '.')
	if dot < 0 {
		return "", false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(v[:dot])
	sig, err2 := base64.RawURLEncoding.DecodeString(v[dot+1:])
	if err1 != nil || err2 != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, a.b.OAuthStateKey)
	mac.Write(payload)
	return string(payload), hmac.Equal(sig, mac.Sum(nil))
}

// safeNext only allows same-site relative paths as a post-login
// destination — never an open redirect.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return ""
	}
	return next
}

func (a *API) oauthStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p, ok := a.b.OAuthProviders[name]
	if !ok {
		http.Redirect(w, r, "/login?error="+url.QueryEscape(name+" sign-in isn't configured on this server"), http.StatusFound)
		return
	}
	state := oauth2.GenerateVerifier() // 43 chars of CSPRNG output — reused as an opaque state value
	verifier := oauth2.GenerateVerifier()
	next := safeNext(r.URL.Query().Get("next"))
	payload := strings.Join([]string{name, state, verifier, strconv.FormatInt(time.Now().Unix(), 10), next}, "|")
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: a.signState(payload), Path: "/api/v1/auth/oauth",
		MaxAge: 600, HttpOnly: true, Secure: a.b.AuthConfig.CookieSecure(), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, p.AuthCodeURL(state, verifier, a.oauthRedirectURL(name)), http.StatusFound)
}

func (a *API) oauthCallback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	fail := func(msg string) {
		http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusFound)
	}
	p, ok := a.b.OAuthProviders[name]
	if !ok {
		fail(name + " sign-in isn't configured on this server")
		return
	}
	// Clear the one-time state cookie whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/api/v1/auth/oauth", MaxAge: -1,
		HttpOnly: true, Secure: a.b.AuthConfig.CookieSecure(), SameSite: http.SameSiteLaxMode})

	if e := r.URL.Query().Get("error"); e != "" {
		if e == "access_denied" {
			fail("Sign-in was cancelled.")
			return
		}
		fail("The provider returned an error: " + e)
		return
	}
	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		fail("Your sign-in attempt expired. Please try again.")
		return
	}
	payload, valid := a.verifyState(c.Value)
	parts := strings.SplitN(payload, "|", 5)
	if !valid || len(parts) != 5 || parts[0] != name {
		fail("Your sign-in attempt couldn't be verified. Please try again.")
		return
	}
	issued, _ := strconv.ParseInt(parts[3], 10, 64)
	if time.Since(time.Unix(issued, 0)) > 10*time.Minute {
		fail("Your sign-in attempt expired. Please try again.")
		return
	}
	if !hmac.Equal([]byte(r.URL.Query().Get("state")), []byte(parts[1])) {
		fail("Your sign-in attempt couldn't be verified. Please try again.")
		return
	}
	profile, err := p.Exchange(r.Context(), r.URL.Query().Get("code"), parts[2], a.oauthRedirectURL(name))
	if err != nil {
		fail("We couldn't complete sign-in with " + name + ". Please try again.")
		return
	}
	res, err := a.b.Accounts.SignInWithOAuth(r.Context(), profile, clientMeta(r))
	if err != nil {
		fail(err.Error())
		return
	}
	a.setSessionCookie(w, res.Token)
	dest := "/console"
	if res.User.OnboardedAt == nil {
		dest = "/onboarding"
	} else if next := safeNext(parts[4]); next != "" {
		dest = next
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// sessionUserID is the human identity for session-only endpoints.
func (a *API) sessionUserID(r *http.Request) (string, bool) {
	if sess := sessionFromContext(r.Context()); sess != nil {
		return sess.UserID, true
	}
	return "", false
}
