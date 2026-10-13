package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// NotificationRepo backs app.NotificationStore: see
// migrations/0025_notification_email.sql.
type NotificationRepo struct{ db *DB }

func NewNotificationRepo(db *DB) *NotificationRepo { return &NotificationRepo{db: db} }

func (r *NotificationRepo) GetNotifySettings(ctx context.Context, userID string) (*account.NotifySettings, error) {
	s := account.NotifySettings{UserID: userID}
	err := r.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(email,''), email_verified_at, COALESCE(pending_email,''), COALESCE(pending_token_hash,''),
		       pending_expires_at, pending_sent_at, sign_ins, receipts
		FROM user_notifications WHERE user_id = $1`, userID).
		Scan(&s.Email, &s.EmailVerifiedAt, &s.PendingEmail, &s.PendingTokenHash, &s.PendingExpiresAt, &s.PendingSentAt, &s.SignIns, &s.Receipts)
	if err == pgx.ErrNoRows {
		return nil, shared.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading notification settings: %w", err)
	}
	return &s, nil
}

func (r *NotificationRepo) SaveNotifySettings(ctx context.Context, s *account.NotifySettings, at time.Time) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO user_notifications (user_id, email, email_verified_at, pending_email, pending_token_hash,
		                                pending_expires_at, pending_sent_at, sign_ins, receipts, updated_at)
		VALUES ($1, NULLIF($2,''), $3, NULLIF($4,''), NULLIF($5,''), $6, $7, $8, $9, $10)
		ON CONFLICT (user_id) DO UPDATE SET
			email = EXCLUDED.email, email_verified_at = EXCLUDED.email_verified_at,
			pending_email = EXCLUDED.pending_email, pending_token_hash = EXCLUDED.pending_token_hash,
			pending_expires_at = EXCLUDED.pending_expires_at, pending_sent_at = EXCLUDED.pending_sent_at,
			sign_ins = EXCLUDED.sign_ins, receipts = EXCLUDED.receipts, updated_at = EXCLUDED.updated_at`,
		s.UserID, s.Email, s.EmailVerifiedAt, s.PendingEmail, s.PendingTokenHash, s.PendingExpiresAt, s.PendingSentAt, s.SignIns, s.Receipts, at)
	if err != nil {
		return fmt.Errorf("postgres: saving notification settings: %w", err)
	}
	return nil
}
