package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/account"
)

// One document, two layouts: what a person reads in HTML and what they read
// as plain text must be the same email, and nothing someone else wrote may
// reach the HTML as markup.

// sampleSignature stands in for a transaction signature: as long as a real one is wide,
// and plainly not one.
const sampleSignature = "SampleTransactionSignatureSampleTransaction11"

func sampleDoc() EmailDoc {
	return EmailDoc{
		Preheader: "0.001 USDC to demo:beta",
		Badge:     "Devnet: test USDC, no real value",
		Heading:   "Receipt for a paid API call",
		Lead:      []string{"Algebra paid for an API call under your Spend Pass."},
		Hero:      &EmailHero{Value: "0.001 USDC", Caption: "paid to demo:beta"},
		Rows: []EmailRow{
			{Label: "For", Value: "token.price"},
			{Label: "Transaction", Value: sampleSignature, Wide: true},
		},
		Button: &EmailLink{Label: "View the signed receipt", URL: "https://algebra.example/console/executions/eint_1/invoice"},
		Links:  []EmailLink{{Label: "Solscan", URL: "https://solscan.io/tx/5z5S?cluster=devnet"}},
		Notes:  []string{"This is a receipt for a payment, not a tax invoice."},
		Footer: []string{"Turn it off under Account > Email notifications."},
	}
}

func TestEmailDoc_HTMLAndTextSayTheSameThing(t *testing.T) {
	brand := BrandFor("https://algebra.example/")
	d := sampleDoc()
	html, text := d.HTML(brand, "Receipt"), d.Text(brand)
	for _, want := range []string{
		"Receipt for a paid API call", "Algebra paid for an API call under your Spend Pass.", "0.001 USDC", "paid to demo:beta",
		"token.price", sampleSignature, "View the signed receipt",
		"https://algebra.example/console/executions/eint_1/invoice", "Solscan", "not a tax invoice", "Email notifications", "Devnet: test USDC, no real value",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the text says %q", want)
		}
		// In HTML an address inside an attribute has its "&" escaped; none of these has one.
		if !strings.Contains(html, strings.ReplaceAll(want, ">", "&gt;")) {
			t.Errorf("the HTML says %q", want)
		}
	}
	if !strings.Contains(html, `href="https://solscan.io/tx/5z5S?cluster=devnet"`) {
		t.Error("a lesser link is a link")
	}
}

func TestEmailDoc_LaysOutForMailClients(t *testing.T) {
	html := sampleDoc().HTML(BrandFor("https://algebra.example"), "Receipt")
	for _, want := range []string{
		`<meta name="viewport" content="width=device-width,initial-scale=1">`, // a phone lays it out at its own width
		`max-width:560px`, // one fluid column, the same on a phone and a desktop
		`role="presentation"`,
		`<img src="https://algebra.example/email/algebra-mark.png" width="34" height="34"`,
		`display:none;max-height:0`, // the inbox preview line is not shown in the body
		`<!--[if mso]>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML has %q", want)
		}
	}
	// Mail clients drop scripts, outside stylesheets and web fonts: an email that needs them breaks in some of them.
	for _, never := range []string{"<script", "<link", "@import", "@font-face", "position:fixed", "javascript:"} {
		if strings.Contains(html, never) {
			t.Errorf("the HTML must not use %q", never)
		}
	}
	// Every style a client needs is inline: without the style block the email still has its layout.
	start, end := strings.Index(html, "<style>"), strings.Index(html, "</style>")
	if bare := html[:start] + html[end+len("</style>"):]; !strings.Contains(bare, `style="background:#ffffff;border:1px solid`) {
		t.Error("the card is styled inline")
	}

	// A development address has no public mark to load: the name stands alone.
	if local := sampleDoc().HTML(BrandFor("http://localhost:3400"), "Receipt"); strings.Contains(local, "<img") {
		t.Error("no image is linked from an address a mail client can't reach")
	}
}

func TestEmailDoc_OtherPeoplesTextNeverBecomesMarkup(t *testing.T) {
	d := EmailDoc{
		Heading: `Hi <script>alert(1)</script>`,
		Lead:    []string{`"quoted" & <b>bold</b>`},
		Hero:    &EmailHero{Value: `<img src=x onerror=alert(1)>`, Caption: "x"},
		Rows:    []EmailRow{{Label: "Provider", Value: `evil"><a href="https://phish.example">click</a>`}},
		Button:  &EmailLink{Label: "Go", URL: "javascript:alert(1)"},
		Links:   []EmailLink{{Label: "Data", URL: "data:text/html,x"}, {Label: "Fine", URL: `https://ok.example/?a=1&b="2"`}},
	}
	html := d.HTML(BrandFor("https://algebra.example"), `</title><script>x</script>`)
	for _, never := range []string{"<script", "<b>bold", "onerror=alert(1)>", `<a href="https://phish.example">`, "javascript:", "data:text/html"} {
		if strings.Contains(html, never) {
			t.Errorf("markup or a scheme got through: %q", never)
		}
	}
	if !strings.Contains(html, `href="https://ok.example/?a=1&amp;b=&#34;2&#34;"`) {
		t.Error("an http(s) address is linked, escaped")
	}
	if strings.Contains(html, ">Go<") {
		t.Error("a button with no safe address is not drawn")
	}
}

type htmlOnlyMailer struct {
	mu   sync.Mutex
	sent []struct{ to, subject, text, html, filename string }
}

func (m *htmlOnlyMailer) Send(context.Context, string, string, string) error {
	panic("a mailer that carries HTML is sent HTML")
}

func (m *htmlOnlyMailer) SendHTML(_ context.Context, to, subject, text, htmlBody, filename string, _ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, struct{ to, subject, text, html, filename string }{to, subject, text, htmlBody, filename})
	return nil
}

func TestSendEmail_UsesTheBestTheMailerCanCarry(t *testing.T) {
	ctx, brand, doc := context.Background(), BrandFor("https://algebra.example"), sampleDoc()

	rich := &htmlOnlyMailer{}
	if err := SendEmail(ctx, rich, brand, "ada@example.test", "Receipt", doc, "r.pdf", []byte("%PDF")); err != nil {
		t.Fatal(err)
	}
	if got := rich.sent[0]; !strings.HasPrefix(got.html, "<!doctype html>") || !strings.Contains(got.text, "0.001 USDC") || got.filename != "r.pdf" {
		t.Fatalf("HTML, the text beside it, and the file: %+v", got)
	}

	// A mailer that only attaches gets the text and the file; one that does neither, the text.
	attach := &fakeNotifyMailer{}
	if err := SendEmail(ctx, attach, brand, "ada@example.test", "Receipt", doc, "r.pdf", []byte("%PDF")); err != nil {
		t.Fatal(err)
	}
	if got := attach.all()[0]; got.filename != "r.pdf" || strings.Contains(got.body, "<table") {
		t.Fatalf("text with the file: %+v", got)
	}
	plain := &fakeNotifyMailer{}
	if err := SendEmail(ctx, textOnlyMailer{inner: plain}, brand, "ada@example.test", "Receipt", doc, "r.pdf", []byte("%PDF")); err != nil {
		t.Fatal(err)
	}
	if got := plain.all()[0]; got.filename != "" || !strings.Contains(got.body, "View the signed receipt") {
		t.Fatalf("text alone: %+v", got)
	}
}

// TestEmailPreviews writes each real email as an HTML file, to look at in a
// browser at a phone's width and a desktop's. It runs only when
// ALGEBRA_EMAIL_PREVIEW_DIR names where to put them.
func TestEmailPreviews(t *testing.T) {
	dir := os.Getenv("ALGEBRA_EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set ALGEBRA_EMAIL_PREVIEW_DIR to write the email previews")
	}
	r := newNotifyRig(t)
	rich := &htmlOnlyMailer{}
	r.svc.SetMailer(rich)
	ctx := context.Background()
	meta := account.ClientMeta{UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36", IP: "203.0.113.7"}

	r.svc.SignedIn(ctx, emailUser, meta, false)
	r.svc.Wait()
	r.advance(2 * 60e9)
	r.svc.SignedIn(ctx, emailUser, meta, true)
	r.svc.Wait()
	v := r.paid(emailUser, "eint_a72c5cd6-dde6-42c9-845a-fa567fdf5451", "solana-devnet", 1000)
	r.claims[v.Receipt].Settlement.Transaction = strings.Repeat(sampleSignature, 2)
	r.svc.ReceiptSigned(ctx, v)
	r.svc.Wait()
	if _, err := r.svc.SetEmail(ctx, walletUser, "grace@example.test"); err != nil {
		t.Fatal(err)
	}
	names := []string{"sign-in", "welcome", "receipt", "confirm"}
	if len(rich.sent) != len(names) {
		t.Fatalf("expected %d emails, got %d", len(names), len(rich.sent))
	}
	for i, m := range rich.sent {
		if err := os.WriteFile(filepath.Join(dir, names[i]+".html"), []byte(m.html), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, names[i]+".txt"), []byte("Subject: "+m.subject+"\n\n"+m.text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
