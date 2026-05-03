package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestHandleSetuWebhook_SucceededFlow(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	provID := h.seedProvider(t, "PROV-wh-1", "electricity", "Test Prov")

	res, err := h.svc.Pay(ctx, uid, PayRequest{
		ProviderID: provID, Identifier: "1234567890",
		AmountPaise: 25000, PaymentMethod: "wallet", IdempotencyKey: "wh-pay-1",
	})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if err := h.svc.HandleSetuWebhook(ctx, SetuWebhookEvent{
		SetuPaymentRef: res.SetuPaymentRef,
		Status:         "succeeded",
		ReceiptNumber:  "RRN-OK",
	}); err != nil {
		t.Fatalf("webhook succeeded: %v", err)
	}
	pmt, _ := h.store.GetPayment(ctx, uid, res.PaymentID)
	if pmt.Status != "succeeded" {
		t.Fatalf("expected succeeded; got %q", pmt.Status)
	}
}

func TestHandleSetuWebhook_FailedFlow_RefundsWallet(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	provID := h.seedProvider(t, "PROV-wh-2", "electricity", "Test Prov")

	res, err := h.svc.Pay(ctx, uid, PayRequest{
		ProviderID: provID, Identifier: "1234567890",
		AmountPaise: 33000, PaymentMethod: "wallet", IdempotencyKey: "wh-pay-2",
	})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	priorRefunds := len(h.wallet.Refunds())
	if err := h.svc.HandleSetuWebhook(ctx, SetuWebhookEvent{
		SetuPaymentRef: res.SetuPaymentRef,
		Status:         "failed",
		FailureReason:  "biller-down",
	}); err != nil {
		t.Fatalf("webhook failed: %v", err)
	}
	pmt, _ := h.store.GetPayment(ctx, uid, res.PaymentID)
	if pmt.Status != "failed" {
		t.Fatalf("expected failed; got %q", pmt.Status)
	}
	if got := len(h.wallet.Refunds()); got != priorRefunds+1 {
		t.Fatalf("expected refund issued on failed webhook; before=%d after=%d", priorRefunds, got)
	}
}

func TestHandleSetuWebhook_AlreadyTerminal_NoOp(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	provID := h.seedProvider(t, "PROV-wh-3", "electricity", "Test Prov")
	res, _ := h.svc.Pay(ctx, uid, PayRequest{
		ProviderID: provID, Identifier: "1234567890",
		AmountPaise: 10000, PaymentMethod: "wallet", IdempotencyKey: "wh-pay-3",
	})
	if err := h.svc.HandleSetuWebhook(ctx, SetuWebhookEvent{
		SetuPaymentRef: res.SetuPaymentRef, Status: "succeeded",
	}); err != nil {
		t.Fatalf("first webhook: %v", err)
	}
	priorRefunds := len(h.wallet.Refunds())
	// Replay with failed — should be a no-op (already succeeded).
	if err := h.svc.HandleSetuWebhook(ctx, SetuWebhookEvent{
		SetuPaymentRef: res.SetuPaymentRef, Status: "failed",
	}); err != nil {
		t.Fatalf("replay webhook: %v", err)
	}
	if got := len(h.wallet.Refunds()); got != priorRefunds {
		t.Fatalf("replay should not issue refund: before=%d after=%d", priorRefunds, got)
	}
}

func TestHandleSetuWebhook_Validation(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	if err := h.svc.HandleSetuWebhook(context.Background(), SetuWebhookEvent{
		Status: "succeeded",
	}); err == nil {
		t.Fatalf("expected ref required error")
	}
	if err := h.svc.HandleSetuWebhook(context.Background(), SetuWebhookEvent{
		SetuPaymentRef: "x", Status: "weird",
	}); err == nil {
		t.Fatalf("expected status validation error")
	}
}
