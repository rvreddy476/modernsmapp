package postgres

// DB-backed proofs of the seller transitions, against the real
// order_status_transitions table. Skips unless COMMERCE_TEST_POSTGRES_DSN is
// set (see order_transitions_db_test.go); point it at commerce_it_test.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type historyRow struct {
	from, to, actor string
	notes           *string
}

func orderHistory(t *testing.T, st *Store, orderID uuid.UUID) []historyRow {
	t.Helper()
	rows, err := st.ListOrderStatusHistory(context.Background(), orderID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	out := make([]historyRow, 0, len(rows))
	for _, r := range rows {
		h := historyRow{to: r.ToStatus, actor: r.ActorType, notes: r.Notes}
		if r.FromStatus != nil {
			h.from = *r.FromStatus
		}
		out = append(out, h)
	}
	return out
}

func orderStatus(t *testing.T, st *Store, orderID uuid.UUID) (status, payStatus string) {
	t.Helper()
	if err := st.db.QueryRow(context.Background(),
		`SELECT status, payment_status FROM orders WHERE id = $1`, orderID).Scan(&status, &payStatus); err != nil {
		t.Fatalf("read order: %v", err)
	}
	return
}

func TestDB_SellerPacksFromConfirmedAndNotFromShipped(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()
	actor := uuid.New()

	o := newTestOrder(t, st, "confirmed", "paid")
	tr, err := st.PackOrder(ctx, o.ID, &actor, "seller")
	if err != nil || !tr.Applied || tr.From != "confirmed" || tr.To != "packed" {
		t.Fatalf("pack from confirmed: %+v, %v", tr, err)
	}
	if s, _ := orderStatus(t, st, o.ID); s != "packed" {
		t.Fatalf("status after pack = %s", s)
	}
	h := orderHistory(t, st, o.ID)
	if len(h) == 0 || h[len(h)-1] != (historyRow{from: "confirmed", to: "packed", actor: "seller", notes: h[len(h)-1].notes}) {
		t.Fatalf("history after pack = %+v; want a confirmed -> packed row by seller", h)
	}

	// Repeat: not a refusal, not a second history line.
	again, err := st.PackOrder(ctx, o.ID, &actor, "seller")
	if err != nil || again.Applied || again.To != "packed" {
		t.Fatalf("repeat pack: %+v, %v; want Applied=false, nil", again, err)
	}
	if got := len(orderHistory(t, st, o.ID)); got != len(h) {
		t.Errorf("repeat pack wrote history: %d rows, had %d", got, len(h))
	}

	shipped := newTestOrder(t, st, "shipped", "paid")
	if _, err := st.PackOrder(ctx, shipped.ID, &actor, "seller"); !errors.Is(err, ErrTransitionNotPermitted) {
		t.Fatalf("pack from shipped: %v; want ErrTransitionNotPermitted", err)
	}
	// And a customer may not pack at all: the matrix has no such row.
	c := newTestOrder(t, st, "confirmed", "paid")
	if _, err := st.PackOrder(ctx, c.ID, &actor, "customer"); !errors.Is(err, ErrTransitionNotPermitted) {
		t.Fatalf("pack by customer: %v; want ErrTransitionNotPermitted", err)
	}
}

func TestDB_SellerShipsThroughPackedWithBothStepsAudited(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()
	actor := uuid.New()

	o := newTestOrder(t, st, "confirmed", "paid")
	tr, err := st.MarkOrderShipped(ctx, o.ID, &actor, "seller", "shipment booked")
	if err != nil || !tr.Applied || tr.To != "shipped" {
		t.Fatalf("ship from confirmed as seller: %+v, %v", tr, err)
	}
	if s, _ := orderStatus(t, st, o.ID); s != "shipped" {
		t.Fatalf("status = %s, want shipped", s)
	}
	h := orderHistory(t, st, o.ID)
	if len(h) < 2 {
		t.Fatalf("history = %+v; want confirmed -> packed -> shipped", h)
	}
	a, b := h[len(h)-2], h[len(h)-1]
	if a.from != "confirmed" || a.to != "packed" || a.actor != "seller" ||
		b.from != "packed" || b.to != "shipped" || b.actor != "seller" {
		t.Fatalf("history = %+v; want confirmed->packed and packed->shipped, both by seller", h)
	}

	// Already shipped: a repeat is a no-op.
	again, err := st.MarkOrderShipped(ctx, o.ID, &actor, "seller", "again")
	if err != nil || again.Applied {
		t.Fatalf("repeat ship: %+v, %v", again, err)
	}

	// From cancelled: refused, for anyone.
	cancelled := newTestOrder(t, st, "cancelled", "refund_pending")
	if _, err := st.MarkOrderShipped(ctx, cancelled.ID, &actor, "seller", ""); !errors.Is(err, ErrTransitionNotPermitted) {
		t.Fatalf("ship from cancelled: %v; want ErrTransitionNotPermitted", err)
	}
}

// The fulfilment worker books as "system". Migration 033 gives it the rows;
// before it the write was silently refused on a gated database.
func TestDB_TheWorkerShipsAsSystemUnderMigration033(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()

	var have bool
	if err := st.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM order_status_transitions
		WHERE from_status='confirmed' AND to_status='shipped' AND actor_type='system')`).Scan(&have); err != nil {
		t.Fatal(err)
	}
	if !have {
		t.Fatal("order_status_transitions has no confirmed -> shipped row for system: " +
			"apply database/migrations/033_system_ships_at_booking.sql to this database")
	}

	o := newTestOrder(t, st, "confirmed", "paid")
	tr, err := st.MarkOrderShipped(ctx, o.ID, nil, "system", "shipment booked")
	if err != nil || !tr.Applied || tr.To != "shipped" {
		t.Fatalf("ship as system: %+v, %v", tr, err)
	}
	h := orderHistory(t, st, o.ID)
	last := h[len(h)-1]
	if last.from != "confirmed" || last.to != "shipped" || last.actor != "system" {
		t.Fatalf("history = %+v; want one confirmed -> shipped row by system", h)
	}

	// Seller packed first, worker booked afterwards.
	p := newTestOrder(t, st, "packed", "paid")
	if tr, err := st.MarkOrderShipped(ctx, p.ID, nil, "system", ""); err != nil || !tr.Applied {
		t.Fatalf("ship packed as system: %+v, %v", tr, err)
	}
}

func TestDB_SellerCancelsFromPackedAndNotFromShipped(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()
	seller := uuid.New()

	o := newTestOrder(t, st, "packed", "paid")
	// The refund leg keys off the paise total, which the legacy-shaped
	// fixture leaves at zero.
	if _, err := st.db.Exec(ctx, `UPDATE orders SET final_amount_minor = 90000 WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelOrder(ctx, o.ID, seller, "seller", "damaged in the warehouse"); err != nil {
		t.Fatalf("cancel from packed as seller: %v", err)
	}
	s, ps := orderStatus(t, st, o.ID)
	if s != "cancelled" {
		t.Fatalf("status = %s, want cancelled", s)
	}
	// Money was captured, so the buyer is owed it back: same path as a
	// customer cancel.
	if ps != "refund_pending" {
		t.Errorf("payment_status = %s, want refund_pending", ps)
	}
	h := orderHistory(t, st, o.ID)
	last := h[len(h)-1]
	if last.from != "packed" || last.to != "cancelled" || last.actor != "seller" ||
		last.notes == nil || *last.notes != "damaged in the warehouse" {
		t.Fatalf("history = %+v; want packed -> cancelled by seller carrying the reason", h)
	}
	var cancelledBy, reason string
	if err := st.db.QueryRow(ctx, `SELECT cancelled_by, cancellation_reason FROM orders WHERE id=$1`, o.ID).
		Scan(&cancelledBy, &reason); err != nil {
		t.Fatal(err)
	}
	if cancelledBy != "seller" || reason != "damaged in the warehouse" {
		t.Errorf("cancelled_by/reason = %q/%q", cancelledBy, reason)
	}

	// Repeat: idempotent, no second history line.
	if err := st.CancelOrder(ctx, o.ID, seller, "seller", "again"); err != nil {
		t.Fatalf("repeat cancel: %v", err)
	}
	if got := len(orderHistory(t, st, o.ID)); got != len(h) {
		t.Errorf("repeat cancel wrote history: %d rows, had %d", got, len(h))
	}

	shipped := newTestOrder(t, st, "shipped", "paid")
	if err := st.CancelOrder(ctx, shipped.ID, seller, "seller", "too late"); !errors.Is(err, ErrCancelNotPermitted) {
		t.Fatalf("cancel from shipped as seller: %v; want ErrCancelNotPermitted", err)
	}
	// The same refusal for the buyer, on a database with or without the
	// gated trigger: the Go-side read of the matrix is what holds here.
	if err := st.CancelOrder(ctx, shipped.ID, shipped.CustomerUserID, "customer", "changed my mind"); !errors.Is(err, ErrCancelNotPermitted) {
		t.Fatalf("cancel from shipped as customer: %v; want ErrCancelNotPermitted", err)
	}
}
