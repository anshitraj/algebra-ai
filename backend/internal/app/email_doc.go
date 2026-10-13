package app

import (
	"context"
	"html"
	"strings"
)

// EmailDoc is one transactional email as content: a heading, a few facts, one
// action. The same document is laid out twice, as HTML and as plain text, so
// the two never say different things and every email looks like the others.
type EmailDoc struct {
	// Preheader is the line an inbox shows after the subject.
	Preheader string
	// Badge is a short label above the heading ("Devnet: test USDC").
	Badge   string
	Heading string
	// Lead is the paragraphs under the heading.
	Lead []string
	// Hero is one figure set large: the amount of a receipt.
	Hero *EmailHero
	Rows []EmailRow
	// Button is the one thing to do next.
	Button *EmailLink
	// Links are lesser links, shown together under the button.
	Links []EmailLink
	// Notes are smaller paragraphs after the action.
	Notes []string
	// Footer says why the person got this and how to stop it.
	Footer []string
}

// EmailHero is a figure and what it is.
type EmailHero struct{ Value, Caption string }

// EmailRow is one fact. Wide sets a long value (a transaction signature)
// under its label on a line of its own, in a fixed-width face.
type EmailRow struct {
	Label, Value string
	Wide         bool
}

// EmailLink is a label and where it leads. Only http(s) addresses are linked.
type EmailLink struct{ Label, URL string }

// EmailBrand is whose email it is: the name, the site, and the mark shown at
// the top (a PNG the site serves; email clients don't draw SVG).
type EmailBrand struct{ Name, SiteURL, LogoURL string }

// BrandFor is Algebra's brand for a deployment at webURL.
func BrandFor(webURL string) EmailBrand {
	webURL = strings.TrimRight(webURL, "/")
	b := EmailBrand{Name: "Algebra", SiteURL: webURL}
	if strings.HasPrefix(webURL, "https://") {
		// A mail client fetches the mark itself, so it must be on the public
		// site: a development address would only show a broken image.
		b.LogoURL = webURL + "/email/algebra-mark.png"
	}
	return b
}

// HTMLMailer is a Mailer that sends an HTML body beside the plain text, and
// can attach one file. A mailer without it sends the text alone.
type HTMLMailer interface {
	SendHTML(ctx context.Context, to, subject, text, htmlBody, filename string, data []byte) error
}

// SendEmail sends doc to one address in the best form the mailer can carry:
// HTML with the text beside it, else the text (with the file, when it can).
func SendEmail(ctx context.Context, m Mailer, brand EmailBrand, to, subject string, doc EmailDoc, filename string, file []byte) error {
	text := doc.Text(brand)
	if hm, ok := m.(HTMLMailer); ok {
		return hm.SendHTML(ctx, to, subject, text, doc.HTML(brand, subject), filename, file)
	}
	if am, ok := m.(AttachmentMailer); ok && len(file) > 0 {
		return am.SendWithAttachment(ctx, to, subject, text, filename, file)
	}
	return m.Send(ctx, to, subject, text)
}

// Text is the email for a client that shows no HTML, and for the log.
func (d EmailDoc) Text(brand EmailBrand) string {
	var b strings.Builder
	para := func(lines []string) {
		for _, l := range lines {
			b.WriteString(l + "\n\n")
		}
	}
	if d.Badge != "" {
		b.WriteString("[" + d.Badge + "]\n")
	}
	b.WriteString(d.Heading + "\n\n")
	para(d.Lead)
	if d.Hero != nil {
		b.WriteString(strings.TrimSpace(d.Hero.Value+"  "+d.Hero.Caption) + "\n\n")
	}
	for _, r := range d.Rows {
		b.WriteString(r.Label + ": " + r.Value + "\n")
	}
	if len(d.Rows) > 0 {
		b.WriteString("\n")
	}
	if d.Button != nil {
		b.WriteString(d.Button.Label + ":\n" + d.Button.URL + "\n\n")
	}
	for _, l := range d.Links {
		b.WriteString(l.Label + ": " + l.URL + "\n")
	}
	if len(d.Links) > 0 {
		b.WriteString("\n")
	}
	para(d.Notes)
	if len(d.Footer) > 0 {
		b.WriteString("--\n" + strings.Join(d.Footer, "\n") + "\n")
	}
	if brand.Name != "" {
		b.WriteString(strings.TrimSpace(brand.Name+"  "+brand.SiteURL) + "\n")
	}
	return b.String()
}

// The palette and faces of the web app, written out: an email has no
// stylesheet to lean on, and many clients drop anything but inline styles.
const (
	emailPage   = "#f7f7f2"
	emailCard   = "#ffffff"
	emailLine   = "#e6e6de"
	emailInk    = "#1c1d1a"
	emailMuted  = "#6a6c63"
	emailTint   = "#f3f3ec"
	emailAction = "#252620"
	emailAccent = "#87633a"
	emailAccBg  = "#f6eedf"
	emailSans   = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"
	emailMono   = "ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace"
)

// safeHref is u when it is an http(s) address, else nothing: no other scheme
// goes into a link someone will click.
func safeHref(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return html.EscapeString(u)
	}
	return ""
}

// HTML lays the email out for mail clients: tables and inline styles only,
// one fluid column up to 560px wide, so it is the same email in Gmail on a
// phone and on a desktop. Nothing depends on the style block, which only
// tightens the padding on small screens for the clients that read it. Every
// value is escaped: names, provider IDs and addresses are other people's text.
func (d EmailDoc) HTML(brand EmailBrand, title string) string {
	esc := html.EscapeString
	var b strings.Builder
	w := func(s string) { b.WriteString(s) }

	w(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	w(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	w(`<meta name="x-apple-disable-message-reformatting">`)
	w(`<meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light">`)
	w(`<title>` + esc(title) + `</title>`)
	w(`<style>@media (max-width:600px){.card{padding:26px 22px !important}.h1{font-size:21px !important}.hero{font-size:28px !important}}a{color:` + emailInk + `}</style>`)
	w(`</head><body style="margin:0;padding:0;background:` + emailPage + `;-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%;">`)
	if d.Preheader != "" {
		// What the inbox shows beside the subject; the padding keeps the body out of it.
		w(`<div style="display:none;max-height:0;max-width:0;overflow:hidden;opacity:0;font-size:1px;line-height:1px;color:` + emailPage + `;">` + esc(d.Preheader) + strings.Repeat("&#847;&zwnj;&nbsp;", 40) + `</div>`)
	}
	w(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:` + emailPage + `;"><tr><td align="center" style="padding:32px 12px 40px 12px;">`)
	// Outlook's desktop engine ignores max-width: give it a fixed table.
	w(`<!--[if mso]><table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0"><tr><td><![endif]-->`)
	w(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;margin:0 auto;">`)

	// The mark and the name.
	w(`<tr><td style="padding:0 6px 18px 6px;"><table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>`)
	if brand.LogoURL != "" {
		w(`<td style="vertical-align:middle;padding-right:10px;"><img src="` + esc(brand.LogoURL) + `" width="34" height="34" alt="" style="display:block;border:0;outline:none;border-radius:8px;"></td>`)
	}
	w(`<td style="vertical-align:middle;font-family:` + emailSans + `;font-size:18px;line-height:22px;font-weight:600;letter-spacing:-0.2px;color:` + emailInk + `;">` + esc(brand.Name) + `</td>`)
	w(`</tr></table></td></tr>`)

	// The card.
	w(`<tr><td class="card" style="background:` + emailCard + `;border:1px solid ` + emailLine + `;border-radius:14px;padding:34px 36px;">`)
	if d.Badge != "" {
		w(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 14px 0;"><tr><td style="background:` + emailAccBg + `;border-radius:999px;padding:4px 11px;font-family:` + emailSans + `;font-size:12px;line-height:16px;font-weight:600;color:` + emailAccent + `;">` + esc(d.Badge) + `</td></tr></table>`)
	}
	w(`<h1 class="h1" style="margin:0 0 12px 0;font-family:` + emailSans + `;font-size:23px;line-height:30px;font-weight:600;letter-spacing:-0.3px;color:` + emailInk + `;">` + esc(d.Heading) + `</h1>`)
	for _, p := range d.Lead {
		w(`<p style="margin:0 0 14px 0;font-family:` + emailSans + `;font-size:15px;line-height:23px;color:` + emailInk + `;">` + esc(p) + `</p>`)
	}
	if d.Hero != nil {
		w(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:8px 0 6px 0;"><tr><td style="background:` + emailTint + `;border-radius:12px;padding:18px 20px;">`)
		w(`<div class="hero" style="font-family:` + emailSans + `;font-size:32px;line-height:38px;font-weight:600;letter-spacing:-0.6px;color:` + emailInk + `;">` + esc(d.Hero.Value) + `</div>`)
		if d.Hero.Caption != "" {
			w(`<div style="margin-top:4px;font-family:` + emailSans + `;font-size:14px;line-height:20px;color:` + emailMuted + `;">` + esc(d.Hero.Caption) + `</div>`)
		}
		w(`</td></tr></table>`)
	}
	if len(d.Rows) > 0 {
		w(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:14px 0 4px 0;">`)
		for i, r := range d.Rows {
			top := `border-top:1px solid ` + emailLine + `;`
			if i == 0 {
				top = ""
			}
			label := `font-family:` + emailSans + `;font-size:13px;line-height:20px;color:` + emailMuted + `;`
			if r.Wide {
				w(`<tr><td colspan="2" style="` + top + `padding:11px 0;"><div style="` + label + `">` + esc(r.Label) + `</div>`)
				w(`<div style="margin-top:4px;font-family:` + emailMono + `;font-size:12.5px;line-height:19px;color:` + emailInk + `;word-break:break-all;overflow-wrap:anywhere;">` + esc(r.Value) + `</div></td></tr>`)
				continue
			}
			w(`<tr><td width="38%" valign="top" style="` + top + `padding:11px 12px 11px 0;` + label + `">` + esc(r.Label) + `</td>`)
			w(`<td valign="top" align="right" style="` + top + `padding:11px 0;font-family:` + emailSans + `;font-size:14px;line-height:20px;font-weight:500;color:` + emailInk + `;word-break:break-word;overflow-wrap:anywhere;">` + esc(r.Value) + `</td></tr>`)
		}
		w(`</table>`)
	}
	if d.Button != nil {
		if href := safeHref(d.Button.URL); href != "" {
			w(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:22px 0 6px 0;"><tr><td bgcolor="` + emailAction + `" style="background:` + emailAction + `;border-radius:10px;">`)
			w(`<a href="` + href + `" target="_blank" style="display:inline-block;padding:13px 24px;font-family:` + emailSans + `;font-size:15px;line-height:20px;font-weight:600;color:#ffffff;text-decoration:none;border-radius:10px;">` + esc(d.Button.Label) + `</a>`)
			w(`</td></tr></table>`)
		}
	}
	if len(d.Links) > 0 {
		w(`<p style="margin:14px 0 0 0;font-family:` + emailSans + `;font-size:14px;line-height:22px;color:` + emailMuted + `;">`)
		n := 0
		for _, l := range d.Links {
			href := safeHref(l.URL)
			if href == "" {
				continue
			}
			if n > 0 {
				w(`&nbsp;&nbsp;&middot;&nbsp;&nbsp;`)
			}
			w(`<a href="` + href + `" target="_blank" style="color:` + emailInk + `;font-weight:500;text-decoration:underline;">` + esc(l.Label) + `</a>`)
			n++
		}
		w(`</p>`)
	}
	for i, p := range d.Notes {
		margin := "12px 0 0 0"
		if i == 0 {
			margin = "22px 0 0 0"
		}
		w(`<p style="margin:` + margin + `;font-family:` + emailSans + `;font-size:13.5px;line-height:21px;color:` + emailMuted + `;">` + esc(p) + `</p>`)
	}
	w(`</td></tr>`)

	// Why they got it, and whose it is.
	w(`<tr><td style="padding:20px 8px 0 8px;font-family:` + emailSans + `;font-size:12px;line-height:19px;color:` + emailMuted + `;">`)
	for _, p := range d.Footer {
		w(`<p style="margin:0 0 6px 0;">` + esc(p) + `</p>`)
	}
	if brand.Name != "" {
		w(`<p style="margin:10px 0 0 0;">` + esc(brand.Name))
		if href := safeHref(brand.SiteURL); href != "" {
			w(` &middot; <a href="` + href + `" target="_blank" style="color:` + emailMuted + `;text-decoration:underline;">` + esc(strings.TrimPrefix(strings.TrimPrefix(brand.SiteURL, "https://"), "http://")) + `</a>`)
		}
		w(`</p>`)
	}
	w(`</td></tr>`)

	w(`</table><!--[if mso]></td></tr></table><![endif]--></td></tr></table></body></html>`)
	return b.String()
}
