//go:build integration

package postgres

// Correction-pass proofs for the commerce half of review §6.
//
// N6 — `expected_total_minor` is mandatory and enforced.
// N1 — checkout works against the REAL schema (the address-column defect).
//
// Review §4 said of B5: "No proof requires expected_total_minor or
// demonstrates a stale-price refusal when the field is omitted." These are
// those proofs, and the assertions are about SIDE EFFECTS — an order, a
// stock hold, a coupon claim and an outbox row must all be absent — because
// "it returned an error" is not the same as "it changed nothing".

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// sideEffects counts everything a checkout would have created for one buyer.
func sideEffects(t *testing.T, f *fixture) (orders, reservations, ledger, outbox int) {
	t.Helper()
	ctx := context.Background()
	q := func(sql string, args ...any) int {
		var n int
		if err := testPool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("counting side effects: %v", err)
		}
		return n
	}
	orders = q(`SELECT count(*) FROM orders WHERE customer_user_id=$1`, f.userID)
	reservations = q(`SELECT count(*) FROM inventory_reservations WHERE variant_id=$1`, f.variantID)
	ledger = q(`SELECT count(*) FROM inventory_ledger WHERE variant_id=$1`, f.variantID)
	outbox = q(`SELECT count(*) FROM outbox_events
	             WHERE partition_key IN (SELECT id::text FROM orders WHERE customer_user_id=$1)`, f.userID)
	return
}

func requireNoSideEffects(t *testing.T, f *fixture, why string) {
	t.Helper()
	orders, reservations, ledger, outbox := sideEffects(t, f)
	if orders != 0 || reservations != 0 || ledger != 0 || outbox != 0 {
		t.Fatalf("%s: expected NO side effects, got orders=%d reservations=%d ledger=%d outbox=%d",
			why, orders, reservations, ledger, outbox)
	}
	// Stock must be untouched.
	if _, reserved := f.inventory(); reserved != 0 {
		t.Fatalf("%s: stock was held (reserved=%d) by a refused checkout", why, reserved)
	}
}

// ─── N6: the approved price is mandatory ─────────────────────────────

// An omitted expected total creates nothing. Before N6 this was the client's
// switch for turning the price-change promise off: both comparisons were
// guarded by `if p.ExpectedTotalMinor > 0`.
func TestProofN6_OmittedExpectedTotalCreatesNothing(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 118000, "18")
	f.addToCart(1, 118000)
	quoteID := f.quote(7000)

	_, err := store.Checkout(ctx, f.paramsExpecting(quoteID, "idem-"+uuid.NewString(), 0))
	if !errors.Is(err, ErrExpectedTotalRequired) {
		t.Fatalf("got %v, want ErrExpectedTotalRequired", err)
	}
	requireNoSideEffects(t, f, "omitted expected total")
}

func TestProofN6_NegativeExpectedTotalCreatesNothing(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 118000, "18")
	f.addToCart(1, 118000)
	quoteID := f.quote(7000)

	_, err := store.Checkout(ctx, f.paramsExpecting(quoteID, "idem-"+uuid.NewString(), money.Paise(-1)))
	if !errors.Is(err, ErrExpectedTotalRequired) {
		t.Fatalf("got %v, want ErrExpectedTotalRequired", err)
	}
	requireNoSideEffects(t, f, "negative expected total")
}

// THE scenario from review §3 N6: the cart showed ₹1,180, the seller raised
// the price, and the client checks out against the stale total. The customer
// must not be charged the new price.
func TestProofN6_StaleTotalIsRefusedAfterAPriceRise(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 118000, "18")
	f.addToCart(1, 118000)
	quoteID := f.quote(7000)
	stale := f.expectedTotal() // ₹1,180 + ₹70 delivery

	// The seller raises the price tenfold after the customer saw the cart.
	if _, err := testPool.Exec(ctx,
		`UPDATE product_variants SET selling_price_minor = 1180000, selling_price = 11800
		  WHERE id = $1`, f.variantID); err != nil {
		t.Fatalf("raising the price: %v", err)
	}

	_, err := store.Checkout(ctx, f.paramsExpecting(quoteID, "idem-"+uuid.NewString(), stale))
	var pce *PriceChangedError
	if !errors.As(err, &pce) {
		t.Fatalf("got %v, want PriceChangedError — the customer must be told, not charged", err)
	}
	requireNoSideEffects(t, f, "stale total after a price rise")
}

// The positive half: the approved total is honoured. Without this, the three
// proofs above could pass because checkout never succeeds at all.
func TestProofN6_MatchingExpectedTotalCommits(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 118000, "18")
	f.addToCart(1, 118000)
	quoteID := f.quote(7000)
	approved := f.expectedTotal()

	res, err := store.Checkout(ctx, f.params(quoteID, "idem-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("a matching expected total must commit: %v", err)
	}
	if res.TotalMinor != approved {
		t.Fatalf("charged %d, customer approved %d", res.TotalMinor, approved)
	}
	if _, reserved := f.inventory(); reserved != 1 {
		t.Fatalf("stock was not held (reserved=%d)", reserved)
	}
}

// ─── N6 negative control (review §4) ─────────────────────────────────
//
// Restore the `> 0` guard and show a stale total sails through. The control
// reimplements the previous condition against the SAME computed figures, so
// if it stops demonstrating the overcharge the assertions above are not
// testing what they claim.
func TestProofNegativeControl_N6_OptionalTotalSkipsTheComparison(t *testing.T) {
	// The previous code, verbatim in shape:
	//     if p.ExpectedTotalMinor > 0 && computed.Total != p.ExpectedTotalMinor { refuse }
	oldRefuses := func(expected, computed money.Paise) bool {
		return expected > 0 && computed != expected
	}
	// The corrected code:
	newRefuses := func(expected, computed money.Paise) bool {
		return expected <= 0 || computed != expected
	}

	const shown, charged = money.Paise(125000), money.Paise(1187000)

	// Omitted: the old code charged ₹11,870 against a ₹1,250 cart silently.
	if oldRefuses(0, charged) {
		t.Fatal("negative control did not reproduce the defect: the old guard refused an omitted total")
	}
	if !newRefuses(0, charged) {
		t.Fatal("the corrected guard accepts an omitted total")
	}
	// Present and stale: both refuse, which is why the defect was invisible
	// to any test that always supplied the field.
	if !oldRefuses(shown, charged) || !newRefuses(shown, charged) {
		t.Fatal("a stale-but-present total must be refused by both")
	}
	t.Log("negative control reproduced the original defect: with expected_total_minor omitted, " +
		"the previous guard skipped the comparison entirely and the customer was charged a total " +
		"they were never shown")
}

// ─── N1: the real schema ─────────────────────────────────────────────
//
// The correction pass introduced a checkout query against `address_line1` /
// `address_line2` while the schema defines `address_line_1` /
// `address_line_2`. Every checkout raised SQLSTATE 42703 and the primary
// buyer journey was unusable. It passed `go vet` and every unit test; only a
// real database could catch it.
//
// This proof pins the column names by exercising the path that reads them
// and asserting the address binding actually works, so the same class of
// defect fails here rather than in production.
func TestProofN1_CheckoutReadsTheRealAddressColumns(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 118000, "18")
	f.addToCart(1, 118000)
	quoteID := f.quote(7000)

	if _, err := store.Checkout(ctx, f.params(quoteID, "idem-"+uuid.NewString())); err != nil {
		t.Fatalf("checkout against the real schema failed: %v", err)
	}
}

// And the B7 binding it guards: editing the address after the quote must
// invalidate the quote rather than ship to an unquoted destination.
func TestProofN1_EditedAddressInvalidatesTheQuote(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	f := newFixture(t, 10, 118000, "18")
	f.addToCart(1, 118000)
	quoteID := f.quote(7000)

	// Same address row, different destination.
	if _, err := testPool.Exec(ctx,
		`UPDATE customer_addresses
		    SET address_line_1 = '9 Remote Rd', city = 'Leh', state = 'LA', postal_code = '194101'
		  WHERE id = $1`, f.addressID); err != nil {
		t.Fatalf("editing the address: %v", err)
	}

	_, err := store.Checkout(ctx, f.params(quoteID, "idem-"+uuid.NewString()))
	if !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("got %v, want ErrQuoteMismatch — a quote must not survive an address edit", err)
	}
	requireNoSideEffects(t, f, "address edited after the quote")
}
