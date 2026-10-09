package mock

import (
	"context"
	"testing"

	"github.com/project-algebra/algebra/internal/domain/merchant"
)

// testFulfillment is what OrderService would hand a connector after
// resolving the caller's shipping alias — the mock refuses to check out
// without one, mirroring a real merchant rejecting an undeliverable order.
func testFulfillment() merchant.Fulfillment {
	return merchant.Fulfillment{Shipping: &merchant.ShippingAddress{
		RecipientName: "Test User", Line1: "1 Test St", City: "Bengaluru", PostalCode: "560001", Country: "IN",
	}}
}

func TestFullCartFlow(t *testing.T) {
	ctx := context.Background()
	c := New()

	products, err := c.SearchProducts(ctx, "coke zero", 5)
	if err != nil || len(products) == 0 {
		t.Fatalf("expected at least one product for 'coke zero', got %v, err=%v", products, err)
	}

	cart, err := c.CreateCart(ctx, "user-1")
	if err != nil {
		t.Fatalf("CreateCart failed: %v", err)
	}
	if _, err := c.AddToCart(ctx, cart.ID, products[0].MerchantProductID, 2); err != nil {
		t.Fatalf("AddToCart failed: %v", err)
	}

	chips, err := c.SearchProducts(ctx, "chips", 5)
	if err != nil || len(chips) == 0 {
		t.Fatalf("expected chips product, got %v, err=%v", chips, err)
	}
	if _, err := c.AddToCart(ctx, cart.ID, chips[0].MerchantProductID, 1); err != nil {
		t.Fatalf("AddToCart(chips) failed: %v", err)
	}

	q, err := c.GetCheckoutQuote(ctx, cart.ID)
	if err != nil {
		t.Fatalf("GetCheckoutQuote failed: %v", err)
	}
	wantSubtotal := int64(2*6000 + 2000) // 2x Coke Zero + 1x chips
	if q.Subtotal.MinorUnits != wantSubtotal {
		t.Errorf("subtotal = %d, want %d", q.Subtotal.MinorUnits, wantSubtotal)
	}
	if q.FinalPayable.MinorUnits <= 0 {
		t.Errorf("expected a positive final payable, got %d", q.FinalPayable.MinorUnits)
	}

	if _, err := c.ApplyCoupon(ctx, cart.ID, "SAVE10"); err != nil {
		t.Fatalf("ApplyCoupon failed: %v", err)
	}
	discounted, err := c.GetCheckoutQuote(ctx, cart.ID)
	if err != nil {
		t.Fatalf("GetCheckoutQuote after coupon failed: %v", err)
	}
	if !discounted.FinalPayable.GreaterThan(discounted.CouponDiscount) {
		t.Fatalf("sanity check failed on discounted quote")
	}
	if discounted.FinalPayable.MinorUnits >= q.FinalPayable.MinorUnits {
		t.Errorf("expected coupon to reduce final payable: before=%d after=%d", q.FinalPayable.MinorUnits, discounted.FinalPayable.MinorUnits)
	}

	result, err := c.ExecuteCheckout(ctx, cart.ID, "appr_test", testFulfillment())
	if err != nil {
		t.Fatalf("ExecuteCheckout failed: %v", err)
	}
	if result.Status != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED, got %s (reason: %s)", result.Status, result.Reason)
	}
	if result.Order == nil {
		t.Fatal("expected an order on success")
	}

	fetched, err := c.GetOrder(ctx, result.Order.MerchantOrderID)
	if err != nil {
		t.Fatalf("GetOrder failed: %v", err)
	}
	if fetched.Total != result.Order.Total {
		t.Errorf("fetched order total mismatch: %v vs %v", fetched.Total, result.Order.Total)
	}
}

func TestApplyCoupon_RejectsUnknownCode(t *testing.T) {
	ctx := context.Background()
	c := New()
	cart, _ := c.CreateCart(ctx, "user-1")
	products, _ := c.SearchProducts(ctx, "pasta", 1)
	_, _ = c.AddToCart(ctx, cart.ID, products[0].MerchantProductID, 1)

	if _, err := c.ApplyCoupon(ctx, cart.ID, "FAKECODE"); err == nil {
		t.Error("expected an unknown coupon code to be rejected")
	}
}

func TestGetCheckoutQuote_EmptyCartErrors(t *testing.T) {
	ctx := context.Background()
	c := New()
	cart, _ := c.CreateCart(ctx, "user-1")
	if _, err := c.GetCheckoutQuote(ctx, cart.ID); err == nil {
		t.Error("expected an error for an empty cart")
	}
}

func TestCancelOrder(t *testing.T) {
	ctx := context.Background()
	c := New()
	cart, _ := c.CreateCart(ctx, "user-1")
	products, _ := c.SearchProducts(ctx, "pasta", 1)
	_, _ = c.AddToCart(ctx, cart.ID, products[0].MerchantProductID, 1)
	result, err := c.ExecuteCheckout(ctx, cart.ID, "appr_test", testFulfillment())
	if err != nil {
		t.Fatalf("ExecuteCheckout failed: %v", err)
	}

	if err := c.CancelOrder(ctx, result.Order.MerchantOrderID); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}
	fetched, err := c.GetOrder(ctx, result.Order.MerchantOrderID)
	if err != nil {
		t.Fatalf("GetOrder failed: %v", err)
	}
	if fetched.Status != "CANCELLED" {
		t.Errorf("expected CANCELLED status, got %s", fetched.Status)
	}
}
