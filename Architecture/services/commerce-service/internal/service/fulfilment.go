package service

// Seller-side fulfilment: pack, cancel, ship with a recorded courier, and
// read the audit trail.
//
// Migration 010's matrix has admitted confirmed -> packed and
// confirmed|packed -> cancelled for a seller since it landed, but no route
// ever let a seller perform either: the one cancel endpoint hard-coded the
// actor to "customer" and checked buyer ownership, and the only fulfilment
// write booked a courier with no body. The web and Android seller screens
// were built against transitions the server could not make.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrOrderSharedWithOtherSellers refuses an order-level move by a seller
	// who owns only some of the lines. Order status is one column for the
	// whole order; D2 (single seller per order) means this cannot arise
	// from the P0 checkout, but a legacy multi-seller order must not let
	// seller A cancel seller B's lines.
	ErrOrderSharedWithOtherSellers = errors.New("this order has lines from other sellers")
	// ErrCancelReasonRequired: the buyer reads the reason on their order
	// page, so a seller cancel without one is refused rather than recorded
	// as an unexplained refund.
	ErrCancelReasonRequired = errors.New("a cancellation reason is required")
)

// ShipmentBooking is what the caller of CreateShipmentsForOrder knows that
// the order row does not: who is booking, and (when the provider allows it)
// which courier and tracking number they used.
type ShipmentBooking struct {
	// ActorType is the actor the status transition runs as: "seller" from
	// the fulfilment routes, "system" from the worker. The matrix decides
	// what each may do.
	ActorType string
	ActorID   *uuid.UUID
	// Courier and TrackingNumber are honoured only when the provider opts in
	// through courier.ManualBookingProvider; see applyManualBooking.
	Courier        string
	TrackingNumber string
}

// applyManualBooking overwrites the adapter's booking with the seller's own
// courier and tracking number when, and only when, the provider accepts
// manual booking. Returns true when the seller's values were used.
//
// Under a carrier-backed provider the body is ignored, not merged: the AWB
// the carrier returned is the one its webhooks will name, so a seller-typed
// number would orphan every tracking update. Under the stub there is no
// carrier and no webhook, and the invented "STUB…" number is worth nothing,
// so the seller's values are the only true record. Label and tracking URLs
// are dropped with the invented AWB because they pointed at it.
func applyManualBooking(p courier.Provider, in ShipmentBooking, sh *postgres.Shipment) bool {
	c := strings.TrimSpace(in.Courier)
	awb := strings.TrimSpace(in.TrackingNumber)
	if c == "" && awb == "" {
		return false
	}
	if p == nil || !courier.AcceptsManualBooking(p) {
		return false
	}
	if c != "" {
		sh.Courier = c
	}
	if awb != "" {
		sh.TrackingNumber = &awb
	}
	sh.CourierOrderID = nil
	sh.LabelURL = nil
	sh.TrackingURL = nil
	return true
}

// bookingRemark is the first shipment event's text, so the timeline says
// which courier has the parcel and under which number without a join.
func bookingRemark(sh *postgres.Shipment, manual bool) string {
	awb := ""
	if sh.TrackingNumber != nil {
		awb = *sh.TrackingNumber
	}
	if manual {
		return fmt.Sprintf("recorded by seller: %s %s", sh.Courier, awb)
	}
	return fmt.Sprintf("booked with %s: %s", sh.Courier, awb)
}

// SellerFulfilmentResult is the body of every seller status write. Applied
// is false on an idempotent repeat: the order was already in `status`.
type SellerFulfilmentResult struct {
	OrderID uuid.UUID `json:"order_id"`
	Status  string    `json:"status"`
	Applied bool      `json:"applied"`
}

// requireWholeOrder checks that the seller owns every line of the order.
// ErrOrderNotFound for a missing order, ErrNotOrderOwner for a seller with
// no line on it (the caller decides whether that reads as 403 or 404), and
// ErrOrderSharedWithOtherSellers when they own some lines but not all.
func (s *Service) requireWholeOrder(ctx context.Context, sellerID, orderID uuid.UUID) (*postgres.Order, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, err
	}
	mine, _ := sellerLines(items, sellerID)
	if len(mine) == 0 {
		return nil, ErrNotOrderOwner
	}
	if len(mine) != len(items) {
		return nil, ErrOrderSharedWithOtherSellers
	}
	return order, nil
}

// SellerPackOrder: confirmed -> packed as the seller. Idempotent on repeat.
func (s *Service) SellerPackOrder(ctx context.Context, sellerID, sellerUserID, orderID uuid.UUID) (SellerFulfilmentResult, error) {
	if _, err := s.requireWholeOrder(ctx, sellerID, orderID); err != nil {
		return SellerFulfilmentResult{}, err
	}
	t, err := s.store.PackOrder(ctx, orderID, &sellerUserID, "seller")
	if err != nil {
		return SellerFulfilmentResult{}, err
	}
	return SellerFulfilmentResult{OrderID: orderID, Status: t.To, Applied: t.Applied}, nil
}

// SellerCancelOrder: confirmed|packed -> cancelled as the seller, through
// the SAME store path the customer cancel uses, so the reservation release,
// the restock of committed stock and the durable refund command happen
// exactly as they do for a buyer. Idempotent on repeat.
func (s *Service) SellerCancelOrder(ctx context.Context, sellerID, sellerUserID, orderID uuid.UUID, reason string) (SellerFulfilmentResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return SellerFulfilmentResult{}, ErrCancelReasonRequired
	}
	order, err := s.requireWholeOrder(ctx, sellerID, orderID)
	if err != nil {
		return SellerFulfilmentResult{}, err
	}
	if order.Status == "cancelled" {
		return SellerFulfilmentResult{OrderID: orderID, Status: "cancelled", Applied: false}, nil
	}
	if err := s.store.CancelOrder(ctx, orderID, sellerUserID, "seller", reason); err != nil {
		return SellerFulfilmentResult{}, err
	}
	return SellerFulfilmentResult{OrderID: orderID, Status: "cancelled", Applied: true}, nil
}

// SellerOrderHistory returns the order's status audit trail, oldest first,
// for a seller who owns lines on it.
func (s *Service) SellerOrderHistory(ctx context.Context, sellerID, orderID uuid.UUID) ([]*postgres.OrderStatusHistory, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if mine, _ := sellerLines(items, sellerID); len(mine) == 0 {
		return nil, ErrNotOrderOwner
	}
	return s.store.ListOrderStatusHistory(ctx, orderID)
}

// SellerOrderRow is one row of the seller's order list: the order header
// plus the two numbers a list needs and the header does not carry.
type SellerOrderRow struct {
	*postgres.Order
	// ItemCount is the number of THIS seller's lines on the order.
	ItemCount int `json:"item_count"`
	// SellerSubtotalMinor is what this seller is owed for the order, in
	// paise, summed over their own lines (see SellerOrderCard for why it is
	// summed from the paise column and not the rupee one).
	SellerSubtotalMinor int64 `json:"seller_subtotal_minor"`
}

// ListSellerOrderRows is ListSellerOrders with the seller's line count and
// subtotal attached, in one batched items query for the page.
func (s *Service) ListSellerOrderRows(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*SellerOrderRow, error) {
	orders, err := s.ListSellerOrders(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	rows := make([]*SellerOrderRow, 0, len(orders))
	if len(orders) == 0 {
		return rows, nil
	}
	ids := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.ID)
	}
	itemsByOrder, err := s.store.GetOrderItemsByOrderIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("batch order items: %w", err)
	}
	for _, o := range orders {
		mine, subtotal := sellerLines(itemsByOrder[o.ID], sellerID)
		rows = append(rows, &SellerOrderRow{Order: o, ItemCount: len(mine), SellerSubtotalMinor: subtotal})
	}
	return rows, nil
}
