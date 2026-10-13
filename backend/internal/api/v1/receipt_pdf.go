package v1

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/receipt"
	"github.com/project-algebra/algebra/internal/platform/pdf"
)

// getEconomicReceiptPDF: GET /api/v1/economic-intents/{id}/receipt.pdf, for
// the intent's person (session) or one of their agents and API keys. It is
// the signed receipt laid out for a person: what was bought, from whom, for
// how much, on which network with a link to the transaction, how the
// provider was chosen and under which authority, whether Algebra's
// signature checks out, and the signed receipt itself so anyone can verify
// it again. It is a receipt for a paid call, not a tax invoice.
func (a *API) getEconomicReceiptPDF(w http.ResponseWriter, r *http.Request) {
	if !a.economic(w) {
		return
	}
	v, ok := a.econOwner(w, r)
	if !ok {
		return
	}
	if v.Receipt == "" {
		writeJSON(w, http.StatusNotFound, errorBody{Error: "no receipt yet: one is signed when the intent commits (state " + string(v.State) + ")"})
		return
	}
	var ver *app.IntentVerification
	if a.b.IntentReceipts != nil {
		ver = a.b.IntentReceipts.Verify(r.Context(), v.Receipt)
	}
	body := receiptPDF(v, ver, a.b.AuthConfig.PublicWebURL)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="algebra-receipt-`+safeFileName(v.ID)+`.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}

// usdcText formats minor units of USDC ("0.002 USDC").
func usdcText(m receipt.Money) string {
	whole := float64(m.MinorUnits) / 1e6
	s := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(whole, 'f', 6, 64), "0"), ".")
	return s + " " + strings.ToUpper(cmpOr(m.Currency, "USDC"))
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// networkText says which network a payment was on, and whether it was money.
func networkText(n string, test bool) string {
	switch n {
	case "solana":
		return "Solana mainnet"
	case "solana-devnet":
		return "Solana devnet (test USDC, no real value)"
	case "sandbox", "":
		if test || n == "sandbox" {
			return "Algebra sandbox (simulated: no money moved)"
		}
	}
	return n
}

// explorerLinks are the transaction on Solana Explorer and Solscan, for the
// two clusters that have them.
func explorerLinks(network, tx string) [][2]string { return app.ExplorerLinks(network, tx) }

func modeText(m string) string {
	switch strings.ToUpper(m) {
	case "CHEAPEST":
		return "Cheapest"
	case "FASTEST":
		return "Fastest"
	case "AUTO":
		return "Balanced"
	}
	return m
}

// receiptPDF lays a committed intent's receipt out on one A4 page.
func receiptPDF(v *app.IntentView, ver *app.IntentVerification, publicURL string) []byte {
	doc := &pdf.Document{Title: "Algebra receipt " + v.ID}
	const left, valueX, right = 50.0, 190.0, pdf.PageWidth - 50
	page := doc.AddPage()
	y := pdf.PageHeight - 60.0
	next := func(step float64) {
		y -= step
		if y < 60 {
			page = doc.AddPage()
			y = pdf.PageHeight - 60
		}
	}

	page.Text(left, y, pdf.HelveticaBold, 20, 0, "Algebra")
	page.Text(left+90, y+1, pdf.Helvetica, 12, 0.35, "Receipt for a paid API call")
	next(18)
	page.Text(left, y, pdf.Helvetica, 8.5, 0.45, "Signed by Algebra. This is a receipt for a payment, not a tax invoice.")
	next(10)
	page.Rule(left, y, right, y, 0.8)
	next(22)

	var c *receipt.IntentClaims
	if ver != nil {
		c = ver.Claims
	}
	net, tx, payer, payTo := "", "", "", ""
	amount := receipt.Money{MinorUnits: v.CommittedMinor, Currency: v.Currency}
	test := false
	if c != nil {
		test = c.Test
		if s := c.Settlement; s != nil {
			net, tx, payer, payTo, amount = s.Network, s.Transaction, s.Payer, s.PayTo, s.Amount
		}
	}
	if test || net == "sandbox" || net == "solana-devnet" {
		banner := "TEST: no real money moved."
		if net == "solana-devnet" {
			banner = "DEVNET: paid in test USDC, which has no real value."
		}
		page.Text(left, y, pdf.HelveticaBold, 10, 0.2, banner)
		next(20)
	}

	row := func(label, value string, f pdf.Font, size float64) {
		page.Text(left, y, pdf.Helvetica, 8.5, 0.45, label)
		lines := pdf.Wrap(f, size, right-valueX, value)
		for i, l := range lines {
			if i > 0 {
				next(size + 3)
			}
			page.Text(valueX, y, f, size, 0, l)
		}
		next(16)
	}
	linkRow := func(label, title, uri string) {
		page.Text(left, y, pdf.Helvetica, 8.5, 0.45, label)
		page.Text(valueX, y, pdf.Helvetica, 10, 0.1, title)
		page.Link(valueX-2, y-3, valueX+pdf.Width(pdf.Helvetica, 10, title)+2, y+10, uri)
		page.Rule(valueX, y-1.5, valueX+pdf.Width(pdf.Helvetica, 10, title), y-1.5, 0.3)
		next(16)
	}

	row("Amount paid", usdcText(amount), pdf.HelveticaBold, 13)
	next(2)
	provider := ""
	if c != nil {
		provider = c.Provider.ID
	}
	row("Paid to", cmpOr(provider, "-"), pdf.Helvetica, 10)
	row("What was bought", v.Capability, pdf.Helvetica, 10)
	row("Network", networkText(net, test), pdf.Helvetica, 10)
	if tx != "" {
		row("Transaction", tx, pdf.Courier, 8.5)
		for _, l := range explorerLinks(net, tx) {
			linkRow("", "View on "+l[0], l[1])
		}
	}
	if payTo != "" {
		row("Payee address", payTo, pdf.Courier, 8.5)
	}
	if payer != "" {
		row("Paid from", payer, pdf.Courier, 8.5)
	}
	row("Budget for this call", usdcText(receipt.Money{MinorUnits: v.BudgetMaxMinor, Currency: v.Currency}), pdf.Helvetica, 10)
	if c != nil {
		if c.Routing != nil {
			how := modeText(c.Routing.Mode)
			if c.Routing.Fallback {
				how += fmt.Sprintf(" (fallback, choice %d)", c.Routing.Rank)
			} else if c.Routing.Rank > 0 {
				how += fmt.Sprintf(" (choice %d)", c.Routing.Rank)
			}
			row("How it was chosen", how, pdf.Helvetica, 10)
		}
		auth := "Within the Spend Pass's limits"
		if c.Authority.Method == receipt.MethodHuman {
			auth = "Approved by the account owner"
		}
		if c.Authority.PassID != "" {
			auth += " · Spend Pass " + c.Authority.PassID
		}
		row("Authority", auth, pdf.Helvetica, 10)
		result := c.Execution.Status
		if q := c.Execution.Quality; q != nil && q.Score != nil {
			result += fmt.Sprintf(" · quality %.0f/100", *q.Score)
		}
		row("Result", strings.ReplaceAll(result, "_", " "), pdf.Helvetica, 10)
		row("Final state", strings.ToLower(c.Final.Lifecycle+" · "+c.Final.Commitment+" · "+c.Final.Fulfillment), pdf.Helvetica, 10)
		if c.Coordination.DuplicateCommitAttemptsBlocked > 0 {
			row("Duplicates stopped", strconv.Itoa(c.Coordination.DuplicateCommitAttemptsBlocked), pdf.Helvetica, 10)
		}
		row("Receipt ID", c.ID, pdf.Courier, 8.5)
		row("Signed at", time.Unix(c.IssuedAt, 0).UTC().Format("2 January 2006, 15:04:05 UTC"), pdf.Helvetica, 10)
	}
	row("Intent", v.ID, pdf.Courier, 8.5)

	check := "Not checked"
	if ver != nil {
		switch {
		case ver.Valid && ver.Recorded:
			check = "Valid: signed by this Algebra and on its record"
		case ver.Valid:
			check = "Valid signature"
		default:
			check = cmpOr(ver.Reason, "The signature does not check out")
		}
	}
	row("Signature", check, pdf.HelveticaBold, 10)
	if publicURL != "" {
		linkRow("Verify it yourself", strings.TrimPrefix(strings.TrimPrefix(publicURL, "https://"), "http://")+"/verify", publicURL+"/verify")
	}

	next(6)
	page.Rule(left, y+8, right, y+8, 0.8)
	next(6)
	page.Text(left, y, pdf.HelveticaBold, 9, 0, "Signed receipt")
	next(12)
	page.Text(left, y, pdf.Helvetica, 8, 0.45, "An Ed25519 JWS. Paste it at the address above, or check it against the keys at /.well-known/jwks.json.")
	next(13)
	for _, l := range pdf.Wrap(pdf.Courier, 6.2, right-left, v.Receipt) {
		page.Text(left, y, pdf.Courier, 6.2, 0.15, l)
		next(7.6)
	}
	return doc.Bytes()
}
