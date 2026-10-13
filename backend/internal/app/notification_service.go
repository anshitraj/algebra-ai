package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-algebra/algebra/internal/domain/account"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// NotificationStore keeps each person's notification settings (migration
// 0025). No row means the defaults.
type NotificationStore interface {
	// GetNotifySettings returns shared.ErrNotFound when none were saved.
	GetNotifySettings(ctx context.Context, userID string) (*account.NotifySettings, error)
	SaveNotifySettings(ctx context.Context, s *account.NotifySettings, at time.Time) error
}

// AttachmentMailer is a Mailer that can attach one file (the receipt PDF).
// A mailer without it sends the same email with the links only.
type AttachmentMailer interface {
	SendWithAttachment(ctx context.Context, to, subject, text, filename string, data []byte) error
}

// NotificationUsers finds the account a notification is for.
type NotificationUsers interface {
	GetUser(ctx context.Context, id string) (*account.User, error)
}

const (
	// An added address is confirmed by a link that lasts this long.
	emailConfirmTTL = 24 * time.Hour
	// One confirmation email a minute per account: the form can't be used to
	// write to an inbox over and over.
	emailConfirmCooldown = time.Minute
	// One sign-in email a minute per account, however many sessions start.
	signInEmailCooldown = time.Minute
	// Receipt emails per account per hour. An agent can pay many times a
	// minute; every receipt stays in the console, and the inbox (and the mail
	// provider's quota, which password resets share) is not flooded.
	ReceiptEmailsPerHour = 20
)

// NotificationService emails people about their own account: a sign-in
// alert after each sign-in, and the receipt of each payment that lands on a
// chain. Mail goes to the address a person signs in with, or to one they
// added and confirmed; an account with neither (a wallet sign-in) gets
// nothing until it adds one. Sending never holds up or fails the sign-in or
// the payment it reports.
type NotificationService struct {
	store    NotificationStore
	users    NotificationUsers
	mailer   Mailer
	webURL   string
	brand    EmailBrand
	sending  bool
	receipts ReceiptVerifier
	// receiptPDF lays a receipt out as a PDF (set by the API, which owns the layout).
	receiptPDF func(v *IntentView, ver *IntentVerification) []byte
	log        *slog.Logger
	now        func() time.Time

	mu           sync.Mutex
	lastSignIn   map[string]time.Time
	receiptsSent map[string][]time.Time
	wg           sync.WaitGroup
}

// NewNotificationService: webURL is the public address of the web app, for
// the links in emails; sending says whether mailer really sends (false: the
// development mailer, which writes emails to the log).
func NewNotificationService(store NotificationStore, users NotificationUsers, mailer Mailer, webURL string, sending bool, log *slog.Logger) *NotificationService {
	if log == nil {
		log = slog.Default()
	}
	return &NotificationService{
		store: store, users: users, mailer: mailer, webURL: strings.TrimRight(webURL, "/"), brand: BrandFor(webURL), sending: sending, log: log, now: time.Now,
		lastSignIn: map[string]time.Time{}, receiptsSent: map[string][]time.Time{},
	}
}

// ReceiptVerifier reads a signed receipt back into its claims, having checked
// the signature. *IntentReceiptService is one.
type ReceiptVerifier interface {
	Verify(ctx context.Context, jws string) *IntentVerification
}

// SetReceipts gives the service the receipts it reads a payment from.
func (s *NotificationService) SetReceipts(r ReceiptVerifier) { s.receipts = r }

// SetReceiptPDF sets how a receipt is laid out as the PDF attached to its email.
func (s *NotificationService) SetReceiptPDF(fn func(v *IntentView, ver *IntentVerification) []byte) {
	s.receiptPDF = fn
}

// SetMailer replaces the mailer (a test captures what would be sent).
func (s *NotificationService) SetMailer(m Mailer) { s.mailer = m }

// AdoptVerifiedEmail uses an address the sign-in provider verified (Privy's
// email code) for an account that has none of its own: someone who signed in
// with a wallet and linked an email there later. Only while the account has
// never saved notification settings, so an address the person removed here
// doesn't come back at the next sign-in.
func (s *NotificationService) AdoptVerifiedEmail(ctx context.Context, u *account.User, email string) {
	if u == nil || u.IsDemo() || account.Deliverable(u.Email) {
		return
	}
	email, err := account.NormalizeEmail(email)
	if err != nil || !account.Deliverable(email) {
		return
	}
	if _, err := s.store.GetNotifySettings(ctx, u.ID); !errors.Is(err, shared.ErrNotFound) {
		return
	}
	now := s.now().UTC()
	st := account.DefaultNotifySettings(u.ID)
	st.Email, st.EmailVerifiedAt = email, &now
	if err := s.store.SaveNotifySettings(ctx, st, now); err != nil {
		s.log.Warn("notifications: adopting a verified email", "user_id", u.ID, "err", err)
	}
}

// Wait returns once every email started so far was handed to the mailer.
func (s *NotificationService) Wait() { s.wg.Wait() }

// NotificationView is a person's notification settings as the console shows them.
type NotificationView struct {
	// Address is where notifications go now; empty when they go nowhere.
	Address string `json:"address"`
	// Source is "account" (the address they sign in with), "added" (one they
	// confirmed here) or "" (none).
	Source string `json:"source"`
	// PendingEmail waits for its confirmation link.
	PendingEmail string `json:"pending_email,omitempty"`
	SignIns      bool   `json:"sign_ins"`
	Receipts     bool   `json:"receipts"`
	// NeedsEmail: the account has no address mail can reach (it signed in
	// with a wallet) and hasn't confirmed one.
	NeedsEmail bool `json:"needs_email"`
	// CanAdd is false for a demo account.
	CanAdd bool `json:"can_add"`
	// Sending is false when this server has no mail provider: emails are
	// written to its log instead.
	Sending bool `json:"sending"`
}

func (s *NotificationService) settings(ctx context.Context, userID string) (*account.NotifySettings, error) {
	st, err := s.store.GetNotifySettings(ctx, userID)
	if errors.Is(err, shared.ErrNotFound) {
		return account.DefaultNotifySettings(userID), nil
	}
	return st, err
}

// addressOf is where u's notifications go: the address they added and
// confirmed, else the one they sign in with, else nowhere.
func addressOf(u *account.User, st *account.NotifySettings) (addr, source string) {
	if u.IsDemo() {
		return "", ""
	}
	if e := st.ConfirmedEmail(); e != "" {
		return e, "added"
	}
	if account.Deliverable(u.Email) {
		return u.Email, "account"
	}
	return "", ""
}

func (s *NotificationService) view(u *account.User, st *account.NotifySettings) *NotificationView {
	addr, source := addressOf(u, st)
	v := &NotificationView{
		Address: addr, Source: source, SignIns: st.SignIns, Receipts: st.Receipts,
		NeedsEmail: addr == "" && !u.IsDemo(), CanAdd: !u.IsDemo(), Sending: s.sending,
	}
	if st.PendingEmail != "" && st.PendingExpiresAt != nil && s.now().Before(*st.PendingExpiresAt) {
		v.PendingEmail = st.PendingEmail
	}
	return v
}

// View is u's notification settings.
func (s *NotificationService) View(ctx context.Context, u *account.User) (*NotificationView, error) {
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return s.view(u, st), nil
}

func invalidNotification(msg string) error { return fmt.Errorf("%w: %s", ErrInvalidRequest, msg) }

// SetEmail asks for u's notifications to go to email. Nothing goes there
// but a confirmation link until that link is opened (ConfirmEmail). Naming
// the address u signs in with needs no link: it goes back to that one.
func (s *NotificationService) SetEmail(ctx context.Context, u *account.User, email string) (*NotificationView, error) {
	if u.IsDemo() {
		return nil, invalidNotification("a demo account has no email notifications; create an account to get them")
	}
	email, err := account.NormalizeEmail(email)
	if err != nil {
		return nil, invalidNotification(err.Error())
	}
	if !account.Deliverable(email) {
		return nil, invalidNotification("enter an address that can receive mail")
	}
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	clearPending := func() {
		st.PendingEmail, st.PendingTokenHash, st.PendingExpiresAt, st.PendingSentAt = "", "", nil, nil
	}
	switch {
	case account.Deliverable(u.Email) && strings.EqualFold(email, u.Email):
		st.Email, st.EmailVerifiedAt = "", nil
		clearPending()
		return s.saved(ctx, u, st, now)
	case email == st.ConfirmedEmail():
		clearPending()
		return s.saved(ctx, u, st, now)
	}
	if st.PendingSentAt != nil {
		if wait := emailConfirmCooldown - now.Sub(*st.PendingSentAt); wait > 0 {
			return nil, &RateLimited{What: "confirmation emails", Retry: wait}
		}
	}
	raw, hash, err := account.NewEmailToken()
	if err != nil {
		return nil, err
	}
	expires := now.Add(emailConfirmTTL)
	st.PendingEmail, st.PendingTokenHash, st.PendingExpiresAt, st.PendingSentAt = email, hash, &expires, &now
	if err := s.store.SaveNotifySettings(ctx, st, now); err != nil {
		return nil, err
	}
	link := s.webURL + "/console/settings?confirm_email=" + url.QueryEscape(raw)
	doc := EmailDoc{
		Preheader: "Open the link to get Algebra's sign-in alerts and receipts at this address.",
		Heading:   "Confirm your email",
		Lead:      []string{"Someone asked for Algebra's email notifications (sign-in alerts and receipts for paid calls) to be sent to this address."},
		Button:    &EmailLink{Label: "Confirm this email", URL: link},
		Notes: []string{
			"Open it while signed in to that account. It works once and expires in 24 hours.",
			"If this wasn't you, ignore this email: nothing more is sent to this address unless the link is opened.",
		},
	}
	if err := SendEmail(ctx, s.mailer, s.brand, email, "Confirm your email for Algebra notifications", doc, "", nil); err != nil {
		// The address may be retried at once: nothing reached it.
		st.PendingSentAt = nil
		_ = s.store.SaveNotifySettings(ctx, st, now)
		s.log.Warn("notifications: confirmation email not sent", "user_id", u.ID, "err", err)
		return nil, errors.New("the confirmation email couldn't be sent; try again in a moment")
	}
	return s.view(u, st), nil
}

func (s *NotificationService) saved(ctx context.Context, u *account.User, st *account.NotifySettings, now time.Time) (*NotificationView, error) {
	if err := s.store.SaveNotifySettings(ctx, st, now); err != nil {
		return nil, err
	}
	return s.view(u, st), nil
}

// ConfirmEmail finishes SetEmail with the token from the link, for the
// account that asked. The token works once.
func (s *NotificationService) ConfirmEmail(ctx context.Context, u *account.User, token string) (*NotificationView, error) {
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	token = strings.TrimSpace(token)
	stale := invalidNotification("this confirmation link was already used, replaced, or belongs to another account; ask for a new one")
	if token == "" || st.PendingEmail == "" || st.PendingTokenHash == "" {
		return nil, stale
	}
	if subtle.ConstantTimeCompare([]byte(account.HashToken(token)), []byte(st.PendingTokenHash)) != 1 {
		return nil, stale
	}
	if st.PendingExpiresAt == nil || !now.Before(*st.PendingExpiresAt) {
		return nil, invalidNotification("this confirmation link has expired; ask for a new one")
	}
	st.Email, st.EmailVerifiedAt = st.PendingEmail, &now
	st.PendingEmail, st.PendingTokenHash, st.PendingExpiresAt, st.PendingSentAt = "", "", nil, nil
	return s.saved(ctx, u, st, now)
}

// RemoveEmail drops the added address (and one waiting to be confirmed):
// notifications go back to the address u signs in with, or nowhere.
func (s *NotificationService) RemoveEmail(ctx context.Context, u *account.User) (*NotificationView, error) {
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	st.Email, st.EmailVerifiedAt = "", nil
	st.PendingEmail, st.PendingTokenHash, st.PendingExpiresAt, st.PendingSentAt = "", "", nil, nil
	return s.saved(ctx, u, st, s.now().UTC())
}

// SetKinds turns each kind of email on or off; nil leaves a kind as it is.
func (s *NotificationService) SetKinds(ctx context.Context, u *account.User, signIns, receipts *bool) (*NotificationView, error) {
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if signIns != nil {
		st.SignIns = *signIns
	}
	if receipts != nil {
		st.Receipts = *receipts
	}
	return s.saved(ctx, u, st, s.now().UTC())
}

// SignedIn emails u that a session just started: when, from which device
// and address, and how to end it if it wasn't them. created: the account's
// first sign-in, which gets a welcome instead.
func (s *NotificationService) SignedIn(ctx context.Context, u *account.User, meta account.ClientMeta, created bool) {
	if u == nil || u.IsDemo() {
		return
	}
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		s.log.Warn("notifications: reading settings", "user_id", u.ID, "err", err)
		return
	}
	to, _ := addressOf(u, st)
	if to == "" || !st.SignIns {
		return
	}
	now := s.now().UTC()
	s.mu.Lock()
	if last, ok := s.lastSignIn[u.ID]; ok && now.Sub(last) < signInEmailCooldown {
		s.mu.Unlock()
		return
	}
	s.sweep(now)
	s.lastSignIn[u.ID] = now
	s.mu.Unlock()

	hello := "Hello,"
	if n := oneLine(u.Name, 80); n != "" {
		hello = "Hi " + n + ","
	}
	device := describeUserAgent(meta.UserAgent)
	settings := s.webURL + "/console/settings"
	doc := EmailDoc{
		Preheader: "From " + device + ". If this wasn't you, sign that device out.",
		Heading:   "New sign-in to your account",
		Lead:      []string{hello, "You signed in to Algebra."},
		Rows: []EmailRow{
			{Label: "When", Value: now.Format("2 January 2006, 15:04 UTC")},
			{Label: "Device", Value: device},
			{Label: "IP address", Value: cmpOrDash(oneLine(meta.IP, 64))},
		},
		Button: &EmailLink{Label: "Review signed-in devices", URL: settings},
		Notes: []string{
			"If this was you, there is nothing to do.",
			"If it wasn't, sign that device out under Account > Where you're signed in. That also revokes the agent it was using.",
		},
		Footer: []string{"You get this email after each sign-in. Turn it off under Account > Email notifications."},
	}
	subject := "New sign-in to Algebra"
	if created {
		subject = "Welcome to Algebra"
		doc.Preheader = "Your account is ready. Start in the sandbox, with simulated USDC."
		doc.Heading = "Welcome to Algebra"
		doc.Lead = []string{hello, "Your Algebra account is ready, and you are signed in. Start in the sandbox: a first paid call with simulated USDC, no wallet needed."}
		doc.Button = &EmailLink{Label: "Start the quickstart", URL: s.webURL + "/console/quickstart"}
		doc.Links = []EmailLink{{Label: "Review signed-in devices", URL: settings}}
		doc.Notes = []string{"This sign-in is listed above. If it wasn't you, sign that device out under Account > Where you're signed in."}
	}
	s.deliver(ctx, "sign_in", u.ID, to, subject, doc, "", nil)
}

// ReceiptSigned emails the receipt of a payment that landed on a chain to
// the person it was made for, with the PDF attached. A sandbox call moves
// nothing and sends nothing. v must carry its signed receipt.
func (s *NotificationService) ReceiptSigned(ctx context.Context, v *IntentView) {
	if v == nil || v.Receipt == "" || s.receipts == nil {
		return
	}
	u, err := s.users.GetUser(ctx, v.PrincipalID)
	if err != nil || u.IsDemo() {
		return
	}
	st, err := s.settings(ctx, u.ID)
	if err != nil {
		s.log.Warn("notifications: reading settings", "user_id", u.ID, "err", err)
		return
	}
	to, _ := addressOf(u, st)
	if to == "" || !st.Receipts {
		return
	}
	ver := s.receipts.Verify(ctx, v.Receipt)
	if ver == nil || !ver.Valid || ver.Claims == nil || ver.Claims.Settlement == nil {
		return
	}
	c, pay := ver.Claims, ver.Claims.Settlement
	links := ExplorerLinks(pay.Network, pay.Transaction)
	if len(links) == 0 || pay.Amount.MinorUnits <= 0 {
		return // nothing on a chain: a sandbox or a free call
	}
	now := s.now().UTC()
	s.mu.Lock()
	recent := s.receiptsSent[u.ID][:0]
	for _, t := range s.receiptsSent[u.ID] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= ReceiptEmailsPerHour {
		s.receiptsSent[u.ID] = recent
		s.mu.Unlock()
		s.log.Info("notifications: receipt email skipped, hourly cap reached", "user_id", u.ID, "intent_id", v.ID)
		return
	}
	s.receiptsSent[u.ID] = append(recent, now)
	s.mu.Unlock()

	amount := usdcAmount(pay.Amount)
	where := "Solana mainnet"
	// The provider's ID is a catalog's text: one line of it, and no more.
	provider := oneLine(c.Provider.ID, 120)
	subject := "Receipt: " + amount + " paid to " + provider
	if pay.Network == "solana-devnet" {
		where = "Solana devnet (test USDC, no real value)"
		subject += " (devnet, test USDC)"
	}
	authority := "Within your Spend Pass's limits"
	if c.Authority.Method == receipt.MethodHuman {
		authority = "Approved by you"
	}
	var pdf []byte
	if s.receiptPDF != nil {
		pdf = s.receiptPDF(v, ver)
	}
	invoice := s.webURL + "/console/executions/" + url.PathEscape(v.ID) + "/invoice"
	doc := EmailDoc{
		Preheader: amount + " to " + provider + " on " + where,
		Heading:   "Receipt for a paid API call",
		Lead:      []string{"Algebra paid for an API call under your Spend Pass."},
		Hero:      &EmailHero{Value: amount, Caption: "paid to " + provider},
		Rows: []EmailRow{
			{Label: "For", Value: oneLine(v.Capability, 200)},
			{Label: "Network", Value: where},
			{Label: "Authority", Value: authority},
			{Label: "Signed at", Value: time.Unix(c.IssuedAt, 0).UTC().Format("2 January 2006, 15:04:05 UTC")},
			{Label: "Receipt ID", Value: c.ID, Wide: true},
			{Label: "Transaction", Value: pay.Transaction, Wide: true},
		},
		Button: &EmailLink{Label: "View the signed receipt", URL: invoice},
		Footer: []string{fmt.Sprintf("You get this email for each payment that lands on a chain, at most %d an hour. Turn it off under Account > Email notifications: %s/console/settings", ReceiptEmailsPerHour, s.webURL)},
	}
	if pay.Network == "solana-devnet" {
		doc.Badge = "Devnet: test USDC, no real value"
	}
	for _, l := range links {
		doc.Links = append(doc.Links, EmailLink{Label: l[0], URL: l[1]})
	}
	doc.Links = append(doc.Links, EmailLink{Label: "Check its signature", URL: s.webURL + "/verify"})
	if len(pdf) > 0 {
		doc.Notes = append(doc.Notes, "The receipt is attached as a PDF.")
	}
	doc.Notes = append(doc.Notes, "This is a receipt for a payment, not a tax invoice.")
	s.deliver(ctx, "receipt", u.ID, to, subject, doc, "algebra-receipt-"+v.ID+".pdf", pdf)
}

// deliver hands one email to the mailer off the caller's path. A failure is
// logged without the address; what the email reports already happened.
func (s *NotificationService) deliver(ctx context.Context, kind, userID, to, subject string, doc EmailDoc, filename string, file []byte) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if err := SendEmail(ctx, s.mailer, s.brand, to, subject, doc, filename, file); err != nil {
			s.log.Warn("notifications: email not sent", "kind", kind, "user_id", userID, "err", err)
		}
	}()
}

// sweep forgets accounts that haven't signed in or paid for an hour, so the
// two maps stay as small as the number of people active now. Called with mu held.
func (s *NotificationService) sweep(now time.Time) {
	if len(s.lastSignIn)+len(s.receiptsSent) < 4096 {
		return
	}
	for id, t := range s.lastSignIn {
		if now.Sub(t) > time.Hour {
			delete(s.lastSignIn, id)
		}
	}
	for id, ts := range s.receiptsSent {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > time.Hour {
			delete(s.receiptsSent, id)
		}
	}
}

// ExplorerLinks are a transaction on Solana Explorer and Solscan, for the
// two clusters that have them; none for the sandbox.
func ExplorerLinks(network, tx string) [][2]string {
	if tx == "" || (network != "solana" && network != "solana-devnet") {
		return nil
	}
	esc := url.PathEscape(tx)
	cluster := ""
	if network == "solana-devnet" {
		cluster = "?cluster=devnet"
	}
	return [][2]string{
		{"Solana Explorer", "https://explorer.solana.com/tx/" + esc + cluster},
		{"Solscan", "https://solscan.io/tx/" + esc + cluster},
	}
}

// usdcAmount formats minor units of USDC ("0.002 USDC").
func usdcAmount(m receipt.Money) string {
	s := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(float64(m.MinorUnits)/1e6, 'f', 6, 64), "0"), ".")
	cur := strings.ToUpper(m.Currency)
	if cur == "" {
		cur = "USDC"
	}
	return s + " " + cur
}

// oneLine is s without control characters, trimmed and cut to max runes: text
// someone else wrote (a name, a catalog's provider ID) can't break a subject
// line or start a new paragraph of an email.
func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

func cmpOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

// describeUserAgent names a browser and system from a User-Agent header
// ("Chrome on Windows"), the way the console's device list does.
func describeUserAgent(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return "Unknown device"
	}
	has := func(s string) bool { return strings.Contains(ua, s) }
	browser := "Browser"
	switch {
	case has("Edg/"):
		browser = "Edge"
	case has("Chrome/"):
		browser = "Chrome"
	case has("Firefox/"):
		browser = "Firefox"
	case has("Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case has("Windows"):
		os = "Windows"
	case has("Android"):
		os = "Android"
	case has("iPhone"), has("iPad"):
		os = "iOS"
	case has("Mac OS X"):
		os = "macOS"
	case has("Linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}
