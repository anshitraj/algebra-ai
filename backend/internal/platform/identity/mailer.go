package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// ResendMailer sends email through Resend's HTTP API (resend.com) — one
// POST, no SDK. Configure with RESEND_API_KEY and EMAIL_FROM (a sender on a
// domain verified in Resend).
type ResendMailer struct {
	apiKey string
	from   string
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{apiKey: apiKey, from: from}
}

func (m *ResendMailer) Send(ctx context.Context, to, subject, text string) error {
	body, err := json.Marshal(map[string]any{"from": m.from, "to": []string{to}, "subject": subject, "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("identity: sending email: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("identity: resend returned %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// LogMailer writes emails to the server log instead of sending them. It is
// the default when no mail provider is configured, so password reset works
// in local development (copy the link from the API's output) — never use it
// in production, where the link would sit in log storage.
type LogMailer struct{ Logger *slog.Logger }

func (m LogMailer) Send(_ context.Context, to, subject, text string) error {
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("email not sent (no RESEND_API_KEY configured) — dev log mailer", "to", to, "subject", subject, "body", text)
	return nil
}
