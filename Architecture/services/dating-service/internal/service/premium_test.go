// Premium service tests — Sprint 5. Use TEST_PG_DSN; mock Razorpay.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newPremiumSvcForTest(t *testing.T) (*Service, *store.Store, *payments.MockClient, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping premium service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	if err := st.SeedPremiumPlans(context.Background()); err != nil {
		t.Fatalf("seed plans: %v", err)
	}
	mock := payments.NewMockClient()
	svc := New(st, nil)
	svc.SetRazorpayClient(mock)
	return svc, st, mock, func() { pool.Close() }
}

func TestPremium_ListPlans(t *testing.T) {
	svc, _, _, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	plans, err := svc.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plans) < 4 {
		t.Fatalf("expected >=4 plans, got %d", len(plans))
	}
}

func TestPremium_Checkout_HappyPath(t *testing.T) {
	svc, _, _, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	user := uuid.New()
	resp, err := svc.Checkout(context.Background(), user, CheckoutRequest{PlanID: "monthly_399", Source: "app"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if resp.RazorpayOrderID == "" {
		t.Fatalf("missing razorpay_order_id")
	}
	if resp.RazorpayKeyID == "" {
		t.Fatalf("missing key id")
	}
	if resp.AmountINRPaise != 39900 {
		t.Fatalf("amount mismatch: %d", resp.AmountINRPaise)
	}
}

func TestPremium_Checkout_UnknownPlan(t *testing.T) {
	svc, _, _, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	user := uuid.New()
	_, err := svc.Checkout(context.Background(), user, CheckoutRequest{PlanID: "nope_xyz", Source: "app"})
	if err == nil {
		t.Fatalf("expected ErrPlanNotFound")
	}
}

func TestPremium_HandleWebhook_PaymentCaptured(t *testing.T) {
	svc, st, mock, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	user := uuid.New()

	// 1) checkout flow to create an intent.
	resp, err := svc.Checkout(context.Background(), user, CheckoutRequest{PlanID: "monthly_399", Source: "app"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	// 2) build a webhook body referencing the order id.
	body := buildPaymentCapturedBody(t, "evt_test_001", resp.RazorpayOrderID)
	sig := mock.SignPayload(body)
	res, err := svc.HandleWebhook(context.Background(), sig, body)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if !res.Processed {
		t.Fatalf("expected processed=true, got %+v", res)
	}

	// 3) verify subscription state.
	sub, err := st.GetSubscription(context.Background(), user)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	if sub.Plan != "monthly_399" || sub.AutoRenew != true {
		t.Fatalf("unexpected sub state: %+v", sub)
	}
	premium, _ := st.IsPremium(context.Background(), user)
	if !premium {
		t.Fatalf("user must be premium after webhook")
	}
}

func TestPremium_HandleWebhook_Idempotent(t *testing.T) {
	svc, _, mock, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	user := uuid.New()
	resp, _ := svc.Checkout(context.Background(), user, CheckoutRequest{PlanID: "monthly_399"})
	body := buildPaymentCapturedBody(t, "evt_idem_001", resp.RazorpayOrderID)
	sig := mock.SignPayload(body)

	first, err := svc.HandleWebhook(context.Background(), sig, body)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first.Idempotent {
		t.Fatalf("first call must not be idempotent")
	}
	second, err := svc.HandleWebhook(context.Background(), sig, body)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !second.Idempotent {
		t.Fatalf("second call must be idempotent (no double-charge)")
	}
}

func TestPremium_HandleWebhook_BadSignature(t *testing.T) {
	svc, _, _, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	body := []byte(`{"id":"evt_bad","event":"payment.captured","payload":{}}`)
	_, err := svc.HandleWebhook(context.Background(), "tampered_sig", body)
	if err == nil {
		t.Fatalf("expected signature failure")
	}
}

func TestPremium_HandleWebhook_BoostOneTime(t *testing.T) {
	svc, _, mock, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	// boost requires Redis to grant the token; without redis the helper
	// logs a warn but the rest of the webhook still succeeds.
	user := uuid.New()
	resp, err := svc.Checkout(context.Background(), user, CheckoutRequest{PlanID: "boost_49"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	body := buildPaymentCapturedBody(t, "evt_boost_001", resp.RazorpayOrderID)
	sig := mock.SignPayload(body)
	if _, err := svc.HandleWebhook(context.Background(), sig, body); err != nil {
		t.Fatalf("webhook: %v", err)
	}
}

func TestPremium_CancelSubscription(t *testing.T) {
	svc, st, mock, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	user := uuid.New()

	// upgrade.
	resp, _ := svc.Checkout(context.Background(), user, CheckoutRequest{PlanID: "monthly_399"})
	body := buildPaymentCapturedBody(t, "evt_cancel_001", resp.RazorpayOrderID)
	sig := mock.SignPayload(body)
	if _, err := svc.HandleWebhook(context.Background(), sig, body); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if err := svc.CancelSubscription(context.Background(), user); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	sub, _ := st.GetSubscription(context.Background(), user)
	if sub.AutoRenew {
		t.Fatalf("auto_renew should be false")
	}
	if sub.CancelledAt == nil {
		t.Fatalf("cancelled_at should be set")
	}
}

func TestPremium_MyPremium_NoSubscription(t *testing.T) {
	svc, _, _, cleanup := newPremiumSvcForTest(t)
	defer cleanup()
	out, err := svc.MyPremium(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("my premium: %v", err)
	}
	if out.IsPremium {
		t.Fatalf("fresh user must not be premium")
	}
	if out.Subscription != nil {
		t.Fatalf("expected nil subscription, got %+v", out.Subscription)
	}
}

func TestPremium_NotConfigured(t *testing.T) {
	// Service constructed with no Razorpay client must surface a clear
	// "not configured" error rather than panicking.
	svc := New(nil, nil)
	if _, err := svc.HandleWebhook(context.Background(), "sig", []byte("{}")); err == nil {
		t.Fatalf("expected razorpay-not-configured error")
	}
	// Sanity: nothing panics.
	_ = errors.New("noop")
}

// buildPaymentCapturedBody assembles a webhook envelope.
func buildPaymentCapturedBody(t *testing.T, eventID, orderID string) []byte {
	t.Helper()
	envelope := map[string]any{
		"id":       eventID,
		"event":    "payment.captured",
		"contains": []string{"payment"},
		"payload": map[string]any{
			"payment": map[string]any{
				"entity": map[string]any{
					"id":       "pay_" + eventID,
					"order_id": orderID,
					"status":   "captured",
				},
			},
		},
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
