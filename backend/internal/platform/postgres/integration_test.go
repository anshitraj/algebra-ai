package postgres

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/project-algebra/algebra/internal/domain/agent"
	"github.com/project-algebra/algebra/internal/domain/approval"
	"github.com/project-algebra/algebra/internal/domain/audit"
	"github.com/project-algebra/algebra/internal/domain/intent"
	"github.com/project-algebra/algebra/internal/domain/money"
	"github.com/project-algebra/algebra/internal/domain/order"
	"github.com/project-algebra/algebra/internal/domain/payment"
	"github.com/project-algebra/algebra/internal/domain/privacy"
	"github.com/project-algebra/algebra/internal/domain/quote"
	"github.com/project-algebra/algebra/policy"
)

// requireDB skips the test unless a real DATABASE_URL is configured — these
// tests exercise real SQL against real Postgres (`make dev-up` first), not
// a mock. See docs/LOCAL_DEVELOPMENT.md.
func requireDB(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test (see docs/LOCAL_DEVELOPMENT.md)")
	}
	ctx := context.Background()
	db, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}
	if err := db.Migrate(ctx, "../../../migrations"); err != nil {
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func mustCreateUser(t *testing.T, db *DB) string {
	t.Helper()
	id := "user_" + uuid.NewString()
	if err := NewUserRepo(db).Create(context.Background(), id, id+"@example.test", "", time.Now()); err != nil {
		t.Fatalf("creating test user: %v", err)
	}
	return id
}

func TestIntentRepo_CreateGetUpdate(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	agentID := mustCreateTestAgent(t, db, userID)
	repo := NewIntentRepo(db)

	pi := intent.New("pi_"+uuid.NewString(), userID, agentID,
		[]intent.Item{{Query: "coke zero", Quantity: 2}},
		intent.Constraints{MaxTotalMinorUnits: 40000, Currency: "INR", PaymentProfile: "payment:personal", DeliveryProfile: "shipping:home"},
		time.Now())

	if err := repo.Create(ctx, pi); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.Get(ctx, pi.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != intent.StateDraft || len(got.Items) != 1 || got.Items[0].Query != "coke zero" {
		t.Errorf("unexpected round-tripped intent: %+v", got)
	}

	if _, err := got.ApplyTransition(intent.StateDiscovering, time.Now()); err != nil {
		t.Fatalf("ApplyTransition failed: %v", err)
	}
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	reloaded, err := repo.Get(ctx, pi.ID)
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if reloaded.Status != intent.StateDiscovering {
		t.Errorf("expected DISCOVERING after update, got %s", reloaded.Status)
	}
}

func mustCreateTestAgent(t *testing.T, db *DB, userID string) string {
	t.Helper()
	_, hash, err := agent.GenerateToken()
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	id := &agent.Identity{
		ID: "agent_" + uuid.NewString(), UserID: userID, ClientID: "test", Name: "test-agent",
		Permissions: agent.AllPermissions, TokenHash: hash, CreatedAt: time.Now(),
	}
	if err := NewAgentRepo(db).Create(context.Background(), id); err != nil {
		t.Fatalf("creating test agent: %v", err)
	}
	return id.ID
}

func TestAgentRepo_CreateGetByTokenHashRevoke(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	repo := NewAgentRepo(db)

	raw, hash, err := agent.GenerateToken()
	if err != nil {
		t.Fatalf("generating token: %v", err)
	}
	id := &agent.Identity{
		ID: "agent_" + uuid.NewString(), UserID: userID, ClientID: "test", Name: "test-agent",
		Permissions: []agent.Permission{agent.PermShoppingRead, agent.PermShoppingExecute}, TokenHash: hash, CreatedAt: time.Now(),
	}
	if err := repo.Create(ctx, id); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByTokenHash(ctx, agent.HashToken(raw))
	if err != nil {
		t.Fatalf("GetByTokenHash failed: %v", err)
	}
	if got.ID != id.ID || len(got.Permissions) != 2 {
		t.Errorf("unexpected agent: %+v", got)
	}
	if got.IsRevoked() {
		t.Error("freshly created agent should not be revoked")
	}

	if err := repo.Revoke(ctx, id.ID, time.Now()); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	reloaded, err := repo.Get(ctx, id.ID)
	if err != nil {
		t.Fatalf("Get after revoke failed: %v", err)
	}
	if !reloaded.IsRevoked() {
		t.Error("expected agent to be revoked")
	}
}

func TestApprovalRepo_MarkConsumedIsAtomic(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	agentID := mustCreateTestAgent(t, db, userID)
	intentRepo := NewIntentRepo(db)
	pi := intent.New("pi_"+uuid.NewString(), userID, agentID, []intent.Item{{Query: "x", Quantity: 1}},
		intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"}, time.Now())
	if err := intentRepo.Create(ctx, pi); err != nil {
		t.Fatalf("creating intent: %v", err)
	}

	repo := NewApprovalRepo(db)
	now := time.Now()
	a := &approval.Approval{
		ID: "appr_" + uuid.NewString(), IntentID: pi.ID, QuoteID: "quote_x", UserID: userID, AgentID: agentID,
		Merchant: "mock", Amount: money.Amount{MinorUnits: 5000, Currency: "INR"}, PaymentSourceAlias: "payment:personal",
		ItemsHash: "hash1", Status: approval.StatusApproved, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Simulate two concurrent execution attempts racing to consume the same
	// approval — exactly one must win (mandate §32: no double execution).
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			claimed, err := repo.MarkConsumed(ctx, a.ID)
			if err != nil {
				t.Error(err)
			}
			results <- claimed
		}()
	}
	claimedCount := 0
	for i := 0; i < 2; i++ {
		if <-results {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Errorf("expected exactly 1 winner of the consume race, got %d", claimedCount)
	}
}

func TestPrivacyProfileRepo_RoundTripThroughResolver(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)

	keyB64 := os.Getenv("ALGEBRA_MASTER_KEY")
	if keyB64 == "" {
		t.Skip("ALGEBRA_MASTER_KEY not set")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decoding master key: %v", err)
	}
	enc, err := privacy.NewAESGCMEncryptor(key)
	if err != nil {
		t.Fatalf("building encryptor: %v", err)
	}

	repo := NewPrivacyProfileRepo(db)
	auditRepo := NewAuditRepo(db)
	resolver := privacy.NewResolver(repo, enc, &ResolutionAuditSink{Logger: auditRepo, Now: time.Now})

	alias := "shipping:home:" + uuid.NewString()
	profile := privacy.ShippingProfile{RecipientName: "Test User", Line1: "1 Test St", City: "Bengaluru", State: "KA", PostalCode: "560001", Country: "IN", Phone: "+910000000000"}
	if err := resolver.StoreShipping(ctx, "profile_"+uuid.NewString(), userID, alias, profile, time.Now()); err != nil {
		t.Fatalf("StoreShipping failed: %v", err)
	}

	got, err := resolver.ResolveShipping(ctx, userID, alias, privacy.ResolveAuthorization{Purpose: "test", RequestedBy: "test"})
	if err != nil {
		t.Fatalf("ResolveShipping failed: %v", err)
	}
	if *got != profile {
		t.Errorf("round-tripped profile mismatch: got %+v, want %+v", *got, profile)
	}
}

func TestOrderRepo_CreateAndSpendLedger(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	agentID := mustCreateTestAgent(t, db, userID)

	pi := intent.New("pi_"+uuid.NewString(), userID, agentID, []intent.Item{{Query: "x", Quantity: 1}},
		intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"}, time.Now())
	if err := NewIntentRepo(db).Create(ctx, pi); err != nil {
		t.Fatalf("creating intent: %v", err)
	}
	a := &approval.Approval{
		ID: "appr_" + uuid.NewString(), IntentID: pi.ID, QuoteID: "q1", UserID: userID, AgentID: agentID,
		Merchant: "mock", Amount: money.Amount{MinorUnits: 9000, Currency: "INR"}, PaymentSourceAlias: "payment:personal",
		ItemsHash: "h1", Status: approval.StatusApproved, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := NewApprovalRepo(db).Create(ctx, a); err != nil {
		t.Fatalf("creating approval: %v", err)
	}

	ord := &order.Order{
		ID: "ord_" + uuid.NewString(), IntentID: pi.ID, ApprovalID: a.ID, Merchant: "mock", MerchantOrderID: "mockorder_1",
		Items: []order.Item{{MerchantProductID: "sku-1", Name: "Test Item", Quantity: 1, UnitPrice: money.Amount{MinorUnits: 9000, Currency: "INR"}}},
		Total: money.Amount{MinorUnits: 9000, Currency: "INR"}, Status: order.StatusPlaced, ProviderMode: "mock", PlacedAt: time.Now(),
	}
	if err := NewOrderRepo(db).Create(ctx, ord); err != nil {
		t.Fatalf("creating order: %v", err)
	}

	fetched, err := NewOrderRepo(db).GetByIntent(ctx, pi.ID)
	if err != nil {
		t.Fatalf("GetByIntent failed: %v", err)
	}
	if fetched.Total.MinorUnits != 9000 {
		t.Errorf("expected total 9000, got %d", fetched.Total.MinorUnits)
	}

	spend, err := NewSpendLedgerRepo(db).SpendToday(ctx, userID, "INR", time.Now())
	if err != nil {
		t.Fatalf("SpendToday failed: %v", err)
	}
	if spend < 9000 {
		t.Errorf("expected today's spend to include the 9000 order, got %d", spend)
	}
}

func TestIdempotencyRepo_BeginCompleteReplay(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	repo := NewIdempotencyRepo(db)
	key := "idem_" + uuid.NewString()

	existing, done, err := repo.Begin(ctx, key, "test-scope")
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if done || existing != nil {
		t.Fatalf("expected a fresh reservation, got done=%v existing=%v", done, existing)
	}

	if err := repo.Complete(ctx, key, "test-scope", []byte(`{"result":"ok"}`)); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	replay, done2, err := repo.Begin(ctx, key, "test-scope")
	if err != nil {
		t.Fatalf("Begin (replay) failed: %v", err)
	}
	if !done2 {
		t.Fatal("expected the second Begin to report already-completed")
	}
	if string(replay) != `{"result":"ok"}` {
		t.Errorf("expected replayed response, got %s", replay)
	}
}

func TestPolicyDecisionRepo_SaveAndGetLatest(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	agentID := mustCreateTestAgent(t, db, userID)
	pi := intent.New("pi_"+uuid.NewString(), userID, agentID, []intent.Item{{Query: "x", Quantity: 1}},
		intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"}, time.Now())
	if err := NewIntentRepo(db).Create(ctx, pi); err != nil {
		t.Fatalf("creating intent: %v", err)
	}

	repo := NewPolicyDecisionRepo(db)
	dec := &policy.PolicyDecision{Decision: policy.Allow, ReasonCodes: []string{"AMOUNT_OK"}, PolicyVersion: "v1", EvaluatedAt: time.Now()}
	if err := repo.Save(ctx, pi.ID, dec); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := repo.GetLatestByIntent(ctx, pi.ID)
	if err != nil {
		t.Fatalf("GetLatestByIntent failed: %v", err)
	}
	if got.Decision != policy.Allow || len(got.ReasonCodes) != 1 {
		t.Errorf("unexpected decision: %+v", got)
	}
}

func TestPaymentSourceRepo_CreateListRevoke(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	repo := NewPaymentSourceRepo(db)

	src := &payment.PaymentSource{
		ID: "src_" + uuid.NewString(), UserID: userID, Alias: "payment:personal", Type: payment.SourceCard,
		ProviderTokenRef: "sandbox_tok_xyz", Network: "visa", Last4: "4242",
		Capabilities: payment.Capabilities{CanPay: true, SupportedCurrencies: []string{"INR"}}, CreatedAt: time.Now(),
	}
	if err := repo.Create(ctx, src); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByAlias(ctx, userID, "payment:personal")
	if err != nil {
		t.Fatalf("GetByAlias failed: %v", err)
	}
	if got.Last4 != "4242" || got.ProviderTokenRef != "sandbox_tok_xyz" {
		t.Errorf("unexpected payment source: %+v", got)
	}

	if err := repo.Revoke(ctx, src.ID, time.Now()); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	reloaded, err := repo.GetByAlias(ctx, userID, "payment:personal")
	if err != nil {
		t.Fatalf("GetByAlias after revoke failed: %v", err)
	}
	if reloaded.RevokedAt == nil {
		t.Error("expected RevokedAt to be set")
	}
}

func TestAuditRepo_RejectsForbiddenMetadataKey(t *testing.T) {
	db := requireDB(t)
	repo := NewAuditRepo(db)
	evt := audit.NewEvent("TestEvent", time.Now())
	evt.Metadata = map[string]any{"card_number": "4111111111111111"}
	if err := repo.Record(context.Background(), evt); err == nil {
		t.Error("expected Record to reject an event with a forbidden metadata key")
	}
}

func TestAuditEventsTable_IsAppendOnlyAtTheDatabaseLevel(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	repo := NewAuditRepo(db)
	evt := audit.NewEvent("TestImmutableEvent", time.Now())
	if err := repo.Record(ctx, evt); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	_, err := db.Pool.Exec(ctx, `UPDATE audit_events SET action = 'tampered' WHERE id = $1`, evt.EventID)
	if err == nil {
		t.Error("expected the database trigger to reject an UPDATE on audit_events")
	}

	_, err = db.Pool.Exec(ctx, `DELETE FROM audit_events WHERE id = $1`, evt.EventID)
	if err == nil {
		t.Error("expected the database trigger to reject a DELETE on audit_events")
	}
}

func TestQuoteRepo_SaveGetListByIntent(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	userID := mustCreateUser(t, db)
	agentID := mustCreateTestAgent(t, db, userID)
	pi := intent.New("pi_"+uuid.NewString(), userID, agentID, []intent.Item{{Query: "x", Quantity: 1}},
		intent.Constraints{MaxTotalMinorUnits: 10000, Currency: "INR"}, time.Now())
	if err := NewIntentRepo(db).Create(ctx, pi); err != nil {
		t.Fatalf("creating intent: %v", err)
	}

	repo := NewQuoteRepo(db)
	now := time.Now()
	q := &quote.CheckoutQuote{
		QuoteID: "quote_" + uuid.NewString(), CartID: "cart_1", Merchant: "mock",
		Subtotal: money.Amount{MinorUnits: 5000, Currency: "INR"}, RetrievedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}
	q.Recompute()
	if err := repo.Save(ctx, pi.ID, q); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := repo.Get(ctx, q.QuoteID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.CartID != "cart_1" || got.FinalPayable.MinorUnits != q.FinalPayable.MinorUnits {
		t.Errorf("unexpected round-tripped quote: %+v", got)
	}

	list, err := repo.ListByIntent(ctx, pi.ID)
	if err != nil {
		t.Fatalf("ListByIntent failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 quote, got %d", len(list))
	}
}
