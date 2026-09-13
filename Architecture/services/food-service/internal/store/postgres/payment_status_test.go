package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func customerStatusOf(t *testing.T, s *Store, userID, orderID uuid.UUID) (*CustomerPaymentState, string) {
	t.Helper()
	st, err := s.CustomerPaymentStatus(context.Background(), userID, orderID)
	if err != nil {
		t.Fatalf("CustomerPaymentStatus: %v", err)
	}
	status, _, err := payments.CustomerStatus(payments.CustomerPaymentSnapshot{
		OrderStatus: st.OrderStatus, PaymentStatus: st.PaymentStatus, PaymentMethod: st.PaymentMethod, CaptureApplied: st.CaptureApplied,
	})
	if err != nil {
		t.Fatalf("CustomerStatus: %v", err)
	}
	return st, status
}

func TestCustomerPaymentStatus_OnlyTheOrdersCustomer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, _ := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")

	_, foreignErr := s.CustomerPaymentStatus(ctx, uuid.New(), orderID)
	_, missingErr := s.CustomerPaymentStatus(ctx, customerID, uuid.New())
	if !errors.Is(foreignErr, pgx.ErrNoRows) || !errors.Is(missingErr, pgx.ErrNoRows) {
		t.Fatalf("foreign err = %v, missing err = %v; both must be pgx.ErrNoRows", foreignErr, missingErr)
	}
	if foreignErr.Error() != missingErr.Error() {
		t.Fatalf("foreign and missing differ: %q vs %q", foreignErr, missingErr)
	}

	st, status := customerStatusOf(t, s, customerID, orderID)
	if st.OrderID != orderID || st.AmountMinor != 25000 || st.Currency != "INR" || st.PaymentMethod != "ONLINE" ||
		st.CaptureApplied || status != payments.CustomerStatusConfirming || st.UpdatedAt.IsZero() {
		t.Fatalf("state = %+v status %s", st, status)
	}
}

func TestCustomerPaymentStatus_PaidOnlyAfterTheEventIsApplied(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// A payment_status written by anything other than the consumer is not paid.
	orderID, customerID, _ := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET payment_status = 'CAPTURED' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	if st, status := customerStatusOf(t, s, customerID, orderID); st.CaptureApplied || status != payments.CustomerStatusConfirming {
		t.Fatalf("captured without an event: %+v status %s", st, status)
	}

	// A recorded but NOT applied capture (amount mismatch) is not paid.
	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	bad := succeeded(orderID, customerID, intentID)
	bad.AmountMinor = 1
	if applied, err := s.ApplyPaymentEvent(ctx, bad); err != nil || applied.Decision.Outcome != payments.OutcomeAmountMismatch {
		t.Fatalf("mismatch apply: %+v %v", applied, err)
	}
	if st, status := customerStatusOf(t, s, customerID, orderID); st.CaptureApplied || status != payments.CustomerStatusConfirming {
		t.Fatalf("after a mismatched capture: %+v status %s", st, status)
	}

	// The signed capture, applied by the consumer path: paid.
	ev := succeeded(orderID, customerID, intentID)
	if applied, err := s.ApplyPaymentEvent(ctx, ev); err != nil || applied.Decision.Outcome != payments.OutcomeConfirmed {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	st, status := customerStatusOf(t, s, customerID, orderID)
	if !st.CaptureApplied || status != payments.CustomerStatusPaid || st.OrderStatus != "CONFIRMED" {
		t.Fatalf("after the capture: %+v status %s", st, status)
	}
	// Still not readable by anyone else.
	if _, err := s.CustomerPaymentStatus(ctx, uuid.New(), orderID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign read of a paid order: %v", err)
	}
}

func TestCustomerPaymentStatus_FailedEvent(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	ev := succeeded(orderID, customerID, intentID)
	ev.EventType, ev.Status = events.EventPaymentFailed, "failed"
	if applied, err := s.ApplyPaymentEvent(ctx, ev); err != nil || applied.Decision.Outcome != payments.OutcomeMarkedFailed {
		t.Fatalf("apply failed: %+v %v", applied, err)
	}
	if st, status := customerStatusOf(t, s, customerID, orderID); status != payments.CustomerStatusFailed {
		t.Fatalf("after payment.failed: %+v status %s", st, status)
	}
}
