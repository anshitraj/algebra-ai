package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	agentpkg "github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"github.com/project-algebra/algebra/policy"
)

// AccountStore persists human accounts, linked OAuth identities, sessions,
// and password-reset tokens. See migrations/0008_accounts.sql.
type AccountStore interface {
	CreateUser(ctx context.Context, u *account.User) error // shared.ErrConflict on duplicate email
	GetUser(ctx context.Context, id string) (*account.User, error)
	// GetUserByEmail finds a first-party (non-tenant) account only.
	GetUserByEmail(ctx context.Context, email string) (*account.User, error)
	UpdateUserProfile(ctx context.Context, id, name, avatarURL string) error
	SetPassword(ctx context.Context, userID, hash string) error
	MarkEmailVerified(ctx context.Context, userID string, at time.Time) error
	MarkOnboarded(ctx context.Context, userID string, at time.Time) error

	GetOAuthIdentity(ctx context.Context, provider, providerUserID string) (userID string, err error)
	LinkOAuthIdentity(ctx context.Context, provider, providerUserID, userID, email string, at time.Time) error
	ListOAuthProviders(ctx context.Context, userID string) ([]string, error)

	CreateSession(ctx context.Context, s *account.Session) error
	GetSessionByTokenHash(ctx context.Context, hash string) (*account.Session, error)
	TouchSession(ctx context.Context, id string, at time.Time) error
	RevokeSession(ctx context.Context, id string, at time.Time) error
	ListActiveSessions(ctx context.Context, userID string, now time.Time) ([]account.Session, error)

	// PurgeExpired deletes sessions dead for over a week and reset tokens
	// that are used or expired, returning how many rows went.
	PurgeExpired(ctx context.Context, now time.Time) (int64, error)

	CreateResetToken(ctx context.Context, hash, userID string, expiresAt time.Time) error
	// ConsumeResetToken atomically marks an unexpired, unused token used
	// and returns its user — a second call with the same token fails.
	ConsumeResetToken(ctx context.Context, hash string, now time.Time) (userID string, err error)

	// EraseUser scrubs the user's personal data and revokes every way to
	// act as them; financial records stay, keyed by the anonymous ID.
	EraseUser(ctx context.Context, userID string, at time.Time) error
}

// GuardrailStore persists a user's own account.Guardrails.
type GuardrailStore interface {
	Get(ctx context.Context, userID string) (*account.Guardrails, error) // shared.ErrNotFound if unset
	Upsert(ctx context.Context, userID string, g account.Guardrails, at time.Time) error
}

// TokenSealer is the envelope-encryption surface used to keep the console
// agent's bearer token at rest. privacy.AESGCMEncryptor satisfies it.
type TokenSealer interface {
	Encrypt(plaintext, aad []byte) (ciphertext, nonce []byte, err error)
	Decrypt(ciphertext, nonce, aad []byte) ([]byte, error)
}

// Mailer sends transactional email (password reset). The wiring layer picks
// a real provider when configured and a log-only mailer otherwise.
type Mailer interface {
	Send(ctx context.Context, to, subject, text string) error
}

// ConsoleAgentPermissions is what the per-session console agent may do —
// everything a shopping agent needs, and nothing human-only (approving,
// adding payment sources, editing addresses are session-only endpoints an
// agent token can never reach).
var ConsoleAgentPermissions = []agentpkg.Permission{
	agentpkg.PermShoppingRead, agentpkg.PermShoppingCreateIntent, agentpkg.PermShoppingExecute,
	agentpkg.PermPaymentsRequest, agentpkg.PermOrdersRead, agentpkg.PermProfilesRead,
	agentpkg.PermProfilesWrite, agentpkg.PermPolicyRead,
}

// ConsoleAgentClientID marks agents minted for a browser session, so they
// can be told apart from agents a user created for an external MCP client.
const ConsoleAgentClientID = "algebra-console"

// SignInResult is a freshly created session. Token is the raw cookie value —
// returned exactly once, never persisted.
type SignInResult struct {
	User    *account.User
	Session *account.Session
	Token   string
	Created bool // true when this sign-in created the account
}

// AccountService owns human identity: sign up, sign in (password or
// OAuth), sessions, password reset, and a user's guardrails.
type AccountService struct {
	store      AccountStore
	agents     *AgentService
	agentStore AgentStore
	sealer     TokenSealer
	mailer     Mailer
	guardrails GuardrailStore
	wallets    UserWalletStore
	now        func() time.Time
	sessionTTL time.Duration
	resetTTL   time.Duration
}

func NewAccountService(store AccountStore, agents *AgentService, agentStore AgentStore, sealer TokenSealer, mailer Mailer, guardrails GuardrailStore, sessionTTL time.Duration) *AccountService {
	if sessionTTL <= 0 {
		sessionTTL = 30 * 24 * time.Hour
	}
	return &AccountService{
		store: store, agents: agents, agentStore: agentStore, sealer: sealer, mailer: mailer,
		guardrails: guardrails, now: time.Now, sessionTTL: sessionTTL, resetTTL: 30 * time.Minute,
	}
}

// SessionTTL is how long a new session lives — the transport uses it for
// the cookie's Max-Age.
func (s *AccountService) SessionTTL() time.Duration { return s.sessionTTL }

// SignUp creates a password account and signs it in.
func (s *AccountService) SignUp(ctx context.Context, name, email, password string, meta account.ClientMeta) (*SignInResult, error) {
	email, err := account.NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if err := account.ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := account.HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &account.User{
		ID: newID("user"), Email: email, Name: strings.TrimSpace(name),
		PasswordHash: hash, CreatedAt: s.now(),
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, shared.ErrConflict) {
			return nil, account.ErrEmailTaken
		}
		return nil, err
	}
	res, err := s.startSession(ctx, u, meta)
	if err != nil {
		return nil, err
	}
	res.Created = true
	return res, nil
}

// SignIn verifies an email + password. Every failure is the same
// ErrInvalidCredentials and takes the same time.
func (s *AccountService) SignIn(ctx context.Context, email, password string, meta account.ClientMeta) (*SignInResult, error) {
	email, err := account.NormalizeEmail(email)
	if err != nil {
		account.VerifyPasswordOrDummy("", password)
		return nil, account.ErrInvalidCredentials
	}
	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			account.VerifyPasswordOrDummy("", password)
			return nil, account.ErrInvalidCredentials
		}
		return nil, err
	}
	if !account.VerifyPasswordOrDummy(u.PasswordHash, password) {
		return nil, account.ErrInvalidCredentials
	}
	return s.startSession(ctx, u, meta)
}

// SignInWithOAuth signs in (or up) with a provider-verified identity.
// Linking to an existing password account by email happens only when the
// provider says the email is verified — otherwise anyone could create a
// GitHub account with a victim's address and take over their Algebra
// account.
func (s *AccountService) SignInWithOAuth(ctx context.Context, p account.OAuthProfile, meta account.ClientMeta) (*SignInResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	email, err := account.NormalizeEmail(p.Email)
	if err != nil {
		return nil, err
	}
	now := s.now()

	if userID, err := s.store.GetOAuthIdentity(ctx, p.Provider, p.ProviderUserID); err == nil {
		u, err := s.store.GetUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		s.fillProfile(ctx, u, p)
		return s.startSession(ctx, u, meta)
	} else if !errors.Is(err, shared.ErrNotFound) {
		return nil, err
	}

	created := false
	u, err := s.store.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		// Existing account, same verified email — link this provider to it.
	case errors.Is(err, shared.ErrNotFound):
		u = &account.User{
			ID: newID("user"), Email: email, Name: strings.TrimSpace(p.Name),
			AvatarURL: p.AvatarURL, EmailVerifiedAt: &now, CreatedAt: now,
		}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return nil, err
		}
		created = true
	default:
		return nil, err
	}
	if err := s.store.LinkOAuthIdentity(ctx, p.Provider, p.ProviderUserID, u.ID, email, now); err != nil {
		return nil, err
	}
	if u.EmailVerifiedAt == nil {
		_ = s.store.MarkEmailVerified(ctx, u.ID, now)
		u.EmailVerifiedAt = &now
	}
	s.fillProfile(ctx, u, p)
	res, err := s.startSession(ctx, u, meta)
	if err != nil {
		return nil, err
	}
	res.Created = created
	return res, nil
}

// fillProfile backfills a missing name/avatar from the provider — never
// overwrites what the user set themselves.
func (s *AccountService) fillProfile(ctx context.Context, u *account.User, p account.OAuthProfile) {
	name, avatar := u.Name, u.AvatarURL
	if name == "" {
		name = strings.TrimSpace(p.Name)
	}
	if avatar == "" {
		avatar = p.AvatarURL
	}
	if name != u.Name || avatar != u.AvatarURL {
		if err := s.store.UpdateUserProfile(ctx, u.ID, name, avatar); err == nil {
			u.Name, u.AvatarURL = name, avatar
		}
	}
}

// DemoSessionTTL is how long a demo account's session lives. There is no
// password to sign back in with, so when it ends the demo account is done.
const DemoSessionTTL = 72 * time.Hour

// StartDemo creates a fresh demo account (account.ModeDemo) and signs it in.
// Every visitor gets their own, so no one sees anyone else's demo orders.
// It has no password and an undeliverable address (.invalid), so it can't
// be signed into again or receive mail — the session is the account.
func (s *AccountService) StartDemo(ctx context.Context, meta account.ClientMeta) (*SignInResult, error) {
	id := newID("user")
	u := &account.User{
		ID: id, Email: "demo-" + strings.TrimPrefix(id, "user_") + "@demo.algebra.invalid",
		Name: "Demo shopper", Mode: account.ModeDemo, CreatedAt: s.now(),
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	res, err := s.startSessionTTL(ctx, u, meta, DemoSessionTTL)
	if err != nil {
		return nil, err
	}
	res.Created = true
	return res, nil
}

// startSession mints the session cookie token and the session's own console
// agent, sealing the agent's token so the server-side LLM loop can use it.
func (s *AccountService) startSession(ctx context.Context, u *account.User, meta account.ClientMeta) (*SignInResult, error) {
	return s.startSessionTTL(ctx, u, meta, s.sessionTTL)
}

func (s *AccountService) startSessionTTL(ctx context.Context, u *account.User, meta account.ClientMeta, ttl time.Duration) (*SignInResult, error) {
	raw, hash, err := account.NewSessionToken()
	if err != nil {
		return nil, err
	}
	agentToken, ag, err := s.agents.CreateAgent(ctx, u.ID, ConsoleAgentClientID, "Web console", ConsoleAgentPermissions)
	if err != nil {
		return nil, fmt.Errorf("app: provisioning console agent: %w", err)
	}
	now := s.now()
	sess := &account.Session{
		ID: newID("sess"), UserID: u.ID, TokenHash: hash, AgentID: ag.ID,
		UserAgent: truncate(meta.UserAgent, 256), IP: truncate(meta.IP, 64),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(ttl),
	}
	ct, nonce, err := s.sealer.Encrypt([]byte(agentToken), sessionAAD(sess))
	if err != nil {
		return nil, fmt.Errorf("app: sealing console agent token: %w", err)
	}
	sess.AgentTokenCiphertext, sess.AgentTokenNonce = ct, nonce
	if err := s.store.CreateSession(ctx, sess); err != nil {
		_ = s.agentStore.Revoke(ctx, ag.ID, now)
		return nil, err
	}
	return &SignInResult{User: u, Session: sess, Token: raw}, nil
}

func sessionAAD(sess *account.Session) []byte {
	return []byte("algebra:session-agent-token:" + sess.ID + ":" + sess.UserID)
}

// Authenticate resolves a raw session token. Last-seen is refreshed at most
// every five minutes so an active tab isn't a write per request.
func (s *AccountService) Authenticate(ctx context.Context, rawToken string) (*account.Session, error) {
	if rawToken == "" {
		return nil, account.ErrSessionInvalid
	}
	sess, err := s.store.GetSessionByTokenHash(ctx, account.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil, account.ErrSessionInvalid
		}
		return nil, err
	}
	now := s.now()
	if !sess.Active(now) {
		return nil, account.ErrSessionInvalid
	}
	if now.Sub(sess.LastSeenAt) > 5*time.Minute {
		_ = s.store.TouchSession(ctx, sess.ID, now)
	}
	return sess, nil
}

// SignOut revokes the session and its console agent.
func (s *AccountService) SignOut(ctx context.Context, sess *account.Session) error {
	now := s.now()
	if err := s.store.RevokeSession(ctx, sess.ID, now); err != nil {
		return err
	}
	if sess.AgentID != "" {
		_ = s.agentStore.Revoke(ctx, sess.AgentID, now)
	}
	return nil
}

// RevokeSession signs out one of the user's own sessions (e.g. another
// device) — never someone else's.
func (s *AccountService) RevokeSession(ctx context.Context, userID, sessionID string) error {
	sessions, err := s.store.ListActiveSessions(ctx, userID, s.now())
	if err != nil {
		return err
	}
	for i := range sessions {
		if sessions[i].ID == sessionID {
			return s.SignOut(ctx, &sessions[i])
		}
	}
	return fmt.Errorf("%w: session %s", shared.ErrNotFound, sessionID)
}

func (s *AccountService) ListSessions(ctx context.Context, userID string) ([]account.Session, error) {
	return s.store.ListActiveSessions(ctx, userID, s.now())
}

// AgentToken unseals the session's console agent token, for the web app's
// server-side agent loop. The token can shop within policy; it can never
// approve, since approval endpoints accept only a human session.
func (s *AccountService) AgentToken(sess *account.Session) (string, error) {
	if len(sess.AgentTokenCiphertext) == 0 {
		return "", fmt.Errorf("%w: session has no console agent", shared.ErrNotFound)
	}
	pt, err := s.sealer.Decrypt(sess.AgentTokenCiphertext, sess.AgentTokenNonce, sessionAAD(sess))
	if err != nil {
		return "", fmt.Errorf("app: unsealing console agent token: %w", err)
	}
	return string(pt), nil
}

// UserMode reports whether the user's account is live or demo — used by
// DiscoveryService to route purchases (AccountModes).
func (s *AccountService) UserMode(ctx context.Context, userID string) (account.Mode, error) {
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return account.ModeLive, err
	}
	if u.IsDemo() {
		return account.ModeDemo, nil
	}
	return account.ModeLive, nil
}

// DeleteAccount erases a person's account at their request (the DPDP
// Act's right to erasure). Their name, email, password, linked sign-ins,
// sessions, saved addresses and preferences go, and every agent token is
// revoked, so nothing can sign in or act as them again. Orders and the
// audit trail stay, tied only to the now-anonymous user ID — they're
// records Algebra must keep.
func (s *AccountService) DeleteAccount(ctx context.Context, userID string) error {
	return s.store.EraseUser(ctx, userID, s.now())
}

func (s *AccountService) User(ctx context.Context, id string) (*account.User, error) {
	return s.store.GetUser(ctx, id)
}

func (s *AccountService) LinkedProviders(ctx context.Context, userID string) ([]string, error) {
	return s.store.ListOAuthProviders(ctx, userID)
}

// UpdateName sets the user's display name.
func (s *AccountService) UpdateName(ctx context.Context, userID, name string) (*account.User, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 80 {
		return nil, errors.New("name must be at most 80 characters")
	}
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateUserProfile(ctx, userID, name, u.AvatarURL); err != nil {
		return nil, err
	}
	u.Name = name
	return u, nil
}

// RequestPasswordReset emails a single-use reset link. It returns nil for
// an unknown email too, so the endpoint can't be used to probe which
// addresses have accounts.
func (s *AccountService) RequestPasswordReset(ctx context.Context, email, linkBase string) error {
	email, err := account.NormalizeEmail(email)
	if err != nil {
		return nil
	}
	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil
		}
		return err
	}
	raw, hash, err := account.NewResetToken()
	if err != nil {
		return err
	}
	if err := s.store.CreateResetToken(ctx, hash, u.ID, s.now().Add(s.resetTTL)); err != nil {
		return err
	}
	link := strings.TrimRight(linkBase, "/") + "/reset-password?token=" + raw
	body := "Someone asked to reset the password for your Algebra account.\n\n" +
		"Set a new password here (valid for 30 minutes, single use):\n" + link + "\n\n" +
		"If this wasn't you, ignore this email — your password stays the same."
	return s.mailer.Send(ctx, u.Email, "Reset your Algebra password", body)
}

// ResetPassword consumes a reset token, sets the new password, and signs
// out every existing session — a reset is what you do when you think
// someone else might be signed in.
func (s *AccountService) ResetPassword(ctx context.Context, rawToken, newPassword string) error {
	if err := account.ValidatePassword(newPassword); err != nil {
		return err
	}
	userID, err := s.store.ConsumeResetToken(ctx, account.HashToken(rawToken), s.now())
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return errors.New("this reset link is invalid or has expired — request a new one")
		}
		return err
	}
	hash, err := account.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(ctx, userID, hash); err != nil {
		return err
	}
	sessions, err := s.store.ListActiveSessions(ctx, userID, s.now())
	if err != nil {
		return err
	}
	for i := range sessions {
		_ = s.SignOut(ctx, &sessions[i])
	}
	return nil
}

// Guardrails returns the user's guardrails, or the platform default (and
// isDefault=true) if they never set any.
func (s *AccountService) Guardrails(ctx context.Context, userID string) (g account.Guardrails, isDefault bool, err error) {
	stored, err := s.guardrails.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return account.DefaultGuardrails(), true, nil
		}
		return account.Guardrails{}, false, err
	}
	return *stored, false, nil
}

func (s *AccountService) SetGuardrails(ctx context.Context, userID string, g account.Guardrails) (account.Guardrails, error) {
	norm, err := g.Normalize()
	if err != nil {
		return account.Guardrails{}, err
	}
	if err := s.guardrails.Upsert(ctx, userID, norm, s.now()); err != nil {
		return account.Guardrails{}, err
	}
	return norm, nil
}

// PurgeExpired removes dead sessions and reset tokens (see AccountStore).
func (s *AccountService) PurgeExpired(ctx context.Context) (int64, error) {
	return s.store.PurgeExpired(ctx, s.now())
}

func (s *AccountService) MarkOnboarded(ctx context.Context, userID string) error {
	return s.store.MarkOnboarded(ctx, userID, s.now())
}

// RulesFor implements UserRulesSource: a user's guardrails as policy.Rules,
// or nil when they have none (the platform default applies).
func (s *AccountService) RulesFor(ctx context.Context, userID string) (*policy.Rules, error) {
	stored, err := s.guardrails.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	r := stored.ToRules()
	return &r, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
