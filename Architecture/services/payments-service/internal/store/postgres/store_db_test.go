package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed regression for the webhook publish path. Skips unless
// PAYMENTS_TEST_POSTGRES_DSN points at a migrated commerce_db (payments
// schema), e.g. the dev stack:
//
//	PAYMENTS_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/commerce_db?sslmode=disable' \
//	  go test ./services/payments-service/internal/store/postgres/ -run DB -v
func dbStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("PAYMENTS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PAYMENTS_TEST_POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

// TestDB_UpdateStatusByProviderRef_ReturnsFullIntent: the capture webhook
// replaces provider_ref (order id → payment id). The store must hand back
// the updated row with its reference_type/reference_id intact so the
// published payment.succeeded still names the order — re-reading by the
// old provider_ref (the previous implementation) finds nothing.
func TestDB_UpdateStatusByProviderRef_ReturnsFullIntent(t *testing.T) {
	st := dbStore(t)
	ctx := context.Background()
	orderID := uuid.New()
	rzpOrder := "order_test_" + uuid.NewString()[:8]
	res, err := st.CreateIntent(ctx, PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(), ReferenceType: "order", ReferenceID: orderID,
		Amount: 900, AmountMinorRaw: 90000, Currency: "INR", Method: "upi", ProviderRef: rzpOrder, IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create intent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.db.Exec(ctx, `DELETE FROM payments.payment_audit_log WHERE intent_id=$1`, res.Intent.ID)
		_, _ = st.db.Exec(ctx, `DELETE FROM payments.payment_intents WHERE id=$1`, res.Intent.ID)
	})

	got, err := st.UpdateStatusByProviderRef(ctx, rzpOrder, "succeeded", "pay_test_1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.ID != res.Intent.ID || got.Status != "succeeded" || got.ProviderRef != "pay_test_1" {
		t.Fatalf("returned row = %+v", got)
	}
	if got.ReferenceType != "order" || got.ReferenceID != orderID {
		t.Fatalf("returned row lost its reference: %s/%s", got.ReferenceType, got.ReferenceID)
	}
	// The old provider_ref no longer resolves; the payment id does.
	if _, err := st.GetIntentByProviderRef(ctx, rzpOrder); !errors.Is(err, ErrPaymentNotFound) && err == nil {
		t.Fatalf("old provider_ref should not resolve after capture")
	}
	if p, err := st.GetIntentByProviderRef(ctx, "pay_test_1"); err != nil || p.ID != res.Intent.ID {
		t.Fatalf("lookup by payment id: %v", err)
	}
	// Replay: succeeded → succeeded is refused by the state machine.
	if _, err := st.UpdateStatusByProviderRef(ctx, "pay_test_1", "succeeded", "pay_test_1"); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("replay err = %v, want ErrInvalidStatusTransition", err)
	}
	// Unknown ref.
	if _, err := st.UpdateStatusByProviderRef(ctx, "order_nope", "succeeded", "p"); !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("unknown ref err = %v", err)
	}
}
