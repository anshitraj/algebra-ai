package vault

import (
	"context"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/payment"
)

func TestSandboxProvider_TokenizeNeverStoresRawInput(t *testing.T) {
	p := NewSandboxProvider()
	result, err := p.Tokenize(context.Background(), payment.TokenizeRequest{
		UserID: "user-1", ProviderNonce: "nonce-abc-123", Alias: "payment:personal", Nickname: "My Card",
	})
	if err != nil {
		t.Fatalf("Tokenize failed: %v", err)
	}
	if result.Source.ProviderTokenRef == "nonce-abc-123" {
		t.Error("the vault-issued token must not equal the raw input nonce")
	}
	if len(result.Source.Last4) != 4 {
		t.Errorf("expected a 4-digit last4, got %q", result.Source.Last4)
	}
	if !result.Source.Capabilities.CanPay {
		t.Error("expected a freshly tokenized source to be payable")
	}
}

func TestSandboxProvider_ListSourcesScopedToUser(t *testing.T) {
	p := NewSandboxProvider()
	ctx := context.Background()
	_, _ = p.Tokenize(ctx, payment.TokenizeRequest{UserID: "user-1", ProviderNonce: "n1", Alias: "payment:personal"})
	_, _ = p.Tokenize(ctx, payment.TokenizeRequest{UserID: "user-2", ProviderNonce: "n2", Alias: "payment:personal"})

	sources, err := p.ListSources(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListSources failed: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected exactly 1 source for user-1, got %d", len(sources))
	}
	if sources[0].UserID != "user-1" {
		t.Errorf("expected source scoped to user-1, got %s", sources[0].UserID)
	}
}

func TestSandboxProvider_RevokeSource(t *testing.T) {
	p := NewSandboxProvider()
	ctx := context.Background()
	result, _ := p.Tokenize(ctx, payment.TokenizeRequest{UserID: "user-1", ProviderNonce: "n1", Alias: "payment:personal"})

	if err := p.RevokeSource(ctx, result.Source.ID); err != nil {
		t.Fatalf("RevokeSource failed: %v", err)
	}
	sources, _ := p.ListSources(ctx, "user-1")
	if sources[0].RevokedAt == nil {
		t.Error("expected RevokedAt to be set after revocation")
	}
}

func TestSandboxProvider_Mode(t *testing.T) {
	p := NewSandboxProvider()
	if p.Mode() != payment.ProviderModeSandbox {
		t.Errorf("expected sandbox mode, got %s", p.Mode())
	}
}
