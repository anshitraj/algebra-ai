package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// AccountRepo backs app.AccountStore — see migrations/0008_accounts.sql.
type AccountRepo struct{ db *DB }

func NewAccountRepo(db *DB) *AccountRepo { return &AccountRepo{db: db} }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r *AccountRepo) CreateUser(ctx context.Context, u *account.User) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, name, avatar_url, password_hash, email_verified_at, created_at, mode)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), $6, $7, $8)`,
		u.ID, u.Email, u.Name, u.AvatarURL, u.PasswordHash, u.EmailVerifiedAt, u.CreatedAt, userMode(u.Mode))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: email already registered", shared.ErrConflict)
		}
		return fmt.Errorf("postgres: inserting user: %w", err)
	}
	return nil
}

const accountUserSelect = `
	SELECT id, email, COALESCE(name,''), COALESCE(avatar_url,''), COALESCE(password_hash,''),
	       email_verified_at, onboarded_at, created_at, mode
	FROM users`

// userMode stores an unset mode as live — the safe default: a live account
// can never reach a demo store.
func userMode(m account.Mode) string {
	if m == account.ModeDemo {
		return string(account.ModeDemo)
	}
	return string(account.ModeLive)
}

func scanAccountUser(row pgx.Row) (*account.User, error) {
	var u account.User
	var mode string
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.PasswordHash,
		&u.EmailVerifiedAt, &u.OnboardedAt, &u.CreatedAt, &mode); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning user: %w", err)
	}
	u.Mode = account.Mode(userMode(account.Mode(mode)))
	return &u, nil
}

func (r *AccountRepo) GetUser(ctx context.Context, id string) (*account.User, error) {
	return scanAccountUser(r.db.Pool.QueryRow(ctx, accountUserSelect+` WHERE id = $1`, id))
}

func (r *AccountRepo) GetUserByEmail(ctx context.Context, email string) (*account.User, error) {
	return scanAccountUser(r.db.Pool.QueryRow(ctx,
		accountUserSelect+` WHERE lower(email) = $1 AND tenant_id IS NULL ORDER BY created_at LIMIT 1`, email))
}

func (r *AccountRepo) exec(ctx context.Context, what, sql string, args ...any) error {
	tag, err := r.db.Pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("postgres: %s: %w", what, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", shared.ErrNotFound, what)
	}
	return nil
}

func (r *AccountRepo) UpdateUserProfile(ctx context.Context, id, name, avatarURL string) error {
	return r.exec(ctx, "updating user profile",
		`UPDATE users SET name = NULLIF($2,''), avatar_url = NULLIF($3,'') WHERE id = $1`, id, name, avatarURL)
}

func (r *AccountRepo) SetPassword(ctx context.Context, userID, hash string) error {
	return r.exec(ctx, "setting password", `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
}

func (r *AccountRepo) MarkEmailVerified(ctx context.Context, userID string, at time.Time) error {
	return r.exec(ctx, "marking email verified",
		`UPDATE users SET email_verified_at = COALESCE(email_verified_at, $2) WHERE id = $1`, userID, at)
}

func (r *AccountRepo) MarkOnboarded(ctx context.Context, userID string, at time.Time) error {
	return r.exec(ctx, "marking onboarded",
		`UPDATE users SET onboarded_at = COALESCE(onboarded_at, $2) WHERE id = $1`, userID, at)
}

func (r *AccountRepo) GetOAuthIdentity(ctx context.Context, provider, providerUserID string) (string, error) {
	var userID string
	err := r.db.Pool.QueryRow(ctx,
		`SELECT user_id FROM oauth_identities WHERE provider = $1 AND provider_user_id = $2`,
		provider, providerUserID).Scan(&userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", shared.ErrNotFound
		}
		return "", fmt.Errorf("postgres: reading oauth identity: %w", err)
	}
	return userID, nil
}

func (r *AccountRepo) LinkOAuthIdentity(ctx context.Context, provider, providerUserID, userID, email string, at time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO oauth_identities (provider, provider_user_id, user_id, email, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (provider, provider_user_id) DO NOTHING`,
		provider, providerUserID, userID, email, at)
	if err != nil {
		return fmt.Errorf("postgres: linking oauth identity: %w", err)
	}
	return nil
}

func (r *AccountRepo) ListOAuthProviders(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT DISTINCT provider FROM oauth_identities WHERE user_id = $1 ORDER BY provider`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing oauth providers: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("postgres: scanning oauth provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *AccountRepo) CreateSession(ctx context.Context, s *account.Session) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO user_sessions (id, user_id, token_hash, agent_id, agent_token_ciphertext, agent_token_nonce,
		                           user_agent, ip, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, NULLIF($7,''), NULLIF($8,''), $9, $10, $11)`,
		s.ID, s.UserID, s.TokenHash, s.AgentID, s.AgentTokenCiphertext, s.AgentTokenNonce,
		s.UserAgent, s.IP, s.CreatedAt, s.LastSeenAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting session: %w", err)
	}
	return nil
}

const sessionSelect = `
	SELECT id, user_id, token_hash, COALESCE(agent_id,''), agent_token_ciphertext, agent_token_nonce,
	       COALESCE(user_agent,''), COALESCE(ip,''), created_at, last_seen_at, expires_at, revoked_at
	FROM user_sessions`

func scanSession(row pgx.Row) (*account.Session, error) {
	var s account.Session
	if err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.AgentID, &s.AgentTokenCiphertext, &s.AgentTokenNonce,
		&s.UserAgent, &s.IP, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.RevokedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: scanning session: %w", err)
	}
	return &s, nil
}

func (r *AccountRepo) GetSessionByTokenHash(ctx context.Context, hash string) (*account.Session, error) {
	return scanSession(r.db.Pool.QueryRow(ctx, sessionSelect+` WHERE token_hash = $1`, hash))
}

func (r *AccountRepo) TouchSession(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `UPDATE user_sessions SET last_seen_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: touching session: %w", err)
	}
	return nil
}

func (r *AccountRepo) RevokeSession(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `UPDATE user_sessions SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("postgres: revoking session: %w", err)
	}
	return nil
}

func (r *AccountRepo) ListActiveSessions(ctx context.Context, userID string, now time.Time) ([]account.Session, error) {
	rows, err := r.db.Pool.Query(ctx,
		sessionSelect+` WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2 ORDER BY last_seen_at DESC`,
		userID, now)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing sessions: %w", err)
	}
	defer rows.Close()
	var out []account.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *AccountRepo) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.Add(-7 * 24 * time.Hour)
	// A session keeps its row a week past death so "where you're signed in"
	// and audits can still resolve it; its agent was already revoked.
	s, err := r.db.Pool.Exec(ctx, `DELETE FROM user_sessions WHERE (revoked_at IS NOT NULL AND revoked_at < $1) OR expires_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres: purging sessions: %w", err)
	}
	t, err := r.db.Pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE used_at IS NOT NULL OR expires_at < $1`, now)
	if err != nil {
		return s.RowsAffected(), fmt.Errorf("postgres: purging reset tokens: %w", err)
	}
	return s.RowsAffected() + t.RowsAffected(), nil
}

// EraseUser scrubs a user's personal data in one transaction: profile and
// sign-in details, linked OAuth accounts, sessions, reset tokens, saved
// addresses, shopping preferences and guardrails go; payment methods and
// agents are revoked with their card details cleared. Orders, intents and
// audit events stay — financial records — keyed only by the user ID.
func (r *AccountRepo) EraseUser(ctx context.Context, userID string, at time.Time) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: erasing user: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	tag, err := tx.Exec(ctx, `UPDATE users SET
			email = 'deleted-' || id || '@deleted.invalid',
			name = NULL, avatar_url = NULL, password_hash = NULL,
			email_verified_at = NULL, deleted_at = $2
		WHERE id = $1 AND deleted_at IS NULL`, userID, at)
	if err != nil {
		return fmt.Errorf("postgres: erasing user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: user %s", shared.ErrNotFound, userID)
	}
	steps := []struct{ what, sql string }{
		{"oauth identities", `DELETE FROM oauth_identities WHERE user_id = $1`},
		{"sessions", `DELETE FROM user_sessions WHERE user_id = $1`},
		{"reset tokens", `DELETE FROM password_reset_tokens WHERE user_id = $1`},
		{"saved addresses", `DELETE FROM private_profiles WHERE user_id = $1`},
		{"commerce profile", `DELETE FROM commerce_profiles WHERE user_id = $1`},
		{"guardrails", `DELETE FROM user_guardrails WHERE user_id = $1`},
		{"plugin settings", `DELETE FROM user_plugins WHERE user_id = $1`},
		{"payment methods", `UPDATE payment_sources SET revoked_at = COALESCE(revoked_at, $2),
			nickname = NULL, last4 = NULL, issuer_meta = NULL, expiry_meta = NULL WHERE user_id = $1`},
		{"agents", `UPDATE agents SET revoked_at = COALESCE(revoked_at, $2) WHERE user_id = $1`},
		// What their agents left behind: no pass may spend any more, the answers kept
		// for repeat requests go, and what they asked is scrubbed from their intents.
		// The intents, receipts and the hash of each request stay: records the law
		// requires, tied now to nobody.
		{"spend passes", `UPDATE spend_passes SET revoked_at = COALESCE(revoked_at, $2) WHERE user_id = $1`},
		{"kept results", `DELETE FROM intent_results WHERE principal_id = $1`},
		{"request inputs", `UPDATE economic_intents SET input = '{}'::jsonb WHERE principal_id = $1`},
	}
	for _, s := range steps {
		args := []any{userID}
		if strings.Contains(s.sql, "$2") {
			args = append(args, at)
		}
		if _, err := tx.Exec(ctx, s.sql, args...); err != nil {
			return fmt.Errorf("postgres: erasing %s: %w", s.what, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: erasing user: %w", err)
	}
	return nil
}

func (r *AccountRepo) CreateResetToken(ctx context.Context, hash, userID string, expiresAt time.Time) error {
	_, err := r.db.Pool.Exec(ctx,
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting reset token: %w", err)
	}
	return nil
}

func (r *AccountRepo) ConsumeResetToken(ctx context.Context, hash string, now time.Time) (string, error) {
	var userID string
	err := r.db.Pool.QueryRow(ctx, `
		UPDATE password_reset_tokens SET used_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING user_id`, hash, now).Scan(&userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", shared.ErrNotFound
		}
		return "", fmt.Errorf("postgres: consuming reset token: %w", err)
	}
	return userID, nil
}

var _ app.AccountStore = (*AccountRepo)(nil)

// GuardrailRepo backs app.GuardrailStore.
type GuardrailRepo struct{ db *DB }

func NewGuardrailRepo(db *DB) *GuardrailRepo { return &GuardrailRepo{db: db} }

func (r *GuardrailRepo) Get(ctx context.Context, userID string) (*account.Guardrails, error) {
	var raw []byte
	err := r.db.Pool.QueryRow(ctx, `SELECT guardrails FROM user_guardrails WHERE user_id = $1`, userID).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: reading guardrails: %w", err)
	}
	var g account.Guardrails
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("postgres: decoding guardrails: %w", err)
	}
	return &g, nil
}

func (r *GuardrailRepo) Upsert(ctx context.Context, userID string, g account.Guardrails, at time.Time) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("postgres: encoding guardrails: %w", err)
	}
	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO user_guardrails (user_id, guardrails, updated_at) VALUES ($1, $2::jsonb, $3)
		ON CONFLICT (user_id) DO UPDATE SET guardrails = EXCLUDED.guardrails, updated_at = EXCLUDED.updated_at`,
		userID, string(raw), at)
	if err != nil {
		return fmt.Errorf("postgres: upserting guardrails: %w", err)
	}
	return nil
}

var _ app.GuardrailStore = (*GuardrailRepo)(nil)
