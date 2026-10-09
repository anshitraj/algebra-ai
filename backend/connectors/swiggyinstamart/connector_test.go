package swiggyinstamart

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/project-algebra/algebra/connectors/remotemcp"
	"github.com/project-algebra/algebra/internal/domain/merchant"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/shared"
	"golang.org/x/oauth2"
)

const testToken = "swiggy-test-token"

type variant struct {
	spin, sku, name, brand, size string
	price, mrp                   float64
	inStock, promoted            bool
}

// fakeSwiggy is a genuine MCP server (go-sdk) whose tools return payloads in
// the shapes Swiggy publishes for Instamart. The protocol and the connector
// are exercised for real; only the merchant behind them is a stand-in.
type fakeSwiggy struct {
	srv *httptest.Server

	mu            sync.Mutex
	catalog       []variant
	cart          map[string]int
	cartOrder     []string
	codAvailable  bool
	smallCartFee  bool
	checkoutReply map[string]any
	checkoutCalls []map[string]any
	allArgs       []string
}

func ok(data any) map[string]any { return map[string]any{"success": true, "data": data} }

func failure(msg string) map[string]any {
	return map[string]any{"success": false, "error": map[string]any{"message": msg}}
}

func newFakeSwiggy(t *testing.T, omit ...string) *fakeSwiggy {
	t.Helper()
	f := &fakeSwiggy{
		catalog: []variant{
			{spin: "spin_milk_1l", sku: "sku_milk", name: "Amul Taaza Toned Milk", brand: "Amul", size: "1 L", price: 54, mrp: 56, inStock: true},
			{spin: "spin_chips", sku: "sku_chips", name: "Lay's Classic Salted", brand: "Lay's", size: "52 g", price: 20, mrp: 20, inStock: true, promoted: true},
			{spin: "spin_oos", sku: "sku_oos", name: "Aged Cheddar", brand: "Go", size: "200 g", price: 300, mrp: 320},
		},
		cart:         map[string]int{},
		codAvailable: true,
	}
	skip := map[string]bool{}
	for _, name := range omit {
		skip[name] = true
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-swiggy-instamart", Version: "1.0.0"}, nil)
	add := func(name string, props []string, h func(map[string]any) map[string]any) {
		if skip[name] {
			return
		}
		properties := map[string]any{}
		for _, p := range props {
			properties[p] = map[string]any{}
		}
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object", "properties": properties}},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := map[string]any{}
				if len(req.Params.Arguments) > 0 {
					if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
						return nil, err
					}
				}
				f.mu.Lock()
				f.allArgs = append(f.allArgs, string(req.Params.Arguments))
				env := h(args)
				f.mu.Unlock()
				raw, err := json.Marshal(env)
				if err != nil {
					return nil, err
				}
				return &mcp.CallToolResult{StructuredContent: env, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
			})
	}
	add("get_addresses", []string{"page", "pageSize"}, f.getAddresses)
	add("search_products", []string{"addressId", "query", "offset"}, f.searchProducts)
	add("update_cart", []string{"selectedAddressId", "items"}, f.updateCart)
	add("clear_cart", nil, f.clearCart)
	add("get_cart", nil, func(map[string]any) map[string]any { return ok(f.cartView()) })
	add("get_payment_options", []string{"addressId"}, f.paymentOptions)
	add("checkout", []string{"addressId", "paymentMethod", "intentApp", "generateUPIQR"}, f.checkout)
	add("get_orders", []string{"count", "orderType", "activeOnly"}, f.getOrders)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSwiggy) getAddresses(map[string]any) map[string]any {
	return ok(map[string]any{
		"addresses": []map[string]any{
			{"id": "addr_home", "addressLine": "Flat 4, 12 MG Road, Indiranagar, Bengaluru 560038", "phoneNumber": "9999999999", "addressTag": "Home"},
			{"id": "addr_work", "addressLine": "Tower B, Powai, Mumbai 400076", "phoneNumber": "9999999999", "addressTag": "Work"},
		},
		"pagination": map[string]any{"page": 1, "pageSize": 10, "total": 2, "totalPages": 1, "hasMore": false},
	})
}

func (f *fakeSwiggy) searchProducts(args map[string]any) map[string]any {
	if args["addressId"] != "addr_home" {
		return failure("Invalid addressId")
	}
	var products []map[string]any
	for i, v := range f.catalog {
		products = append(products, map[string]any{
			"displayName": v.name, "brand": v.brand, "inStock": v.inStock, "isAvail": true,
			"productId": fmt.Sprintf("p%d", i), "parentProductId": fmt.Sprintf("pp%d", i), "isPromoted": v.promoted,
			"variations": []map[string]any{{
				"spinId": v.spin, "skuId": v.sku, "quantityDescription": v.size, "displayName": v.name, "brandName": v.brand,
				"price":                 map[string]any{"mrp": v.mrp, "offerPrice": v.price},
				"isInStockAndAvailable": v.inStock,
				"sla":                   map[string]any{"value": "10", "unit": "MINS"},
			}},
		})
	}
	return ok(map[string]any{"nextOffset": "", "products": products})
}

func (f *fakeSwiggy) variant(spin string) (variant, bool) {
	for _, v := range f.catalog {
		if v.spin == spin {
			return v, true
		}
	}
	return variant{}, false
}

// updateCart replaces the whole cart, as Swiggy documents, silently dropping
// lines that are out of stock.
func (f *fakeSwiggy) updateCart(args map[string]any) map[string]any {
	if args["selectedAddressId"] != "addr_home" {
		return failure("Invalid selectedAddressId")
	}
	items, _ := args["items"].([]any)
	f.cart, f.cartOrder = map[string]int{}, nil
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		spin, _ := item["spinId"].(string)
		qty, _ := item["quantity"].(float64)
		v, known := f.variant(spin)
		if !known || !v.inStock || qty <= 0 {
			continue
		}
		if _, seen := f.cart[spin]; !seen {
			f.cartOrder = append(f.cartOrder, spin)
		}
		f.cart[spin] += int(qty)
	}
	return ok(f.cartView())
}

func (f *fakeSwiggy) clearCart(map[string]any) map[string]any {
	f.cart, f.cartOrder = map[string]int{}, nil
	return ok(map[string]any{})
}

func (f *fakeSwiggy) cartView() map[string]any {
	if len(f.cartOrder) == 0 {
		return map[string]any{"cartAbsent": true, "cartAbsentReason": "Cart is empty", "items": []any{}, "cartTotalAmount": "0",
			"billBreakdown": map[string]any{"lineItems": []any{}, "toPay": map[string]any{"label": "To Pay", "value": "₹0"}}}
	}
	var items []map[string]any
	var itemTotal float64
	for _, spin := range f.cartOrder {
		v, _ := f.variant(spin)
		qty := f.cart[spin]
		items = append(items, map[string]any{"spinId": spin, "skuId": v.sku, "productId": "p", "itemName": v.name, "itemVariant": v.size,
			"quantity": qty, "isInStockAndAvailable": true, "mrp": v.mrp, "discountedFinalPrice": v.price})
		itemTotal += v.price * float64(qty)
	}
	lines := []map[string]any{
		{"label": "Item Total", "value": fmt.Sprintf("₹%.2f", itemTotal)},
		{"label": "Delivery Fee", "value": "₹25"},
		{"label": "Handling Fee", "value": "₹4"},
		{"label": "Item Discount", "value": "-₹10"},
	}
	toPay := itemTotal + 25 + 4 - 10
	if f.smallCartFee {
		lines = append(lines, map[string]any{"label": "Small Cart Charge", "value": "₹15"})
		toPay += 15
	}
	return map[string]any{
		"selectedAddress":         "addr_home",
		"selectedAddressDetails":  map[string]any{"id": "addr_home", "address": "Flat 4, 12 MG Road", "area": "Indiranagar", "name": "Test User", "mobile": "9999999999"},
		"cartTotalAmount":         fmt.Sprintf("%.2f", toPay),
		"items":                   items,
		"billBreakdown":           map[string]any{"lineItems": lines, "toPay": map[string]any{"label": "To Pay", "value": fmt.Sprintf("₹%.2f", toPay)}},
		"cartId":                  "cart_1",
		"availablePaymentMethods": []string{"Cash", "UPI"},
	}
}

func (f *fakeSwiggy) paymentOptions(map[string]any) map[string]any {
	return ok(map[string]any{
		"cod":                map[string]any{"available": f.codAvailable, "id": "cod", "displayName": "Cash on Delivery"},
		"allMethods":         []any{},
		"placeOrderToolName": "checkout",
	})
}

func (f *fakeSwiggy) checkout(args map[string]any) map[string]any {
	f.checkoutCalls = append(f.checkoutCalls, args)
	if f.checkoutReply != nil {
		return f.checkoutReply
	}
	return ok(map[string]any{"orderId": "IM123456", "status": "CONFIRMED", "paymentMethod": args["paymentMethod"], "cartTotal": 127, "addressId": args["addressId"]})
}

func (f *fakeSwiggy) getOrders(map[string]any) map[string]any {
	return ok(map[string]any{"hasMore": false, "orders": []map[string]any{
		{"orderId": "IM123456", "status": "DELIVERED", "createdAt": "2026-09-10T10:00:00Z", "updatedAt": "2026-09-10T10:20:00Z", "itemCount": 1,
			"totalAmount": 127, "orderType": "DASH", "isActive": false, "currentStatus": "Delivered", "historyStatus": "DELIVERED",
			"items": []map[string]any{{"name": "Amul Taaza Toned Milk", "quantity": 2}}},
		{"orderId": "IM777", "status": "ORDER_PLACED", "createdAt": "2026-09-10T11:00:00Z", "updatedAt": "2026-09-10T11:01:00Z", "itemCount": 1,
			"totalAmount": 40, "orderType": "DASH", "isActive": true, "currentStatus": "Packing your order", "historyStatus": "",
			"items": []map[string]any{{"name": "Lay's Classic Salted", "quantity": 2}}},
	}})
}

var linkedSettings = map[string]string{SettingAddressID: "addr_home", SettingShippingAlias: "shipping:home"}

func connectorFor(t *testing.T, f *fakeSwiggy, settings map[string]string) *Connector {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	store := remotemcp.NewFileSessionStore(t.TempDir(), enc)
	if settings != nil {
		if err := store.Save(&remotemcp.Session{
			Merchant: Name, Endpoint: f.srv.URL, ClientID: "client",
			Token:    &oauth2.Token{AccessToken: testToken, TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
			Settings: settings, LinkedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	client, err := remotemcp.NewClient(remotemcp.Config{Merchant: Name, Endpoint: f.srv.URL, Store: store, AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return New(client)
}

func readyConnector(t *testing.T, f *fakeSwiggy) *Connector {
	t.Helper()
	c := connectorFor(t, f, linkedSettings)
	if err := c.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	return c
}

func homeFulfillment(pin string) merchant.Fulfillment {
	return merchant.Fulfillment{Shipping: &merchant.ShippingAddress{
		RecipientName: "Test User", Line1: "Flat 4, 12 MG Road", City: "Bengaluru", PostalCode: pin, Country: "IN", Phone: "+919999999999",
	}}
}

// milkCart makes the same connector calls DiscoveryService.discoverOne does:
// search, create cart, add the best match, then price delivery for alias.
func milkCart(t *testing.T, c *Connector, alias string) (cartID string, deliveryErr error) {
	t.Helper()
	ctx := context.Background()
	products, err := c.SearchProducts(ctx, "milk", 5)
	if err != nil || len(products) == 0 {
		t.Fatalf("SearchProducts: %v, %v", products, err)
	}
	cart, err := c.CreateCart(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddToCart(ctx, cart.ID, products[0].MerchantProductID, 2); err != nil {
		t.Fatalf("AddToCart: %v", err)
	}
	_, deliveryErr = c.GetDeliveryOptions(ctx, cart.ID, alias)
	return cart.ID, deliveryErr
}

func (f *fakeSwiggy) checkoutCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checkoutCalls)
}

func TestNotLinked_NothingEnabledAndCheckoutNeedsUser(t *testing.T) {
	f := newFakeSwiggy(t)
	c := connectorFor(t, f, nil)
	if err := c.Warm(context.Background()); err != nil {
		t.Fatalf("Warm with no linked account should not error: %v", err)
	}
	if c.Capabilities() != (merchant.Capabilities{}) {
		t.Fatalf("expected no capabilities, got %+v", c.Capabilities())
	}
	st := c.Status()
	if st.Ready || !strings.Contains(st.Detail, "merchant-login") || st.Integration != merchant.IntegrationOfficialMCP {
		t.Fatalf("unexpected status %+v", st)
	}
	res, err := c.ExecuteCheckout(context.Background(), "cart", "approval", homeFulfillment("560038"))
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded {
		t.Fatalf("expected USER_INTERVENTION_REQUIRED, got %+v, %v", res, err)
	}
}

func TestLinkedWithoutAddress_NothingEnabled(t *testing.T) {
	f := newFakeSwiggy(t)
	c := connectorFor(t, f, map[string]string{SettingShippingAlias: "shipping:home"})
	if err := c.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Capabilities() != (merchant.Capabilities{}) || !strings.Contains(c.Status().Detail, "delivery address") {
		t.Fatalf("expected setup-needed status, got %+v / %+v", c.Capabilities(), c.Status())
	}
}

func TestContractMismatch_DisablesEverything(t *testing.T) {
	f := newFakeSwiggy(t, "checkout")
	c := connectorFor(t, f, linkedSettings)
	var ce *remotemcp.ContractError
	if err := c.Warm(context.Background()); !errors.As(err, &ce) {
		t.Fatalf("expected a contract error, got %v", err)
	}
	if c.Capabilities() != (merchant.Capabilities{}) {
		t.Fatalf("a contract mismatch must disable every capability, got %+v", c.Capabilities())
	}
	if !strings.Contains(c.Status().Detail, "checkout") {
		t.Fatalf("status should name the missing tool: %q", c.Status().Detail)
	}
}

func TestSearchProducts_MapsPublishedShape(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	caps := c.Capabilities()
	if !caps.Search || !caps.Cart || !caps.Checkout || !caps.OrderTracking || caps.Coupons {
		t.Fatalf("unexpected capabilities %+v", caps)
	}

	products, err := c.SearchProducts(context.Background(), "milk", 10)
	if err != nil || len(products) != 3 {
		t.Fatalf("SearchProducts: %v, %v", products, err)
	}
	milk, chips, cheese := products[0], products[1], products[2]
	if milk.MerchantProductID != "spin_milk_1l|sku_milk" || milk.PriceMinorUnits != 5400 || milk.Currency != "INR" ||
		milk.Size != "1 L" || milk.Brand != "Amul" || !milk.Available || milk.Merchant != Name {
		t.Fatalf("milk mapped wrong: %+v", milk)
	}
	if milk.DeliveryETA == nil || time.Until(*milk.DeliveryETA) < 9*time.Minute || time.Until(*milk.DeliveryETA) > 11*time.Minute {
		t.Fatalf("expected a ~10 minute ETA, got %v", milk.DeliveryETA)
	}
	if math.Abs(milk.Confidence-0.8) > 1e-9 || math.Abs(chips.Confidence-0.55) > 1e-9 {
		t.Fatalf("rank confidence wrong: milk %v, promoted chips %v", milk.Confidence, chips.Confidence)
	}
	if cheese.Available {
		t.Fatal("out-of-stock variant reported available")
	}
	if one, _ := c.SearchProducts(context.Background(), "milk", 1); len(one) != 1 {
		t.Fatalf("limit not applied: %d", len(one))
	}
}

func TestCODOrder_EndToEnd(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	ctx := context.Background()

	cartID, err := milkCart(t, c, "shipping:home")
	if err != nil {
		t.Fatalf("GetDeliveryOptions: %v", err)
	}
	q, err := c.GetCheckoutQuote(ctx, cartID)
	if err != nil {
		t.Fatalf("GetCheckoutQuote: %v", err)
	}
	// 2 × ₹54 = ₹108, + ₹25 delivery + ₹4 handling − ₹10 discount = ₹127.
	if q.FinalPayable.MinorUnits != 12700 || q.Subtotal.MinorUnits != 10800 || q.DeliveryFee.MinorUnits != 2500 ||
		q.HandlingFee.MinorUnits != 400 || q.ItemDiscounts.MinorUnits != 1000 || q.OtherFee.MinorUnits != 0 || q.FinalPayable.Currency != "INR" {
		t.Fatalf("quote mapped wrong: %+v", q)
	}
	if len(q.PaymentSourceRequirements) != 1 || q.PaymentSourceRequirements[0] != PaymentRequirementCOD {
		t.Fatalf("quote must say it is Cash on Delivery: %v", q.PaymentSourceRequirements)
	}

	res, err := c.ExecuteCheckout(ctx, cartID, "approval-1", homeFulfillment("560038"))
	if err != nil || res.Status != merchant.ExecutionSucceeded {
		t.Fatalf("ExecuteCheckout: %+v, %v", res, err)
	}
	if res.Order.MerchantOrderID != "IM123456" || res.Order.Total.MinorUnits != 12700 || res.Order.Status != order.StatusPlaced {
		t.Fatalf("order mapped wrong: %+v", res.Order)
	}

	f.mu.Lock()
	calls, sent := f.checkoutCalls, strings.Join(f.allArgs, "\n")
	f.mu.Unlock()
	if len(calls) != 1 || calls[0]["paymentMethod"] != "COD" || calls[0]["addressId"] != "addr_home" {
		t.Fatalf("checkout called wrong: %v", calls)
	}
	// Swiggy orders against its saved address ID: the resolved address from
	// Algebra's privacy layer must never be put on the wire.
	for _, private := range []string{"MG Road", "9999999999", "Test User", "560038"} {
		if strings.Contains(sent, private) {
			t.Fatalf("resolved shipping data %q was sent to Swiggy", private)
		}
	}
	if _, err := c.GetCheckoutQuote(ctx, cartID); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("a checked-out cart must be gone, got %v", err)
	}
}

func TestQuote_ReconcilesUnlabelledFees(t *testing.T) {
	f := newFakeSwiggy(t)
	f.smallCartFee = true
	c := readyConnector(t, f)
	cartID, _ := milkCart(t, c, "shipping:home")
	q, err := c.GetCheckoutQuote(context.Background(), cartID)
	if err != nil {
		t.Fatal(err)
	}
	if q.FinalPayable.MinorUnits != 14200 || q.OtherFee.MinorUnits != 1500 {
		t.Fatalf("unlabelled fee must land in OtherFee and FinalPayable must equal To Pay: %+v", q)
	}
}

func TestCartEditedInSwiggyApp_Refused(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	cartID, _ := milkCart(t, c, "shipping:home")

	f.mu.Lock()
	f.cart["spin_milk_1l"] = 5 // the user changed the quantity in the Swiggy app
	f.mu.Unlock()

	if _, err := c.GetCheckoutQuote(context.Background(), cartID); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("expected a cart-drift error, got %v", err)
	}
	res, err := c.ExecuteCheckout(context.Background(), cartID, "approval", homeFulfillment("560038"))
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded {
		t.Fatalf("expected USER_INTERVENTION_REQUIRED, got %+v, %v", res, err)
	}
	if f.checkoutCount() != 0 {
		t.Fatal("checkout must not be called for a drifted cart")
	}
}

func TestShippingAliasNotLinked_CheckoutRefused(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	cartID, err := milkCart(t, c, "shipping:work")
	if err == nil {
		t.Fatal("an alias that isn't linked to a Swiggy address must be rejected")
	}
	res, err := c.ExecuteCheckout(context.Background(), cartID, "approval", homeFulfillment("560038"))
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded || !strings.Contains(res.Reason, "shipping alias") {
		t.Fatalf("expected alias refusal, got %+v, %v", res, err)
	}
	if f.checkoutCount() != 0 {
		t.Fatal("checkout must not be called")
	}
}

func TestPINMismatch_CheckoutRefused(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	cartID, _ := milkCart(t, c, "shipping:home")
	res, err := c.ExecuteCheckout(context.Background(), cartID, "approval", homeFulfillment("400001"))
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded || !strings.Contains(res.Reason, "PIN") {
		t.Fatalf("expected PIN refusal, got %+v, %v", res, err)
	}
	if f.checkoutCount() != 0 {
		t.Fatal("checkout must not be called")
	}
}

func TestCODUnavailable_CheckoutRefused(t *testing.T) {
	f := newFakeSwiggy(t)
	f.codAvailable = false
	c := readyConnector(t, f)
	cartID, _ := milkCart(t, c, "shipping:home")
	res, err := c.ExecuteCheckout(context.Background(), cartID, "approval", homeFulfillment("560038"))
	if err != nil || res.Status != merchant.ExecutionUserInterventionNeeded || !strings.Contains(res.Reason, "Cash on Delivery") {
		t.Fatalf("expected COD refusal, got %+v, %v", res, err)
	}
	if f.checkoutCount() != 0 {
		t.Fatal("checkout must not be called")
	}
}

func TestCheckoutOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		reply  map[string]any
		status merchant.ExecutionStatus
		reason string
	}{
		{
			name:   "declined",
			reply:  failure("Store closed for the night"),
			status: merchant.ExecutionFailed,
			reason: "Store closed",
		},
		{
			name: "multi-store partial",
			reply: ok(map[string]any{"orders": []map[string]any{{"orderId": "IM1", "status": "CONFIRMED"}, {"error": "store closed"}},
				"orderCount": 2, "successCount": 1, "failureCount": 1, "allSucceeded": false, "paymentMethod": "COD"}),
			status: merchant.ExecutionMerchantInterventionNeeded,
			reason: "IM1",
		},
		{
			name: "multi-store success",
			reply: ok(map[string]any{"orders": []map[string]any{{"orderId": "IM1", "status": "CONFIRMED"}, {"orderId": "IM2", "status": "CONFIRMED"}},
				"orderCount": 2, "successCount": 2, "failureCount": 0, "allSucceeded": true, "paymentMethod": "COD"}),
			status: merchant.ExecutionSucceeded,
		},
		{
			name:   "unexpected online payment",
			reply:  ok(map[string]any{"orderId": "IM9", "transactionId": "t", "paasId": "paas-1", "status": "PENDING_PAYMENT", "paymentMethod": "UPI"}),
			status: merchant.ExecutionMerchantInterventionNeeded,
			reason: "online payment",
		},
		{
			name:   "no order id",
			reply:  ok(map[string]any{"status": "CONFIRMED"}),
			status: merchant.ExecutionMerchantInterventionNeeded,
			reason: "no usable order ID",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSwiggy(t)
			f.checkoutReply = tc.reply
			c := readyConnector(t, f)
			cartID, _ := milkCart(t, c, "shipping:home")
			res, err := c.ExecuteCheckout(context.Background(), cartID, "approval", homeFulfillment("560038"))
			if err != nil || res.Status != tc.status || !strings.Contains(res.Reason, tc.reason) {
				t.Fatalf("got %+v, %v", res, err)
			}
			if tc.status == merchant.ExecutionSucceeded && res.Order.MerchantOrderID != "IM1,IM2" {
				t.Fatalf("multi-store order ids: %q", res.Order.MerchantOrderID)
			}
		})
	}
}

func TestAddToCart_OutOfStockIsAnError(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	ctx := context.Background()
	cart, _ := c.CreateCart(ctx, "user-1")
	if _, err := c.AddToCart(ctx, cart.ID, "spin_oos|sku_oos", 1); err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Fatalf("expected refusal for an out-of-stock item, got %v", err)
	}
}

func TestGetOrder_CombinesMultiStoreStatus(t *testing.T) {
	f := newFakeSwiggy(t)
	c := readyConnector(t, f)
	ctx := context.Background()

	one, err := c.GetOrder(ctx, "IM123456")
	if err != nil || one.Status != order.StatusDelivered || one.Total.MinorUnits != 12700 {
		t.Fatalf("single order: %+v, %v", one, err)
	}
	both, err := c.GetOrder(ctx, "IM123456,IM777")
	if err != nil || both.Status != order.StatusPlaced || both.Total.MinorUnits != 16700 {
		t.Fatalf("combined order should report the least-advanced part: %+v, %v", both, err)
	}
	if _, err := c.GetOrder(ctx, "IM000"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCancelOrder_IsHonestlyUnsupported(t *testing.T) {
	c := New(nil)
	if err := c.CancelOrder(context.Background(), "IM1"); !errors.Is(err, shared.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestParseDisplayAmount(t *testing.T) {
	good := map[string]int64{"₹1,234.50": 123450, "-₹20": -2000, "FREE": 0, "₹ 45": 4500, "127.00": 12700}
	for in, want := range good {
		if got, err := parseDisplayAmount(in); err != nil || got != want {
			t.Errorf("parseDisplayAmount(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1.2.3", "5-3"} {
		if _, err := parseDisplayAmount(in); err == nil {
			t.Errorf("parseDisplayAmount(%q) should fail", in)
		}
	}
}

func TestProductIDRoundTrip(t *testing.T) {
	spin, sku, err := decodeProductID(encodeProductID("spin|with pipe", "sku/1"))
	if err != nil || spin != "spin|with pipe" || sku != "sku/1" {
		t.Fatalf("round trip: %q %q %v", spin, sku, err)
	}
	if _, _, err := decodeProductID("|sku"); err == nil {
		t.Fatal("an empty spin id must be rejected")
	}
}

func TestPINCompatible(t *testing.T) {
	cases := []struct {
		line, pin string
		want      bool
	}{
		{"12 MG Road, Bengaluru 560038", "560038", true},
		{"12 MG Road, Bengaluru 560038", "400001", false},
		{"12 MG Road, Bengaluru", "400001", true}, // nothing to compare
		{"12 MG Road, Bengaluru 560038", "", true},
	}
	for _, tc := range cases {
		if got := pinCompatible(tc.line, tc.pin); got != tc.want {
			t.Errorf("pinCompatible(%q, %q) = %v", tc.line, tc.pin, got)
		}
	}
}
