// Payments store integration tests. Skipped when TEST_PG_DSN is not set.
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func paymentsTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping payments store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	s := New(pool)
	if err := s.SeedPremiumPlans(context.Background()); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
	return s, func() { pool.Close() }
}

func TestPayments_SeedPlans_ListActive(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	plans, err := s.ListActivePlans(context.Background())
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(plans) < 4 {
		t.Fatalf("expected at least 4 seeded plans, got %d", len(plans))
	}
	seen := map[string]bool{}
	for _, p := range plans {
		seen[p.ID] = true
	}
	for _, want := range []string{"monthly_399", "quarterly_999", "yearly_2499", "boost_49"} {
		if !seen[want] {
			t.Fatalf("missing seeded plan %s", want)
		}
	}
}

func TestPayments_GetPlan_NotFound(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	_, err := s.GetPlan(context.Background(), "nonexistent_plan_xyz")
	if err == nil {
		t.Fatalf("expected ErrPlanNotFound")
	}
}

func TestPayments_CreatePaymentIntent_HappyPath(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	orderID := "order_test_" + userID.String()[:8]
	intent, err := s.CreatePaymentIntent(context.Background(), userID, "monthly_399", orderID, "app", 39900)
	if err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if intent.UserID != userID || intent.PlanID != "monthly_399" || intent.Status != "created" {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	if intent.AmountINRPaise != 39900 {
		t.Fatalf("amount mismatch: %d", intent.AmountINRPaise)
	}
}

func TestPayments_CreatePaymentIntent_DuplicateOrderIDFails(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	orderID := "order_dupe_" + userID.String()[:8]
	if _, err := s.CreatePaymentIntent(context.Background(), userID, "monthly_399", orderID, "app", 39900); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := s.CreatePaymentIntent(context.Background(), userID, "monthly_399", orderID, "app", 39900); err == nil {
		t.Fatalf("expected unique-violation on duplicate order id")
	}
}

func TestPayments_RecordPaymentEvent_Idempotent(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	intent, err := s.CreatePaymentIntent(context.Background(), userID, "monthly_399",
		"order_evt_"+userID.String()[:8], "app", 39900)
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	eventID := "evt_test_" + userID.String()[:8]
	payload := []byte(`{"event":"payment.captured","payment_id":"pay_1"}`)

	inserted, err := s.RecordPaymentEvent(context.Background(), &intent.ID, eventID, "payment.captured", payload)
	if err != nil {
		t.Fatalf("record event 1: %v", err)
	}
	if !inserted {
		t.Fatalf("first delivery should insert")
	}
	// Replay — must not double-insert.
	inserted2, err := s.RecordPaymentEvent(context.Background(), &intent.ID, eventID, "payment.captured", payload)
	if err != nil {
		t.Fatalf("record event 2: %v", err)
	}
	if inserted2 {
		t.Fatalf("replayed delivery must not insert")
	}
}

func TestPayments_MarkIntentPaid(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	intent, err := s.CreatePaymentIntent(context.Background(), userID, "monthly_399",
		"order_paid_"+userID.String()[:8], "app", 39900)
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	if err := s.MarkPaymentIntentPaid(context.Background(), intent.ID, time.Now()); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	got, err := s.GetPaymentIntent(context.Background(), intent.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "paid" || got.PaidAt == nil {
		t.Fatalf("expected paid+timestamp, got %+v", got)
	}
}

func TestPayments_UpsertSubscription_ExtendsExpiry(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	now := time.Now()
	first := now.Add(30 * 24 * time.Hour)
	if err := s.UpsertSubscription(context.Background(), userID, "monthly_399", "monthly_399", nil, "app", &now, &first, true); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	got, err := s.GetSubscription(context.Background(), userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ExpiresAt == nil || got.ExpiresAt.Before(first.Add(-time.Second)) {
		t.Fatalf("expected expiry %v got %v", first, got.ExpiresAt)
	}
	// A re-charge: expiry should roll forward, never backward.
	earlier := first.Add(-5 * 24 * time.Hour)
	if err := s.UpsertSubscription(context.Background(), userID, "monthly_399", "monthly_399", nil, "app", &now, &earlier, true); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got2, _ := s.GetSubscription(context.Background(), userID)
	if got2.ExpiresAt.Before(*got.ExpiresAt) {
		t.Fatalf("expiry must not roll backward; was %v became %v", got.ExpiresAt, got2.ExpiresAt)
	}
}

func TestPayments_MarkSubscriptionCancelled(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	now := time.Now()
	exp := now.Add(30 * 24 * time.Hour)
	if err := s.UpsertSubscription(context.Background(), userID, "monthly_399", "monthly_399", nil, "app", &now, &exp, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.MarkSubscriptionCancelled(context.Background(), userID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, _ := s.GetSubscription(context.Background(), userID)
	if got.AutoRenew {
		t.Fatalf("auto_renew should be false after cancel")
	}
	if got.CancelledAt == nil {
		t.Fatalf("cancelled_at should be set")
	}
	// expires_at unchanged.
	if got.ExpiresAt == nil {
		t.Fatalf("expires_at must remain set after cancel")
	}
}

func TestPayments_IsPremium_AfterUpsert(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	if ok, err := s.IsPremium(context.Background(), userID); err != nil || ok {
		t.Fatalf("non-subscriber must not be premium, got ok=%v err=%v", ok, err)
	}
	now := time.Now()
	exp := now.Add(30 * 24 * time.Hour)
	_ = s.UpsertSubscription(context.Background(), userID, "monthly_399", "monthly_399", nil, "app", &now, &exp, true)
	if ok, err := s.IsPremium(context.Background(), userID); err != nil || !ok {
		t.Fatalf("after upsert IsPremium must be true, got ok=%v err=%v", ok, err)
	}
}

func TestPayments_RecordConsent(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	userID := uuid.New()
	if err := s.RecordConsent(context.Background(), userID, "echoes", true, "v1.0-2026-04-29"); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	if err := s.RecordConsent(context.Background(), userID, "echoes", false, "v1.0-2026-04-29"); err != nil {
		t.Fatalf("record consent toggle off: %v", err)
	}
	entries, err := s.ListConsentForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 consent rows (audit trail), got %d", len(entries))
	}
}

func TestPayments_RecordPaymentEvent_InvalidJSON(t *testing.T) {
	s, cleanup := paymentsTestStore(t)
	defer cleanup()
	if _, err := s.RecordPaymentEvent(context.Background(), nil, "evt_bad", "payment.captured", []byte("{not json")); err == nil {
		t.Fatalf("expected invalid json error")
	}
}
