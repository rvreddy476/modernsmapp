package payments

import (
	"context"
	"strings"
	"testing"
)

func TestMockClient_CreateOrder_Deterministic(t *testing.T) {
	m := NewMockClient()
	o1, err := m.CreateOrder(context.Background(), 39900, "rcpt-1", nil)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if !strings.HasPrefix(o1.ID, "order_mock_") {
		t.Fatalf("expected order_mock_ prefix, got %s", o1.ID)
	}
	if o1.Amount != 39900 || o1.Currency != "INR" {
		t.Fatalf("unexpected order: %+v", o1)
	}
	o2, _ := m.CreateOrder(context.Background(), 49, "rcpt-2", nil)
	if o1.ID == o2.ID {
		t.Fatalf("order ids must differ")
	}
}

func TestMockClient_CreateOrder_Invalid(t *testing.T) {
	m := NewMockClient()
	if _, err := m.CreateOrder(context.Background(), 0, "rcpt", nil); err == nil {
		t.Fatalf("expected error on zero amount")
	}
	if _, err := m.CreateOrder(context.Background(), -1, "rcpt", nil); err == nil {
		t.Fatalf("expected error on negative amount")
	}
}

func TestMockClient_CreateSubscription(t *testing.T) {
	m := NewMockClient()
	s, err := m.CreateSubscription(context.Background(), "plan_monthly_399", 12, nil)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if !strings.HasPrefix(s.ID, "sub_mock_") {
		t.Fatalf("unexpected sub id: %s", s.ID)
	}
	if s.PlanID != "plan_monthly_399" || s.TotalCount != 12 {
		t.Fatalf("unexpected sub: %+v", s)
	}
	if _, err := m.CreateSubscription(context.Background(), "", 0, nil); err == nil {
		t.Fatalf("expected error on empty plan id")
	}
}

func TestMockClient_VerifyWebhookSignature_HappyPath(t *testing.T) {
	m := NewMockClient()
	payload := []byte(`{"event":"payment.captured"}`)
	sig := m.SignPayload(payload)
	if err := m.VerifyWebhookSignature(payload, sig); err != nil {
		t.Fatalf("expected verification to pass: %v", err)
	}
}

func TestMockClient_VerifyWebhookSignature_Mismatch(t *testing.T) {
	m := NewMockClient()
	payload := []byte(`{"event":"payment.captured"}`)
	if err := m.VerifyWebhookSignature(payload, "deadbeef"); err == nil {
		t.Fatalf("expected mismatch error")
	}
	if err := m.VerifyWebhookSignature(payload, ""); err == nil {
		t.Fatalf("expected missing signature error")
	}
}

func TestMockClient_VerifyHook_Override(t *testing.T) {
	m := NewMockClient()
	called := false
	m.VerifyHook = func(payload []byte, signature string) error {
		called = true
		return nil
	}
	if err := m.VerifyWebhookSignature([]byte("x"), "ignored"); err != nil {
		t.Fatalf("hook should override: %v", err)
	}
	if !called {
		t.Fatalf("verify hook not invoked")
	}
}

func TestHTTPClient_VerifyWebhookSignature(t *testing.T) {
	c := NewHTTPClient("rzp_test_x", "secret_x", "whsec_real", "")
	payload := []byte(`{"event":"payment.captured","id":"evt_1"}`)
	// Compute expected via the mock helper using a parallel client.
	mock := NewMockClient()
	mock.webhookSecret = "whsec_real"
	sig := mock.SignPayload(payload)
	if err := c.VerifyWebhookSignature(payload, sig); err != nil {
		t.Fatalf("expected match: %v", err)
	}
	if err := c.VerifyWebhookSignature(payload, "tampered"); err == nil {
		t.Fatalf("expected mismatch error")
	}
}

func TestHTTPClient_VerifyWebhookSignature_NoSecret(t *testing.T) {
	c := NewHTTPClient("rzp_test_x", "secret_x", "", "")
	if err := c.VerifyWebhookSignature([]byte("x"), "anything"); err == nil {
		t.Fatalf("expected error when secret unconfigured")
	}
}

func TestMockClient_FetchPayment_Default(t *testing.T) {
	m := NewMockClient()
	p, err := m.FetchPayment(context.Background(), "pay_test_1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if p.ID != "pay_test_1" || p.Status != "captured" {
		t.Fatalf("unexpected default payment: %+v", p)
	}
}
