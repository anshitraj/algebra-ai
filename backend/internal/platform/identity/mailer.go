package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// resendEndpoint is Resend's send-an-email call.
const resendEndpoint = "https://api.resend.com/emails"

// ResendMailer sends email through Resend's HTTP API (resend.com) — one
// POST, no SDK. Configure with RESEND_API_KEY and EMAIL_FROM (a sender on a
// domain verified in Resend).
type ResendMailer struct {
	apiKey string
	from   string
	// endpoint is resendEndpoint, except in tests.
	endpoint string
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{apiKey: apiKey, from: from, endpoint: resendEndpoint}
}

func (m *ResendMailer) Send(ctx context.Context, to, subject, text string) error {
	return m.post(ctx, map[string]any{"from": m.from, "to": []string{to}, "subject": subject, "text": text})
}

// SendWithAttachment is Send with one file attached (a receipt's PDF).
func (m *ResendMailer) SendWithAttachment(ctx context.Context, to, subject, text, filename string, data []byte) error {
	return m.post(ctx, map[string]any{
		"from": m.from, "to": []string{to}, "subject": subject, "text": text,
		"attachments": []map[string]string{{"filename": filename, "content": base64.StdEncoding.EncodeToString(data)}},
	})
}

func (m *ResendMailer) post(ctx context.Context, email map[string]any) error {
	body, err := json.Marshal(email)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
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
	m.logger().Warn("email not sent (no RESEND_API_KEY configured) — dev log mailer", "to", to, "subject", subject, "body", text)
	return nil
}

// SendWithAttachment logs the email and what would have been attached.
func (m LogMailer) SendWithAttachment(_ context.Context, to, subject, text, filename string, data []byte) error {
	m.logger().Warn("email not sent (no RESEND_API_KEY configured) — dev log mailer", "to", to, "subject", subject, "body", text,
		"attachment", filename, "attachment_bytes", len(data))
	return nil
}

func (m LogMailer) logger() *slog.Logger {
	if m.Logger == nil {
		return slog.Default()
	}
	return m.Logger
}
