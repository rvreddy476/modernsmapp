package postgres

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed proof of the guarded transition. Skips unless
// COMMERCE_TEST_POSTGRES_DSN points at a migrated commerce_db, e.g. the
// dev stack:
//
//	COMMERCE_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/commerce_db?sslmode=disable' \
//	  go test ./services/commerce-service/internal/store/postgres/ -run DB -v
//
// Rows are created under a throwaway customer id and deleted afterwards.
func dbStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("COMMERCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("COMMERCE_TEST_POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

func newTestOrder(t *testing.T, st *Store, status, paymentStatus string) *Order {
	t.Helper()
	pm := "upi"
	key := uuid.NewString()
	o := &Order{
		CustomerUserID: uuid.New(), Subtotal: 900, FinalAmount: 900, CurrencyCode: "INR",
		PaymentMethod: &pm, PaymentStatus: paymentStatus, Status: status, IdempotencyKey: &key,
		DeliveryAddressSnapshot: []byte(`{}`),
	}
	if err := st.CreateOrder(context.Background(), o, nil); err != nil {
		t.Fatalf("create order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.db.Exec(context.Background(), `DELETE FROM orders WHERE id=$1`, o.ID)
	})
	return o
}

// TestDB_MarkOrderPaid_ConcurrentConverges: N goroutines (webhook consumer
// + customer confirm + retries) all call MarkOrderPaid on one
// payment_pending order. Exactly one must observe Applied=true; the row
// must end paid/confirmed with exactly one status-history entry.
func TestDB_MarkOrderPaid_ConcurrentConverges(t *testing.T) {
	st, pool := dbStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	o := newTestOrder(t, st, "payment_pending", "pending")

	const n = 8
	var wg sync.WaitGroup
	results := make([]PaidTransition, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			actor := o.CustomerUserID
			var actorPtr *uuid.UUID
			actorType := "system"
			if i%2 == 0 {
				actorPtr, actorType = &actor, "customer"
			}
			results[i], errs[i] = st.MarkOrderPaid(ctx, o.ID, "pay_"+uuid.NewString()[:8], "razorpay", actorPtr, actorType)
		}(i)
	}
	close(start)
	wg.Wait()

	applied := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i].Applied {
			applied++
			if results[i].FromStatus != "payment_pending" || results[i].Status != "confirmed" || results[i].PaymentStatus != "paid" {
				t.Fatalf("winner saw %+v", results[i])
			}
		} else if results[i].PaymentStatus != "paid" {
			t.Fatalf("loser must observe the converged paid row, saw %+v", results[i])
		}
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want exactly 1", applied)
	}

	var status, paymentStatus string
	var histCount int
	if err := pool.QueryRow(ctx, `SELECT status, payment_status FROM orders WHERE id=$1`, o.ID).Scan(&status, &paymentStatus); err != nil {
		t.Fatal(err)
	}
	if status != "confirmed" || paymentStatus != "paid" {
		t.Fatalf("row = %s/%s, want confirmed/paid", status, paymentStatus)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM order_status_history WHERE order_id=$1 AND notes='payment confirmed'`, o.ID).Scan(&histCount); err != nil {
		t.Fatal(err)
	}
	if histCount != 1 {
		t.Fatalf("status history rows = %d, want 1", histCount)
	}

	// A late payment.failed after capture must be refused.
	applied2, err := st.TransitionPaymentStatus(ctx, o.ID, "failed", "pay_late", "razorpay")
	if err != nil || applied2 {
		t.Fatalf("late failed: applied=%v err=%v, want refused", applied2, err)
	}
}

// TestDB_MarkOrderPaid_Guards covers the non-racing guard outcomes.
func TestDB_MarkOrderPaid_Guards(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()

	t.Run("COD confirmed order: payment applies, status unchanged, no history row", func(t *testing.T) {
		o := newTestOrder(t, st, "confirmed", "pending")
		tr, err := st.MarkOrderPaid(ctx, o.ID, "cod-settled", "cod", nil, "system")
		if err != nil || !tr.Applied || tr.Status != "confirmed" || tr.PaymentStatus != "paid" {
			t.Fatalf("tr=%+v err=%v", tr, err)
		}
		var n int
		// CreateOrder writes its own initial history row; only the paid
		// transition's row is in question here.
		_ = st.db.QueryRow(ctx, `SELECT COUNT(*) FROM order_status_history WHERE order_id=$1 AND notes='payment confirmed'`, o.ID).Scan(&n)
		if n != 0 {
			t.Fatalf("payment-confirmed history rows = %d, want 0 (status did not change)", n)
		}
	})
	t.Run("cancelled order refuses money", func(t *testing.T) {
		o := newTestOrder(t, st, "cancelled", "pending")
		tr, err := st.MarkOrderPaid(ctx, o.ID, "pay_x", "razorpay", nil, "system")
		if err != nil || tr.Applied || tr.Status != "cancelled" || tr.PaymentStatus != "pending" {
			t.Fatalf("tr=%+v err=%v", tr, err)
		}
	})
	t.Run("failed attempt can be retried to paid", func(t *testing.T) {
		o := newTestOrder(t, st, "payment_pending", "failed")
		tr, err := st.MarkOrderPaid(ctx, o.ID, "pay_retry", "razorpay", nil, "system")
		if err != nil || !tr.Applied || tr.Status != "confirmed" {
			t.Fatalf("tr=%+v err=%v", tr, err)
		}
	})
	t.Run("missing order", func(t *testing.T) {
		_, err := st.MarkOrderPaid(ctx, uuid.New(), "pay_x", "razorpay", nil, "system")
		if err != ErrOrderNotFound {
			t.Fatalf("err = %v, want ErrOrderNotFound", err)
		}
	})
	t.Run("failed is idempotent and refused once paid", func(t *testing.T) {
		o := newTestOrder(t, st, "payment_pending", "pending")
		if ok, err := st.TransitionPaymentStatus(ctx, o.ID, "failed", "p1", "razorpay"); err != nil || !ok {
			t.Fatalf("first failed: ok=%v err=%v", ok, err)
		}
		if ok, err := st.TransitionPaymentStatus(ctx, o.ID, "failed", "p1", "razorpay"); err != nil || ok {
			t.Fatalf("second failed should be a no-op: ok=%v err=%v", ok, err)
		}
	})
}
