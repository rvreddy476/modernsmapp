package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestInsertPayment_Validation(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-pmt-1", "mobile_postpaid", "Prov", nil)
	uid := uuid.New()

	// negative amount
	if _, err := s.InsertPayment(ctx, CreatePaymentInput{
		UserID: uid, ProviderID: provID,
		AmountPaise: 0, PaymentMethod: "wallet", IdempotencyKey: "k1",
	}); err == nil {
		t.Fatalf("expected validation error on amount=0")
	}
	// missing key
	if _, err := s.InsertPayment(ctx, CreatePaymentInput{
		UserID: uid, ProviderID: provID,
		AmountPaise: 100, PaymentMethod: "wallet",
	}); err == nil {
		t.Fatalf("expected validation error on missing idempotency key")
	}
}

func TestInsertPayment_RoundTrip(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-pmt-2", "electricity", "Prov", nil)
	uid := uuid.New()

	pmt, err := s.InsertPayment(ctx, CreatePaymentInput{
		UserID: uid, ProviderID: provID, AmountPaise: 50000,
		PaymentMethod: "wallet", IdempotencyKey: "round-trip-1",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if pmt.Status != "initiated" {
		t.Fatalf("expected initiated status; got %q", pmt.Status)
	}
	got, err := s.GetPayment(ctx, uid, pmt.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AmountPaise != 50000 {
		t.Fatalf("amount mismatch: %d", got.AmountPaise)
	}
}

func TestPaymentTransitions(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-pmt-3", "electricity", "Prov", nil)
	uid := uuid.New()

	pmt, _ := s.InsertPayment(ctx, CreatePaymentInput{
		UserID: uid, ProviderID: provID, AmountPaise: 80000,
		PaymentMethod: "wallet", IdempotencyKey: "trans-1",
	})

	// initiated -> submitted
	if err := s.MarkPaymentSubmitted(ctx, pmt.ID, "setu-ref-1"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := s.GetPayment(ctx, uid, pmt.ID)
	if got.Status != "submitted" || got.SetuPaymentRef == nil || *got.SetuPaymentRef != "setu-ref-1" {
		t.Fatalf("submit transition failed: %+v", got)
	}
	// submitted -> succeeded
	if err := s.MarkPaymentSucceeded(ctx, pmt.ID, "RRN-9999"); err != nil {
		t.Fatalf("succeed: %v", err)
	}
	got, _ = s.GetPayment(ctx, uid, pmt.ID)
	if got.Status != "succeeded" || got.ReceiptNumber == nil || *got.ReceiptNumber != "RRN-9999" {
		t.Fatalf("succeed transition failed: %+v", got)
	}
	// succeeded -> refunded
	if err := s.MarkPaymentRefunded(ctx, pmt.ID, "test_refund"); err != nil {
		t.Fatalf("refund: %v", err)
	}
	got, _ = s.GetPayment(ctx, uid, pmt.ID)
	if got.Status != "refunded" {
		t.Fatalf("refund transition failed: %+v", got)
	}
}

func TestGetPayment_NotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	_, err := s.GetPayment(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("expected not-found; got %v", err)
	}
}

func TestAttachWalletTxn(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-pmt-4", "electricity", "Prov", nil)
	uid := uuid.New()
	pmt, _ := s.InsertPayment(ctx, CreatePaymentInput{
		UserID: uid, ProviderID: provID, AmountPaise: 1000,
		PaymentMethod: "wallet", IdempotencyKey: "wall-1",
	})
	walletTxnID := uuid.New()
	if err := s.AttachWalletTxn(ctx, pmt.ID, walletTxnID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	got, _ := s.GetPayment(ctx, uid, pmt.ID)
	if got.WalletTxnID == nil || *got.WalletTxnID != walletTxnID {
		t.Fatalf("attach failed: %+v", got)
	}
}

func TestListPaymentsByUser(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-pmt-5", "electricity", "Prov", nil)
	uid := uuid.New()
	for i := 0; i < 3; i++ {
		key := "list-" + uuid.New().String()
		if _, err := s.InsertPayment(ctx, CreatePaymentInput{
			UserID: uid, ProviderID: provID, AmountPaise: int64(1000 + i*100),
			PaymentMethod: "wallet", IdempotencyKey: key,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	out, err := s.ListPaymentsByUser(ctx, uid, "", "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 rows; got %d", len(out))
	}
}
