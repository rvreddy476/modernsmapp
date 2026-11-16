package service

// Seller "Mark delivered" (migration 054).
//
// A seller who ships with their own courier records the courier and tracking
// number at ship time (applyManualBooking). No carrier webhook ever reports
// such a parcel, so its order stayed `shipped` forever: no delivered notice,
// no review, no delivered tab, no COD remittance. The seller may now say it
// was delivered, and only then:
//
//   - the caller is the seller of every line on the order (requireWholeOrder);
//   - every shipment on the order is seller-managed: a courier- or
//     delivery-partner-tracked parcel is delivered by its tracker's scan,
//     never by the seller's word;
//   - the order is shipped or out_for_delivery.
//
// The write is the courier path's: the D6 matrix (with the seller as actor),
// the history row, every line to delivered with the server's clock as its
// delivered_at (the return window and the payout read that, exactly as for a
// courier delivery), the shipment's status and event, then
// commerce.order.delivered and the COD remittance, as HandleShipmentWebhook
// does. The buyer's order screen carries delivery_confirmation, so a
// seller's mark is shown as such and never as a courier scan.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrOrderNotShipped: the order is not in a state a seller can mark
	// delivered from (not shipped yet, cancelled, returned …).
	ErrOrderNotShipped = errors.New("only a shipped order can be marked delivered")
	// ErrDeliveredAtInFuture: delivered_at is later than now.
	ErrDeliveredAtInFuture = errors.New("delivered_at is in the future")
	// ErrDeliveredAtBeforeShipped: delivered_at is before the parcel shipped.
	ErrDeliveredAtBeforeShipped = errors.New("delivered_at is before the order was shipped")
	// ErrDeliveryNoteTooLong: the note is over MaxDeliveryNoteRunes.
	ErrDeliveryNoteTooLong = errors.New("the delivery note is too long")
)

// ErrShipmentNotSellerManaged is the store's refusal, re-exported so the
// handler maps one name.
var ErrShipmentNotSellerManaged = postgres.ErrShipmentNotSellerManaged

// MaxDeliveryNoteRunes caps the seller's note to the buyer.
const MaxDeliveryNoteRunes = 500

// deliveredAtClockSkew is how far ahead of the server a client's
// delivered_at may be and still count as "now": a phone a few seconds fast
// must not be refused for sending its own clock. Such a value is stored as
// the server's now, so nothing stored is ever in the future.
const deliveredAtClockSkew = 2 * time.Minute

// MarkDeliveredInput is the optional body of POST
// /seller/orders/:orderId/mark-delivered.
type MarkDeliveredInput struct {
	DeliveredAt  *time.Time
	Note         string
	ProofMediaID *uuid.UUID
}

// SellerMarkDeliveredResult is the route's body. Applied is false on a
// repeat (the order was already delivered). DeliveryConfirmation is what the
// buyer is shown; null when the order was delivered some other way (the
// dev stub courier's timer).
type SellerMarkDeliveredResult struct {
	OrderID              uuid.UUID                      `json:"order_id"`
	Status               string                         `json:"status"`
	Applied              bool                           `json:"applied"`
	DeliveryConfirmation *postgres.DeliveryConfirmation `json:"delivery_confirmation"`
}

// markDeliveredDecision is what the pure rule decided.
type markDeliveredDecision struct {
	Repeat      bool
	DeliveredAt time.Time
	Note        *string
	Proof       *uuid.UUID
}

func isSellerShipment(sh *postgres.Shipment, sellerID uuid.UUID) bool {
	return sh != nil && sh.SellerManaged && sh.SellerID == sellerID
}

// decideSellerMarkDelivered is the whole rule, pure, so every refusal is
// tested without a database. The store re-checks the shipment and the
// state under the order's row lock (Store.MarkDeliveredBySeller).
func decideSellerMarkDelivered(status string, shipments []*postgres.Shipment, sellerID uuid.UUID, in MarkDeliveredInput, now time.Time) (markDeliveredDecision, error) {
	inFlight := status == "shipped" || status == "out_for_delivery"
	if len(shipments) == 0 {
		if inFlight || status == "delivered" {
			return markDeliveredDecision{}, ErrShipmentNotSellerManaged
		}
		return markDeliveredDecision{}, fmt.Errorf("%w (the order is %s)", ErrOrderNotShipped, status)
	}
	for _, sh := range shipments {
		if !isSellerShipment(sh, sellerID) {
			return markDeliveredDecision{}, ErrShipmentNotSellerManaged
		}
	}
	allDelivered := true
	for _, sh := range shipments {
		if !strings.EqualFold(sh.Status, "delivered") {
			allDelivered = false
			break
		}
	}
	if status == "delivered" || allDelivered {
		return markDeliveredDecision{Repeat: true}, nil
	}
	if !inFlight {
		return markDeliveredDecision{}, fmt.Errorf("%w (the order is %s)", ErrOrderNotShipped, status)
	}

	d := markDeliveredDecision{DeliveredAt: now}
	if note := strings.TrimSpace(in.Note); note != "" {
		if utf8.RuneCountInString(note) > MaxDeliveryNoteRunes {
			return markDeliveredDecision{}, ErrDeliveryNoteTooLong
		}
		d.Note = &note
	}
	if in.ProofMediaID != nil && *in.ProofMediaID != uuid.Nil {
		id := *in.ProofMediaID
		d.Proof = &id
	}
	if in.DeliveredAt != nil {
		at := in.DeliveredAt.UTC()
		switch {
		case at.After(now.Add(deliveredAtClockSkew)):
			return markDeliveredDecision{}, ErrDeliveredAtInFuture
		case at.After(now):
			at = now
		}
		d.DeliveredAt = at
	}
	// Not before the parcel left: the latest shipped_at on the order (a
	// shipment with none counts from when it was recorded).
	var shipped time.Time
	for _, sh := range shipments {
		at := sh.CreatedAt
		if sh.ShippedAt != nil {
			at = *sh.ShippedAt
		}
		if at.After(shipped) {
			shipped = at
		}
	}
	if d.DeliveredAt.Before(shipped) {
		return markDeliveredDecision{}, ErrDeliveredAtBeforeShipped
	}
	return d, nil
}

// SellerMarkDelivered is POST /seller/orders/:orderId/mark-delivered.
func (s *Service) SellerMarkDelivered(ctx context.Context, sellerID, sellerUserID, orderID uuid.UUID, in MarkDeliveredInput) (SellerMarkDeliveredResult, error) {
	order, err := s.requireWholeOrder(ctx, sellerID, orderID)
	if err != nil {
		return SellerMarkDeliveredResult{}, err
	}
	shipments, err := s.store.ListShipmentsByOrder(ctx, orderID)
	if err != nil {
		return SellerMarkDeliveredResult{}, err
	}
	d, err := decideSellerMarkDelivered(order.Status, shipments, sellerID, in, time.Now().UTC())
	if err != nil {
		return SellerMarkDeliveredResult{}, err
	}
	if d.Repeat {
		return SellerMarkDeliveredResult{OrderID: orderID, Status: order.Status, Applied: false,
			DeliveryConfirmation: firstDeliveryConfirmation(shipments)}, nil
	}
	// The proof photo is the seller's own upload, ready and moderated.
	if d.Proof != nil {
		if err := s.verifyMedia(ctx, sellerUserID, media.KindImage, d.Proof); err != nil {
			return SellerMarkDeliveredResult{}, err
		}
	}

	t, err := s.store.MarkDeliveredBySeller(ctx, postgres.SellerDeliveryMark{
		OrderID: orderID, SellerID: sellerID, ActorUserID: sellerUserID,
		DeliveredAt: d.DeliveredAt, Note: d.Note, ProofMediaID: d.Proof,
	})
	if err != nil {
		return SellerMarkDeliveredResult{}, err
	}

	after, err := s.store.ListShipmentsByOrder(ctx, orderID)
	if err != nil {
		slog.Warn("mark delivered: could not re-read shipments", "order_id", orderID, "error", err)
		after = shipments
	}
	if t.Applied {
		// What the courier webhook does on delivery, in the same order:
		// the delivered event (notification, and every consumer that keys
		// on it) once per order, then each shipment's COD remittance.
		fresh, _ := s.store.GetOrderByID(ctx, orderID)
		if fresh == nil {
			fresh = order
		}
		var shipmentID uuid.UUID
		if len(after) > 0 {
			shipmentID = after[0].ID
		}
		s.publishOrderDelivered(ctx, orderID, fresh, shipmentID, d.DeliveredAt)
		for _, sh := range after {
			s.recordCODRemittance(ctx, sh, fresh, d.DeliveredAt)
		}
	}
	return SellerMarkDeliveredResult{OrderID: orderID, Status: "delivered", Applied: t.Applied,
		DeliveryConfirmation: firstDeliveryConfirmation(after)}, nil
}

// firstDeliveryConfirmation is the order's non-courier delivery record, or
// nil. One per order in practice: a seller's mark covers every shipment.
func firstDeliveryConfirmation(shipments []*postgres.Shipment) *postgres.DeliveryConfirmation {
	for _, sh := range shipments {
		if sh != nil && sh.DeliveryConfirmation != nil {
			return sh.DeliveryConfirmation
		}
	}
	return nil
}
