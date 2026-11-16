package service

// The seller's "Mark delivered" rule, every refusal by name. Pure: the store
// re-checks shipment and state under the row lock (DB proofs in
// store/postgres/seller_delivery_db_test.go).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	sdNow     = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	sdShipped = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
)

func sdShipment(seller uuid.UUID, managed bool, status string) *postgres.Shipment {
	at := sdShipped
	return &postgres.Shipment{SellerID: seller, SellerManaged: managed, Status: status, ShippedAt: &at, CreatedAt: at}
}

func TestMarkDeliveredAppliesToTheSellersOwnSelfShippedOrder(t *testing.T) {
	seller := uuid.New()
	for _, status := range []string{"shipped", "out_for_delivery"} {
		d, err := decideSellerMarkDelivered(status, []*postgres.Shipment{sdShipment(seller, true, "booked")}, seller, MarkDeliveredInput{}, sdNow)
		if err != nil || d.Repeat {
			t.Fatalf("%s: %+v, %v; want applied", status, d, err)
		}
		if !d.DeliveredAt.Equal(sdNow) {
			t.Errorf("%s: delivered_at = %s, want now when omitted", status, d.DeliveredAt)
		}
	}
}

func TestMarkDeliveredRefusesACourierTrackedShipment(t *testing.T) {
	seller := uuid.New()
	_, err := decideSellerMarkDelivered("shipped", []*postgres.Shipment{sdShipment(seller, false, "in_transit")}, seller, MarkDeliveredInput{}, sdNow)
	if !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("courier shipment: %v; want ErrShipmentNotSellerManaged", err)
	}
	// Even when it is already delivered: a courier delivery is never the
	// seller's "repeat".
	_, err = decideSellerMarkDelivered("delivered", []*postgres.Shipment{sdShipment(seller, false, "delivered")}, seller, MarkDeliveredInput{}, sdNow)
	if !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("courier-delivered: %v; want ErrShipmentNotSellerManaged", err)
	}
	// One self-shipped parcel beside a tracked one: refused as a whole.
	_, err = decideSellerMarkDelivered("shipped", []*postgres.Shipment{
		sdShipment(seller, true, "booked"), sdShipment(seller, false, "booked"),
	}, seller, MarkDeliveredInput{}, sdNow)
	if !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("mixed shipments: %v; want ErrShipmentNotSellerManaged", err)
	}
	// A shipped order with no shipment row at all.
	if _, err := decideSellerMarkDelivered("shipped", nil, seller, MarkDeliveredInput{}, sdNow); !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("no shipment: %v; want ErrShipmentNotSellerManaged", err)
	}
}

func TestMarkDeliveredRefusesAnotherSellersShipment(t *testing.T) {
	seller := uuid.New()
	_, err := decideSellerMarkDelivered("shipped", []*postgres.Shipment{sdShipment(uuid.New(), true, "booked")}, seller, MarkDeliveredInput{}, sdNow)
	if !errors.Is(err, ErrShipmentNotSellerManaged) {
		t.Fatalf("other seller's shipment: %v; want ErrShipmentNotSellerManaged", err)
	}
}

func TestMarkDeliveredRefusesAnOrderThatIsNotInFlight(t *testing.T) {
	seller := uuid.New()
	for _, status := range []string{"confirmed", "packed", "cancelled", "payment_pending"} {
		var ships []*postgres.Shipment
		if status == "cancelled" {
			ships = []*postgres.Shipment{sdShipment(seller, true, "booked")}
		}
		_, err := decideSellerMarkDelivered(status, ships, seller, MarkDeliveredInput{}, sdNow)
		if !errors.Is(err, ErrOrderNotShipped) {
			t.Errorf("%s: %v; want ErrOrderNotShipped", status, err)
		}
		if err != nil && !strings.Contains(err.Error(), status) {
			t.Errorf("%s: message %q does not name the state", status, err)
		}
	}
}

func TestMarkDeliveredIsIdempotent(t *testing.T) {
	seller := uuid.New()
	d, err := decideSellerMarkDelivered("delivered", []*postgres.Shipment{sdShipment(seller, true, "delivered")}, seller, MarkDeliveredInput{}, sdNow)
	if err != nil || !d.Repeat {
		t.Fatalf("delivered: %+v, %v; want a repeat", d, err)
	}
	// After delivery the buyer may have asked for a return; the seller's
	// retried mark is still a repeat, not a 409.
	d, err = decideSellerMarkDelivered("return_requested", []*postgres.Shipment{sdShipment(seller, true, "delivered")}, seller, MarkDeliveredInput{}, sdNow)
	if err != nil || !d.Repeat {
		t.Fatalf("return_requested: %+v, %v; want a repeat", d, err)
	}
}

func TestMarkDeliveredAtIsNeitherFutureNorBeforeShipping(t *testing.T) {
	seller := uuid.New()
	ships := []*postgres.Shipment{sdShipment(seller, true, "booked")}

	future := sdNow.Add(time.Hour)
	if _, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{DeliveredAt: &future}, sdNow); !errors.Is(err, ErrDeliveredAtInFuture) {
		t.Fatalf("future: %v; want ErrDeliveredAtInFuture", err)
	}
	before := sdShipped.Add(-time.Minute)
	if _, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{DeliveredAt: &before}, sdNow); !errors.Is(err, ErrDeliveredAtBeforeShipped) {
		t.Fatalf("before shipped: %v; want ErrDeliveredAtBeforeShipped", err)
	}
	// A phone a few seconds fast is "now", stored as the server's now.
	skew := sdNow.Add(30 * time.Second)
	d, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{DeliveredAt: &skew}, sdNow)
	if err != nil || !d.DeliveredAt.Equal(sdNow) {
		t.Fatalf("clock skew: %+v, %v; want stored as now", d, err)
	}
	ok := time.Date(2026, 9, 7, 15, 0, 0, 0, time.FixedZone("IST", 19800))
	d, err = decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{DeliveredAt: &ok}, sdNow)
	if err != nil || !d.DeliveredAt.Equal(ok) || d.DeliveredAt.Location() != time.UTC {
		t.Fatalf("valid: %+v, %v; want %s in UTC", d, err, ok)
	}
	// Exactly at shipped_at is allowed.
	at := sdShipped
	if _, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{DeliveredAt: &at}, sdNow); err != nil {
		t.Fatalf("at shipped_at: %v", err)
	}
}

func TestMarkDeliveredNoteAndProof(t *testing.T) {
	seller := uuid.New()
	ships := []*postgres.Shipment{sdShipment(seller, true, "booked")}

	long := strings.Repeat("ā", MaxDeliveryNoteRunes+1)
	if _, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{Note: long}, sdNow); !errors.Is(err, ErrDeliveryNoteTooLong) {
		t.Fatalf("long note: %v; want ErrDeliveryNoteTooLong", err)
	}
	exact := strings.Repeat("ā", MaxDeliveryNoteRunes)
	if _, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{Note: exact}, sdNow); err != nil {
		t.Fatalf("note at the limit (runes, not bytes): %v", err)
	}
	d, err := decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{Note: "   ", ProofMediaID: &uuid.Nil}, sdNow)
	if err != nil || d.Note != nil || d.Proof != nil {
		t.Fatalf("blank note / nil proof: %+v, %v; want both absent", d, err)
	}
	proof := uuid.New()
	d, err = decideSellerMarkDelivered("shipped", ships, seller, MarkDeliveredInput{Note: " Left at the door ", ProofMediaID: &proof}, sdNow)
	if err != nil || d.Note == nil || *d.Note != "Left at the door" || d.Proof == nil || *d.Proof != proof {
		t.Fatalf("note + proof: %+v, %v", d, err)
	}
}

func TestFirstDeliveryConfirmation(t *testing.T) {
	if firstDeliveryConfirmation(nil) != nil {
		t.Fatal("no shipments: want nil")
	}
	c := &postgres.DeliveryConfirmation{ConfirmedBy: postgres.ConfirmedBySeller}
	got := firstDeliveryConfirmation([]*postgres.Shipment{{}, {DeliveryConfirmation: c}})
	if got != c {
		t.Fatalf("got %+v, want the seller's confirmation", got)
	}
}
