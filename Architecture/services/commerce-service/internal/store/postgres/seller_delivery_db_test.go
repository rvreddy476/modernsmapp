//go:build integration

package postgres

// DB-backed proofs of the seller's "Mark delivered" (migration 054), against
// the real order_status_transitions table. Skips unless COMMERCE_TEST_DSN is
// set; point it at commerce_it_test.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestShipment(t *testing.T, st *Store, orderID, sellerID uuid.UUID, sellerManaged bool, shippedAt time.Time) *Shipment {
	t.Helper()
	awb := "IT" + uuid.NewString()[:12]
	sh := &Shipment{OrderID: orderID, SellerID: sellerID, Courier: "Delhivery", TrackingNumber: &awb,
		Status: "booked", ShippedAt: &shippedAt, SellerManaged: sellerManaged}
	if err := st.CreateShipment(context.Background(), sh); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.db.Exec(context.Background(), `DELETE FROM shipment_events WHERE shipment_id=$1`, sh.ID)
		_, _ = st.db.Exec(context.Background(), `DELETE FROM shipments WHERE id=$1`, sh.ID)
	})
	return sh
}

func TestDB_SellerMarksSelfShippedOrderDelivered(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()
	seller, actor := uuid.New(), uuid.New()
	shippedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	deliveredAt := time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC)
	note := "Left with the security desk"

	o := newTestOrder(t, st, "shipped", "paid")
	sh := newTestShipment(t, st, o.ID, seller, true, shippedAt)

	tr, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{
		OrderID: o.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: deliveredAt, Note: &note,
	})
	if err != nil || !tr.Applied || tr.From != "shipped" || tr.To != "delivered" {
		t.Fatalf("mark delivered: %+v, %v", tr, err)
	}
	if s, _ := orderStatus(t, st, o.ID); s != "delivered" {
		t.Fatalf("order status = %s, want delivered", s)
	}
	h := orderHistory(t, st, o.ID)
	last := h[len(h)-1]
	if last.from != "shipped" || last.to != "delivered" || last.actor != "seller" ||
		last.notes == nil || *last.notes != SellerDeliveryNote {
		t.Fatalf("history = %+v; want shipped -> delivered by seller with the seller note", h)
	}
	got, err := st.GetShipmentByOrderAndSeller(ctx, o.ID, seller)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "delivered" || got.DeliveredAt == nil || !got.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("shipment = %+v; want delivered at %s", got, deliveredAt)
	}
	c := got.DeliveryConfirmation
	if c == nil || c.ConfirmedBy != ConfirmedBySeller || !c.DeliveredAt.Equal(deliveredAt) || c.Note == nil || *c.Note != note {
		t.Fatalf("delivery confirmation = %+v; want the seller's", c)
	}
	evts, _ := st.ListShipmentEvents(ctx, sh.ID)
	if len(evts) == 0 || evts[0].Status != "delivered" {
		t.Fatalf("shipment events = %+v; want a delivered event", evts)
	}

	// A repeat: not a refusal, no second history row.
	again, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{OrderID: o.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: deliveredAt})
	if err != nil || again.Applied {
		t.Fatalf("repeat: %+v, %v; want Applied=false, nil", again, err)
	}
	if n := len(orderHistory(t, st, o.ID)); n != len(h) {
		t.Errorf("repeat wrote history: %d rows, had %d", n, len(h))
	}
}

func TestDB_SellerCannotMarkACourierTrackedOrderDelivered(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()
	seller, actor := uuid.New(), uuid.New()

	o := newTestOrder(t, st, "shipped", "paid")
	newTestShipment(t, st, o.ID, seller, false, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	_, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{OrderID: o.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: time.Now()})
	if !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("courier-tracked: %v; want ErrShipmentNotSellerManaged", err)
	}
	if s, _ := orderStatus(t, st, o.ID); s != "shipped" {
		t.Fatalf("order moved to %s on a refused mark", s)
	}

	// Another seller's self-shipped parcel is not this seller's either.
	other := newTestOrder(t, st, "shipped", "paid")
	newTestShipment(t, st, other.ID, uuid.New(), true, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if _, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{OrderID: other.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: time.Now()}); !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("someone else's shipment: %v; want ErrShipmentNotSellerManaged", err)
	}

	// No shipment at all.
	bare := newTestOrder(t, st, "shipped", "paid")
	if _, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{OrderID: bare.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: time.Now()}); !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("no shipment: %v; want ErrShipmentNotSellerManaged", err)
	}
}

// The matrix, not Go, refuses a seller's delivered from a state that is not
// in flight: a cancelled order with a self-shipped parcel stays cancelled.
func TestDB_SellerMarkDeliveredRefusedOutsideShipped(t *testing.T) {
	st, _ := dbStore(t)
	ctx := context.Background()
	seller, actor := uuid.New(), uuid.New()

	o := newTestOrder(t, st, "cancelled", "refund_pending")
	newTestShipment(t, st, o.ID, seller, true, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if _, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{OrderID: o.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: time.Now()}); !errors.Is(err, ErrTransitionNotPermitted) {
		t.Fatalf("from cancelled: %v; want ErrTransitionNotPermitted", err)
	}

	ofd := newTestOrder(t, st, "out_for_delivery", "paid")
	newTestShipment(t, st, ofd.ID, seller, true, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if tr, err := st.MarkDeliveredBySeller(ctx, SellerDeliveryMark{OrderID: ofd.ID, SellerID: seller, ActorUserID: actor, DeliveredAt: time.Now()}); err != nil || !tr.Applied {
		t.Fatalf("from out_for_delivery: %+v, %v; want applied", tr, err)
	}
}
