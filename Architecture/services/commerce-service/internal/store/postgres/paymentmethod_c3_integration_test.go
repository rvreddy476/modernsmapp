//go:build integration

package postgres

// C3-LB-3 — one payment-method vocabulary, proven at the store boundary
// against a live PostgreSQL.
//
// The defect review 3 found: Android offered upi/card, the gated CHECK
// permitted net_banking, and the commerce store refused only "" and "cod". So
// a direct client could send net_banking, and the order COMMITTED and
// RESERVED STOCK before payments-service refused to open an intent for it.
// The buyer was left with a payment_pending order they could not pay, and the
// inventory stayed locked behind it until the expiry sweeper ran.
//
// Hiding the option in one Android binary is not an authority boundary. These
// tests assert what did NOT happen at the layer a direct caller reaches:
// no order, no reservation, no coupon claim, no outbox row.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
)

// sideEffects captures everything a checkout would durably create.
type c3SideEffects struct {
	orders      int
	orderItems  int
	reserved    int
	couponUsage int
	outbox      int
}

func (f *fixture) c3SideEffects() c3SideEffects {
	f.t.Helper()
	ctx := context.Background()
	var s c3SideEffects
	q := func(dst *int, sql string, args ...any) {
		if err := testPool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			f.t.Fatalf("counting side effects: %v", err)
		}
	}
	q(&s.orders, `SELECT count(*) FROM orders WHERE customer_user_id=$1`, f.userID)
	q(&s.orderItems, `SELECT count(*) FROM order_items oi
	                    JOIN orders o ON o.id = oi.order_id
	                   WHERE o.customer_user_id=$1`, f.userID)
	q(&s.reserved, `SELECT COALESCE(reserved_qty,0) FROM inventory_items WHERE variant_id=$1`, f.variantID)
	q(&s.couponUsage, `SELECT count(*) FROM coupon_usages WHERE user_id=$1`, f.userID)
	q(&s.outbox, `SELECT count(*) FROM outbox_events WHERE partition_key=$1`, f.userID.String())
	return s
}

// everyRefusedMethod is the exhaustive list the launch must refuse. It is
// derived from nothing — it is written out on purpose, so that widening
// paymentmethod.Allowed() without revisiting this list turns the suite red
// rather than silently shrinking what is tested.
var everyRefusedMethod = []string{
	"net_banking", // the live defect: commerce accepted it, payments did not
	"wallet",      // skipped provider-order creation, leaving a blank reference
	"cod",         // no PSP leg at all
	"emi",
	"bnpl",
	"escrow",
	"",       // blank
	"   ",    // whitespace
	"UPI",    // non-canonical case — must NOT be silently folded
	"Card",   //
	"upi ",   // trailing space
	"credit", // simply unknown
}

// THE C3-LB-3 proof. Every refused method must leave the database exactly as
// it found it.
func TestC3NoUnsupportedMethodCreatesAnyDurableEffect(t *testing.T) {
	for _, method := range everyRefusedMethod {
		t.Run("method="+method, func(t *testing.T) {
			f := newFixture(t, 10, 100000, "18")
			f.addToCart(1, 100000)
			quoteID := f.quote(4000)

			before := f.c3SideEffects()

			p := f.params(quoteID, "c3lb3-"+uuid.NewString())
			p.PaymentMethod = method
			_, err := f.store.Checkout(context.Background(), p)
			if err == nil {
				t.Fatalf("checkout ACCEPTED payment_method %q — an order was committed that "+
					"payments-service cannot open an intent for", method)
			}

			after := f.c3SideEffects()
			if after != before {
				t.Fatalf("payment_method %q was refused but left side effects: before=%+v after=%+v",
					method, before, after)
			}
			// And through the suite's own shared assertion, so the two
			// accountings of "nothing happened" have to agree.
			requireNoSideEffects(t, f, "refused payment_method "+method)
			// Specifically: no stock is held behind an unpayable order.
			if _, reserved := f.inventory(); reserved != 0 {
				t.Fatalf("payment_method %q reserved %d unit(s) behind an order that can "+
					"never be paid for", method, reserved)
			}
		})
	}
}

// COD keeps its own typed error, because the client renders dedicated copy
// for it and A5 is a named launch decision rather than a typo.
func TestC3CODStillReportsItsOwnError(t *testing.T) {
	f := newFixture(t, 5, 100000, "18")
	f.addToCart(1, 100000)
	quoteID := f.quote(4000)

	p := f.params(quoteID, "c3cod-"+uuid.NewString())
	p.PaymentMethod = "cod"
	if _, err := f.store.Checkout(context.Background(), p); !errors.Is(err, ErrCODNotSupported) {
		t.Fatalf("got %v, want ErrCODNotSupported", err)
	}
}

// Everything else reports the shared vocabulary error, so a client can tell
// "you sent something we do not support" from "COD is a product decision".
func TestC3UnsupportedMethodReportsTheVocabularyError(t *testing.T) {
	f := newFixture(t, 5, 100000, "18")
	f.addToCart(1, 100000)
	quoteID := f.quote(4000)

	p := f.params(quoteID, "c3nb-"+uuid.NewString())
	p.PaymentMethod = "net_banking"
	_, err := f.store.Checkout(context.Background(), p)
	if !errors.Is(err, paymentmethod.ErrUnsupported) {
		t.Fatalf("got %v, want paymentmethod.ErrUnsupported", err)
	}
}

// The positive half. Both launch methods must actually work — otherwise the
// refusals above would be passing against a checkout that refuses everything.
func TestC3BothLaunchMethodsCompleteCheckout(t *testing.T) {
	for _, method := range paymentmethod.Allowed() {
		t.Run("method="+method, func(t *testing.T) {
			f := newFixture(t, 10, 100000, "18")
			f.addToCart(1, 100000)
			quoteID := f.quote(4000)

			p := f.params(quoteID, "c3ok-"+uuid.NewString())
			p.PaymentMethod = method
			res, err := f.store.Checkout(context.Background(), p)
			if err != nil {
				t.Fatalf("checkout with the launch method %q failed: %v", method, err)
			}
			if res.OrderID == uuid.Nil {
				t.Fatal("checkout returned no order id")
			}
			if _, reserved := f.inventory(); reserved != 1 {
				t.Fatalf("reserved = %d, want 1", reserved)
			}

			// And the value that was stored is exactly what was sent — no
			// silent coercion.
			var stored string
			if err := testPool.QueryRow(context.Background(),
				`SELECT payment_method FROM orders WHERE id=$1`, res.OrderID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != method {
				t.Fatalf("stored payment_method = %q, want %q — the value was coerced", stored, method)
			}
		})
	}
}

// The launch vocabulary is exactly two methods. If this changes, every layer
// named in the C3-LB-3 comment above has to change with it, and this test is
// the tripwire that says so.
func TestC3LaunchVocabularyIsExactlyUPIAndCard(t *testing.T) {
	got := paymentmethod.Allowed()
	want := []string{"card", "upi"}
	if len(got) != len(want) {
		t.Fatalf("allowed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allowed = %v, want %v", got, want)
		}
	}
}

// The database is the last authority, and it must refuse independently of
// every Go check above it. This bypasses the store entirely.
func TestC3DatabaseRefusesAnUnsupportedMethodDirectly(t *testing.T) {
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO orders (id, customer_user_id, order_number, payment_method,
		                    subtotal, final_amount, final_amount_minor)
		VALUES (gen_random_uuid(), gen_random_uuid(), $1, 'net_banking', 100.00, 100.00, 10000)`,
		"ORD-C3-"+uuid.NewString()[:8])
	if err == nil {
		t.Fatal("the database accepted an order with payment_method='net_banking'; the gated 998 " +
			"CHECK is not enforcing the launch vocabulary, so the Go checks are the only " +
			"thing standing between a direct writer and an unpayable order")
	}
}
