package v1

import (
	"strings"
	"testing"

	"github.com/project-algebra/algebra/internal/app"
	"github.com/project-algebra/algebra/internal/domain/econ"
	"github.com/project-algebra/algebra/internal/domain/receipt"
)

// pdfText is the readable part of the generated PDF: its text and link strings.
func pdfText(b []byte) string { return string(b) }

func devnetClaims(funding *receipt.FundingRef) *app.IntentVerification {
	return &app.IntentVerification{Valid: true, Recorded: true, Claims: &receipt.IntentClaims{
		ID: "ircpt_test", IssuedAt: 1_700_000_000, Test: true,
		Provider: receipt.ProviderRef{ID: "demo:skycast"},
		Settlement: &receipt.SettlementRef{
			Rail: "x402-solana-devnet", Network: "solana-devnet", Amount: receipt.Money{MinorUnits: 1_200, Currency: "USDC"},
			Transaction: "FakePaymentTransaction111", Payer: "FakePayerKey222", PayTo: "FakePayeeAddress333", Funding: funding,
		},
		Final: receipt.FinalState{Lifecycle: "committed", Commitment: "settled", Fulfillment: "fulfilled"},
	}}
}

func devnetIntent() *app.IntentView {
	in := econ.Intent{ID: "eint_test", Capability: "weather.forecast", Currency: "USDC", BudgetMaxMinor: 10_000, CommittedMinor: 1_200}
	return &app.IntentView{Intent: in, Receipt: "fake.jws.value"}
}

// A vault payment's PDF keeps the exact facts a person (or their accountant)
// checks on chain: the vault, its owner, the pull and the payment, with links.
func TestReceiptPDFShowsTheVaultAndBothTransactions(t *testing.T) {
	ver := devnetClaims(&receipt.FundingRef{
		Source: "spend_pass_vault", Vault: "FakeVaultAddress444", Owner: "FakeOwnerWallet555", PullSignature: "FakePullTransaction666",
		Pulled: receipt.Money{MinorUnits: 1_200, Currency: "USDC"},
	})
	text := pdfText(receiptPDF(devnetIntent(), ver, "https://algebra.test"))
	for _, want := range []string{
		"Funded from", "FakeVaultAddress444", "Vault owner", "FakeOwnerWallet555",
		"Vault transaction", "FakePullTransaction666",
		"solscan.io/tx/FakePullTransaction666?cluster=devnet",
		"Payment signed by", "FakePayerKey222",
		"FakePaymentTransaction111", "solscan.io/tx/FakePaymentTransaction111?cluster=devnet",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the PDF should contain %q", want)
		}
	}
	if strings.Contains(text, "Paid from") {
		t.Error("a vault payment is not 'paid from' the signer's own wallet")
	}
}

func TestReceiptPDFWithoutAVaultKeepsTheOldLine(t *testing.T) {
	text := pdfText(receiptPDF(devnetIntent(), devnetClaims(nil), "https://algebra.test"))
	if !strings.Contains(text, "Paid from") || strings.Contains(text, "Funded from") {
		t.Error("no vault: the receipt says what it always said")
	}
}
