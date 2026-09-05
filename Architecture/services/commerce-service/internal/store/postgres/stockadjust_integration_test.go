//go:build integration

package postgres

// Stock after creation.
//
// Before AdjustStock, `total_qty` was written exactly once — by CreateProduct,
// from the number typed into the create form. The only other writer, bulk
// import, is behind the launch fence. So a sold-out seller stayed sold out.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// stockFixture is a seller + product + variant + inventory row, all fresh.
type stockFixture struct {
	sellerID  uuid.UUID
	variantID uuid.UUID
	actorID   uuid.UUID
}

func newStockFixture(t *testing.T, total, reserved int) stockFixture {
	t.Helper()
	f := stockFixture{sellerID: uuid.New(), variantID: uuid.New(), actorID: uuid.New()}
	productID := uuid.New()
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	             VALUES ($1,$2,'Stock Store',$3,'stock@example.test','KA')`,
		f.sellerID, f.actorID, "stock-"+f.sellerID.String()[:8])
	mustExec(t, `INSERT INTO products
	               (id,seller_id,title,slug,status,approval_status,return_policy_type)
	             VALUES ($1,$2,'Stocked Thing',$3,'active','approved','7_days')`,
		productID, f.sellerID, "stocked-"+productID.String()[:8])
	mustExec(t, `INSERT INTO product_variants
	               (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor)
	             VALUES ($1,$2,$3,100,100,10000,10000)`,
		f.variantID, productID, "SKU-"+f.variantID.String()[:8])
	mustExec(t, `INSERT INTO inventory_items (id,variant_id,seller_id,total_qty,reserved_qty)
	             VALUES (gen_random_uuid(),$1,$2,$3,$4)`,
		f.variantID, f.sellerID, total, reserved)
	seedOfferFor(t, productID)
	return f
}

func (f stockFixture) adjust(delta int, reason string) StockAdjustment {
	return StockAdjustment{
		VariantID: f.variantID, SellerID: f.sellerID, ActorID: f.actorID,
		Delta: delta, Reason: reason,
	}
}

// The capability that did not exist: a sold-out seller restocks.
func TestASoldOutSellerCanRestock(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 0, 0)

	level, err := store.AdjustStock(ctx, f.adjust(+25, "purchase"))
	if err != nil {
		t.Fatalf("AdjustStock: %v — a sold-out seller has no way back", err)
	}
	if level.TotalQty != 25 || level.Available != 25 {
		t.Fatalf("total=%d available=%d, want 25/25", level.TotalQty, level.Available)
	}
}

// A delta, not a new total. Two units sell while the seller is typing; the
// seller's "+10" must add ten to what is actually there, not overwrite it.
func TestARestockAddsToWhatIsActuallyThere(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 42, 0)

	// Two units sell after the seller's screen rendered 42.
	mustExec(t, `UPDATE inventory_items SET total_qty=40 WHERE variant_id=$1`, f.variantID)

	level, err := store.AdjustStock(ctx, f.adjust(+10, "purchase"))
	if err != nil {
		t.Fatal(err)
	}
	if level.TotalQty != 50 {
		t.Fatalf("total=%d, want 50; a 'set total to 52' API would have resurrected "+
			"the two units that sold while the seller was typing", level.TotalQty)
	}
}

// Reserved units are promised to orders mid-checkout.
func TestStockCannotBeWrittenBelowWhatIsReserved(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 6)

	_, err := store.AdjustStock(ctx, f.adjust(-7, "damage"))
	if !errors.Is(err, ErrStockBelowReserved) {
		t.Fatalf("err = %v, want ErrStockBelowReserved; 6 units are promised to live orders "+
			"and leaving 3 makes them unfulfillable", err)
	}

	// And nothing moved.
	var total int
	if err := testPool.QueryRow(ctx,
		`SELECT total_qty FROM inventory_items WHERE variant_id=$1`, f.variantID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Fatalf("total=%d after a refused adjustment, want 10", total)
	}
}

// Writing down exactly to the reserved line is legal — those units exist.
func TestStockMayBeWrittenDownToTheReservedLine(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 6)

	level, err := store.AdjustStock(ctx, f.adjust(-4, "damage"))
	if err != nil {
		t.Fatalf("AdjustStock: %v", err)
	}
	if level.TotalQty != 6 || level.Available != 0 {
		t.Fatalf("total=%d available=%d, want 6/0", level.TotalQty, level.Available)
	}
}

// Ownership comes from the variant's product, not from the caller's claim.
func TestASellerCannotAdjustAnotherSellersStock(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	victim := newStockFixture(t, 100, 0)
	attacker := newStockFixture(t, 5, 0)

	adj := victim.adjust(-100, "correction")
	adj.SellerID = attacker.sellerID // the attacker's own, honestly resolved seller id

	if _, err := store.AdjustStock(ctx, adj); !errors.Is(err, ErrNotYourVariant) {
		t.Fatalf("err = %v, want ErrNotYourVariant; one seller just zeroed another's stock", err)
	}

	var total int
	if err := testPool.QueryRow(ctx,
		`SELECT total_qty FROM inventory_items WHERE variant_id=$1`, victim.variantID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 100 {
		t.Fatalf("victim total=%d, want 100", total)
	}
}

// Every movement lands in the ledger, the same append-only account checkout,
// payment commit and expiry write to.
func TestAnAdjustmentIsRecordedInBothTheLedgerAndTheAuditTable(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 0, 0)

	adj := f.adjust(+12, "recount")
	adj.Notes = "quarterly count"
	if _, err := store.AdjustStock(ctx, adj); err != nil {
		t.Fatal(err)
	}

	var delta int
	var reason, actorType string
	if err := testPool.QueryRow(ctx, `
		SELECT delta_total, reason, actor_type FROM inventory_ledger
		 WHERE variant_id=$1 ORDER BY id DESC LIMIT 1`, f.variantID).
		Scan(&delta, &reason, &actorType); err != nil {
		t.Fatalf("no ledger row: %v — total_qty would no longer reconcile against the ledger", err)
	}
	if delta != 12 || reason != "seller_adjust" || actorType != "seller" {
		t.Fatalf("ledger: delta=%d reason=%q actor=%q", delta, reason, actorType)
	}

	var auditDelta int
	var code, notes string
	if err := testPool.QueryRow(ctx, `
		SELECT delta, reason_code, COALESCE(notes,'') FROM inventory_adjustments
		 WHERE variant_id=$1`, f.variantID).Scan(&auditDelta, &code, &notes); err != nil {
		t.Fatalf("no audit row: %v", err)
	}
	if auditDelta != 12 || code != "recount" || notes != "quarterly count" {
		t.Fatalf("audit: delta=%d code=%q notes=%q", auditDelta, code, notes)
	}
}

// Two adjustments at once must both land. Without the row lock, one is lost.
func TestConcurrentAdjustmentsBothLand(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 0, 0)

	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = store.AdjustStock(ctx, f.adjust(+3, "purchase"))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	var total int
	if err := testPool.QueryRow(ctx,
		`SELECT total_qty FROM inventory_items WHERE variant_id=$1`, f.variantID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != writers*3 {
		t.Fatalf("total=%d after %d concurrent +3 adjustments, want %d — a lost update",
			total, writers, writers*3)
	}
}

// Stock cannot go negative even with no reservations in play.
func TestStockCannotGoNegative(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 3, 0)

	if _, err := store.AdjustStock(ctx, f.adjust(-4, "theft")); err == nil {
		t.Fatal("removed 4 units from a stock of 3")
	}
}

func TestAZeroDeltaIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 3, 0)
	if _, err := store.AdjustStock(ctx, f.adjust(0, "correction")); !errors.Is(err, ErrZeroAdjustment) {
		t.Fatalf("err = %v, want ErrZeroAdjustment", err)
	}
}

// StockFor is ownership-checked too — it is the read the seller's stock screen
// makes, and it must not become a way to enumerate another seller's levels.
func TestStockForRefusesAnotherSellersVariant(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	victim := newStockFixture(t, 7, 2)
	attacker := newStockFixture(t, 1, 0)

	if _, err := store.StockFor(ctx, victim.variantID, attacker.sellerID); !errors.Is(err, ErrNotYourVariant) {
		t.Fatalf("err = %v, want ErrNotYourVariant", err)
	}
	level, err := store.StockFor(ctx, victim.variantID, victim.sellerID)
	if err != nil {
		t.Fatal(err)
	}
	if level.TotalQty != 7 || level.ReservedQty != 2 || level.Available != 5 {
		t.Fatalf("level = %+v, want 7/2/5", level)
	}
}

func TestAdjustingAnUnknownVariantIsNotFound(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	_, err := store.AdjustStock(ctx, StockAdjustment{
		VariantID: uuid.New(), SellerID: uuid.New(), Delta: 1, Reason: "purchase"})
	if !errors.Is(err, ErrVariantNotFound) {
		t.Fatalf("err = %v, want ErrVariantNotFound", err)
	}
}

// ─── The cart-hold release ─────────────────────────────────────────────

// ReleaseReservation was broken twice over and compiled clean: it ran
// `DELETE ... LIMIT 1`, which PostgreSQL rejects outright, and it targeted
// `order_id IS NULL` while the caller passed an order's quantity.
//
// It is unreachable today (cmd/server wires the P0 consumer, which uses
// ApplyPaymentFailed). These prove the repair, so the next person to wire a
// cancel-cart-hold path gets a function that works.

func seedCartHold(t *testing.T, f stockFixture, userID uuid.UUID, qty int) {
	t.Helper()
	mustExec(t, `INSERT INTO inventory_reservations
	               (variant_id, order_id, user_id, quantity, type, expires_at)
	             VALUES ($1, NULL, $2, $3, 'cart', NOW() + INTERVAL '30 minutes')`,
		f.variantID, userID, qty)
	mustExec(t, `UPDATE inventory_items SET reserved_qty = reserved_qty + $2 WHERE variant_id = $1`,
		f.variantID, qty)
}

func reservedFor(t *testing.T, variantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT reserved_qty FROM inventory_items WHERE variant_id=$1`, variantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReleasingACartHoldActuallyReleasesIt(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 0)
	buyer := uuid.New()
	seedCartHold(t, f, buyer, 3)

	if err := store.ReleaseReservation(ctx, f.variantID, buyer, 3); err != nil {
		t.Fatalf("ReleaseReservation: %v — the previous version raised a SQL syntax error "+
			"on every call and its caller logged it at Warn", err)
	}
	if got := reservedFor(t, f.variantID); got != 0 {
		t.Fatalf("reserved=%d after release, want 0; the units stay held until the TTL sweep", got)
	}
	var rows int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE variant_id=$1`, f.variantID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d reservation rows survive the release", rows)
	}
}

// The reservation row decides how much comes back, not the caller. A caller
// passing a stale or wrong quantity must not be able to corrupt the count.
func TestTheReservationRowDecidesHowMuchIsReleased(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 0)
	buyer := uuid.New()
	seedCartHold(t, f, buyer, 2)

	// The caller claims 9. Only the 2 actually held may come back.
	if err := store.ReleaseReservation(ctx, f.variantID, buyer, 9); err != nil {
		t.Fatal(err)
	}
	if got := reservedFor(t, f.variantID); got != 0 {
		t.Fatalf("reserved=%d, want 0", got)
	}
}

// An order-attached hold is not a cart hold, and must survive. This is the
// second half of the original defect: the old DELETE targeted cart rows while
// the caller meant an order's.
func TestReleasingACartHoldLeavesAnOrdersHoldAlone(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 20, 0)
	buyer := uuid.New()

	orderID := uuid.New()
	mustExec(t, `INSERT INTO orders
	               (id, order_number, customer_user_id, subtotal, final_amount, status, payment_status)
	             VALUES ($1, $2, $3, 100, 100, 'payment_pending', 'pending')`,
		orderID, "ORD-"+orderID.String()[:8], buyer)
	mustExec(t, `INSERT INTO inventory_reservations
	               (variant_id, order_id, user_id, quantity, type, expires_at)
	             VALUES ($1, $2, $3, 5, 'order', NOW() + INTERVAL '30 minutes')`,
		f.variantID, orderID, buyer)
	mustExec(t, `UPDATE inventory_items SET reserved_qty = 5 WHERE variant_id = $1`, f.variantID)

	// No cart hold exists — only the order's. The release must be a no-op.
	if err := store.ReleaseReservation(ctx, f.variantID, buyer, 5); err != nil {
		t.Fatal(err)
	}
	if got := reservedFor(t, f.variantID); got != 5 {
		t.Fatalf("reserved=%d, want 5 — releasing a cart hold just cancelled an order's "+
			"hold on stock the buyer is mid-checkout for", got)
	}
	var orderRows int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE order_id=$1`, orderID).Scan(&orderRows); err != nil {
		t.Fatal(err)
	}
	if orderRows != 1 {
		t.Fatalf("the order's reservation row is gone")
	}
}

// Idempotent: a second release finds nothing and subtracts nothing.
func TestReleasingTwiceDoesNotDoubleSubtract(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 0)
	buyer := uuid.New()
	seedCartHold(t, f, buyer, 4)
	seedCartHold(t, f, buyer, 4) // two separate holds, 8 reserved

	if err := store.ReleaseReservation(ctx, f.variantID, buyer, 4); err != nil {
		t.Fatal(err)
	}
	if got := reservedFor(t, f.variantID); got != 4 {
		t.Fatalf("reserved=%d after releasing one of two holds, want 4", got)
	}
	if err := store.ReleaseReservation(ctx, f.variantID, buyer, 4); err != nil {
		t.Fatal(err)
	}
	if got := reservedFor(t, f.variantID); got != 0 {
		t.Fatalf("reserved=%d, want 0", got)
	}
	// A third release has nothing left and must change nothing.
	if err := store.ReleaseReservation(ctx, f.variantID, buyer, 4); err != nil {
		t.Fatal(err)
	}
	if got := reservedFor(t, f.variantID); got != 0 {
		t.Fatalf("reserved=%d after a release with nothing to release", got)
	}
}
