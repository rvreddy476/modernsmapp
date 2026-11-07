//go:build integration

package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// An order the sweep cannot expire is pushed back, and the sweep moves on to
// the next lapsed hold instead of retrying the same one at its head forever
// (migration 053).
func TestExpireStaleOrders_DefersAFailingOrderAndProgresses(t *testing.T) {
	ctx := context.Background()
	unpaid := func() (*fixture, uuid.UUID) {
		f := newFixture(t, 5, 100000, "18")
		f.addToCart(1, 100000)
		res, err := f.store.Checkout(ctx, f.params(f.quote(0), "idem-"+uuid.NewString()))
		if err != nil {
			t.Fatalf("checkout: %v", err)
		}
		return f, res.OrderID
	}
	fa, stuck := unpaid()
	_, next := unpaid()
	// Lapsed long ago, oldest first, so they lead the sweep whatever else
	// the database holds.
	mustExec(t, `UPDATE inventory_reservations SET expires_at = '2000-01-01T00:00:00Z' WHERE order_id = $1`, stuck)
	mustExec(t, `UPDATE inventory_reservations SET expires_at = '2000-01-02T00:00:00Z' WHERE order_id = $1`, next)

	// The stuck order's expiry fails every time: its release event is refused.
	constraint := "test_refuse_expiry_" + strings.ReplaceAll(stuck.String(), "-", "")[:12]
	mustExec(t, fmt.Sprintf(`ALTER TABLE outbox_events ADD CONSTRAINT %s
		CHECK (NOT (event_type = 'commerce.inventory.released' AND payload->'payload'->>'order_id' = '%s')) NOT VALID`,
		constraint, stuck))
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS `+constraint)
	})

	status := func(id uuid.UUID) (s string, attempts int, deferred bool) {
		t.Helper()
		if err := testPool.QueryRow(ctx,
			`SELECT status, expiry_attempts, COALESCE(expiry_next_attempt_at > NOW(), FALSE) FROM orders WHERE id = $1`, id).
			Scan(&s, &attempts, &deferred); err != nil {
			t.Fatalf("read order: %v", err)
		}
		return
	}

	// Tick 1 takes the oldest lapse, fails it and defers it.
	if n, err := fa.store.ExpireStaleOrders(ctx, 1); err != nil || n != 0 {
		t.Fatalf("tick 1: expired=%d err=%v, want 0 (the stuck order fails)", n, err)
	}
	if s, a, d := status(stuck); s != "payment_pending" || a != 1 || !d {
		t.Fatalf("stuck order after tick 1: status=%s attempts=%d deferred=%v, want payment_pending/1/true", s, a, d)
	}
	// Tick 2 is not spent on it again: the next lapsed order expires.
	if n, err := fa.store.ExpireStaleOrders(ctx, 1); err != nil || n != 1 {
		t.Fatalf("tick 2: expired=%d err=%v, want 1", n, err)
	}
	if s, _, _ := status(next); s != "expired" {
		t.Fatalf("next order after tick 2 is %q, want expired", s)
	}
	if s, a, _ := status(stuck); s != "payment_pending" || a != 1 {
		t.Fatalf("stuck order was retried inside its back-off: status=%s attempts=%d", s, a)
	}

	// Once the cause is gone and the back-off has passed, it expires too.
	mustExec(t, `ALTER TABLE outbox_events DROP CONSTRAINT `+constraint)
	mustExec(t, `UPDATE orders SET expiry_next_attempt_at = NOW() - INTERVAL '1 second' WHERE id = $1`, stuck)
	if n, err := fa.store.ExpireStaleOrders(ctx, 1); err != nil || n != 1 {
		t.Fatalf("tick 3: expired=%d err=%v, want 1", n, err)
	}
	if s, _, _ := status(stuck); s != "expired" {
		t.Fatalf("stuck order after its back-off is %q, want expired", s)
	}
}
