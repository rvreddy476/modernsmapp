// Order reads: buyer order lists and cards with their cursor, order detail
// (including the opened delivery address), and the seller order and
// fulfilment queues. Moved out of service.go unchanged.

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	// HP3: pagination clamped (default 20, max 200).
	limit, offset = clampListPagination(limit, offset)
	orders, _, err := s.store.GetOrdersByCustomer(ctx, userID, limit, offset)
	return orders, err
}

// ListOrderCardsResult is the customer order-list response — Phase 2.1.
// NextCursor is empty on the last page.
type ListOrderCardsResult struct {
	Items      []postgres.OrderCard
	NextCursor string
}

// ListOrderCards returns one page of order cards for the customer using
// keyset pagination over (created_at, id). The richer shape (item count,
// seller count, first item) replaces the old anemic list the customer
// couldn't tell orders apart from. Replaces the offset/COUNT(*) path —
// no more table-scan on every page.
func (s *Service) ListOrderCards(ctx context.Context, userID uuid.UUID, limit int, cursor string) (*ListOrderCardsResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		t, id, err := decodeOrderCursor(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor")
		}
		cursorTime = &t
		cursorID = &id
	}
	cards, hasMore, err := s.store.ListOrderCardsByCustomer(ctx, userID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, err
	}
	s.hydrateOrderCardImages(ctx, cards)
	res := &ListOrderCardsResult{Items: cards}
	if hasMore && len(cards) > 0 {
		last := cards[len(cards)-1]
		res.NextCursor = encodeOrderCursor(last.CreatedAt, last.ID)
	}
	return res, nil
}

// encodeOrderCursor / decodeOrderCursor are deliberately opaque to the
// client. Format is rfc3339nano|uuid wrapped in URL-safe base64; bumping
// the format only requires a server-side change because clients never
// crack it open.
func encodeOrderCursor(t time.Time, id uuid.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func decodeOrderCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, id, nil
}

// GetOrderWithItems returns the order and its line items, verifying ownership.
func (s *Service) GetOrderWithItems(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, []*postgres.OrderItem, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	if order.CustomerUserID != userID {
		return nil, nil, fmt.Errorf("order not found")
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	s.hydrateOrderItemImages(ctx, items)
	return order, items, nil
}

// ListSellerOrders returns orders for a seller — used by the seller fulfillment dashboard.
// Authorization: caller must own the seller account (verified in handler).
// HP3: pagination clamped (default 20, max 200).
func (s *Service) ListSellerOrders(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	limit, offset = clampListPagination(limit, offset)
	orders, _, err := s.store.GetOrdersBySeller(ctx, sellerID, limit, offset)
	return orders, err
}

// SellerOrderCard is the rich DTO the fulfillment dashboard renders for one
// order containing the seller's items. The customer's other-seller items in
// a multi-seller order are intentionally excluded — sellers only see what
// they're responsible for shipping.
type SellerOrderCard struct {
	Order    *postgres.Order       `json:"order"`
	Items    []*postgres.OrderItem `json:"items"`
	Shipment *postgres.Shipment    `json:"shipment,omitempty"`

	// SellerSubtotalMinor is what this seller is owed for THIS order, in
	// paise, summed over their own lines.
	//
	// It reported 0 on a ₹929 order because it was summed from
	// `order_items.final_price` — the NUMERIC column migration 007 stopped
	// maintaining. Nothing was wrong with the arithmetic; it was adding up
	// a column nobody writes. The sum is now over `final_price_minor`
	// (with the pre-007 rupee fallback inside OrderItem.LineTotalMinor).
	SellerSubtotalMinor int64 `json:"seller_subtotal_minor"`
	// SellerSubtotal is the same figure in rupees, kept because the
	// existing dashboard reads this name. Derived from the paise, never
	// summed independently, so the two cannot disagree.
	SellerSubtotal float64 `json:"seller_subtotal"` // money-exempt: rupee mirror of seller_subtotal_minor, derived not computed

	DeliveryAddress []byte `json:"delivery_address,omitempty"`
}

// sellerLines splits an order's items into the ones belonging to sellerID
// and totals them in paise. One definition, so the fulfillment list and the
// order detail can never disagree about what a seller is owed.
func sellerLines(items []*postgres.OrderItem, sellerID uuid.UUID) ([]*postgres.OrderItem, int64) {
	mine := make([]*postgres.OrderItem, 0, len(items))
	var subtotalMinor int64
	for _, it := range items {
		if it.SellerID == sellerID {
			mine = append(mine, it)
			subtotalMinor += it.LineTotalMinor()
		}
	}
	return mine, subtotalMinor
}

// ListSellerFulfillment returns the seller's fulfillment queue, optionally
// filtered by stage:
//
//	stage="" / "all"     — every order with the seller's items
//	stage="unshipped"    — paid but no shipment booked for this seller yet
//	stage="in_transit"   — shipment booked, not yet delivered
//	stage="delivered"    — shipment delivered
//	stage="cancelled"    — order cancelled
//
// The DTO carries only this seller's items even in multi-seller orders.
// Phase 4.2 — replaces the bare ListSellerOrders surface so the dashboard
// can render item-level state in one round trip.
func (s *Service) ListSellerFulfillment(ctx context.Context, sellerID uuid.UUID, stage string, limit, offset int) ([]*SellerOrderCard, error) {
	// HP3: clamp pagination so a malicious / sloppy client can't ask for
	// 100k rows. Same defaults as other seller surfaces (20/200).
	limit, offset = clampListPagination(limit, offset)
	orders, _, err := s.store.GetOrdersBySeller(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return []*SellerOrderCard{}, nil
	}

	// HP2: was 2 queries per order (items + shipment); now 2 queries
	// total for the whole page. 20-order page drops from 41 to 3 round
	// trips (orders + items batch + shipments batch).
	orderIDs := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		orderIDs = append(orderIDs, o.ID)
	}
	itemsByOrder, err := s.store.GetOrderItemsByOrderIDs(ctx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("batch order items: %w", err)
	}
	// ONE media batch for the whole page, flattened across every order —
	// hydrating per order would turn a 20-order page back into 20 sequential
	// calls to media-service, which is the N+1 this batching block exists to
	// remove.
	{
		flat := make([]*postgres.OrderItem, 0, len(orderIDs)*2)
		for _, id := range orderIDs {
			flat = append(flat, itemsByOrder[id]...)
		}
		s.hydrateOrderItemImages(ctx, flat)
	}
	shipmentsByOrder, err := s.store.GetShipmentsByOrderAndSeller(ctx, orderIDs, sellerID)
	if err != nil {
		return nil, fmt.Errorf("batch shipments: %w", err)
	}

	out := make([]*SellerOrderCard, 0, len(orders))
	for _, o := range orders {
		mine, subtotalMinor := sellerLines(itemsByOrder[o.ID], sellerID)
		if len(mine) == 0 {
			continue
		}
		card := &SellerOrderCard{
			Order:               o,
			Items:               mine,
			Shipment:            shipmentsByOrder[o.ID],
			SellerSubtotalMinor: subtotalMinor,
			SellerSubtotal:      round2(float64(subtotalMinor) / 100.0),
			DeliveryAddress:     o.DeliveryAddressSnapshot,
		}
		if !fulfillmentMatchesStage(card, stage) {
			continue
		}
		out = append(out, card)
	}
	return out, nil
}

// clampListPagination applies the standard commerce list-endpoint
// defaults: limit 20, max 200, non-negative offset. Centralised so the
// HP3 pagination caps are consistent across service-layer entry points
// regardless of which HTTP handler parsed the query string.
func clampListPagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// GetSellerOrderDetail returns a single order from the seller's perspective —
// their items + their shipment + the buyer's delivery snapshot — used by
// the seller order-detail page. Fails if the caller has no items in the
// order (authorization).
func (s *Service) GetSellerOrderDetail(ctx context.Context, sellerID, orderID uuid.UUID) (*SellerOrderCard, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	items, _ := s.store.GetOrderItems(ctx, orderID)
	mine, subtotalMinor := sellerLines(items, sellerID)
	if len(mine) == 0 {
		return nil, ErrNotOrderOwner
	}
	shipment, _ := s.store.GetShipmentByOrderAndSeller(ctx, orderID, sellerID)
	return &SellerOrderCard{
		Order:               order,
		Items:               mine,
		Shipment:            shipment,
		SellerSubtotalMinor: subtotalMinor,
		SellerSubtotal:      round2(float64(subtotalMinor) / 100.0),
		DeliveryAddress:     order.DeliveryAddressSnapshot,
	}, nil
}

// fulfillmentMatchesStage routes a card into the dashboard tab buckets so
// callers can filter server-side without each tab issuing its own query.
func fulfillmentMatchesStage(card *SellerOrderCard, stage string) bool {
	switch stage {
	case "", "all":
		return true
	case "unshipped":
		return card.Order.Status != "cancelled" &&
			card.Order.PaymentStatus == "paid" &&
			(card.Shipment == nil || card.Shipment.Status == "pending")
	case "in_transit":
		return card.Shipment != nil &&
			card.Shipment.Status != "delivered" &&
			card.Shipment.Status != "pending" &&
			card.Order.Status != "cancelled"
	case "delivered":
		return card.Shipment != nil && card.Shipment.Status == "delivered"
	case "cancelled":
		return card.Order.Status == "cancelled"
	}
	return true
}

func (s *Service) GetOrder(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil || order.CustomerUserID != userID {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

// ─── Order detail (the buyer's order screen) ─────────────────────────

// OrderDetail is one order as the buyer's order screen renders it.
//
// ─── WHY THIS TYPE EXISTS AT ALL ────────────────────────────────────────
//
// GET /v1/commerce/orders/:orderId used to return the raw `postgres.Order`
// row. Three things followed from that, all of them visible on the screen:
//
//	₹0.00 everywhere   the row's money fields are the NUMERIC columns
//	                   migration 007 stopped maintaining. The LIST endpoint
//	                   had already moved to `*_minor` (see OrderCard); detail
//	                   had not, so the same order showed ₹929 in the list and
//	                   ₹0.00 when you opened it.
//	no lines           the row is the order header. Items lived behind a
//	                   second call the app was not making.
//	no address         `delivery_address_snapshot` went out as a base64 blob
//	                   of a JSON object. After the PII cutover that blob is
//	                   the ROUTING fields only — no name, no phone, no
//	                   street — so it is not merely inconvenient to render,
//	                   it is not the address any more. The real one is in
//	                   `delivery_address_snapshot_enc` and has to be opened
//	                   through the cipher.
//
// And `can_cancel`, which the screen needs to decide whether to draw the
// cancel button at all, existed nowhere: the app was guessing from `status`
// against its own copy of the rules.
//
// The money field names match OrderCard exactly, so the list and the detail
// deserialise into the same client model.
type OrderDetail struct {
	ID          uuid.UUID `json:"id"`
	OrderNumber string    `json:"order_number"`

	SubtotalMinor money.Paise `json:"subtotal_minor"`
	DiscountMinor money.Paise `json:"discount_minor"`
	ShippingMinor money.Paise `json:"shipping_minor"`
	TaxMinor      money.Paise `json:"tax_minor"`
	TotalMinor    money.Paise `json:"total_minor"`
	Currency      string      `json:"currency"`

	// CouponCode is the coupon the discount came from, when one was applied.
	// DiscountMinor above includes its discount.
	CouponCode *string `json:"coupon_code,omitempty"`

	// PaymentOffer is the bank offer applied at payment (payments-service's
	// Razorpay Offers), or null. It does not change the order's value:
	// TotalMinor is still what the order cost, and AmountPaidMinor is what
	// the buyer actually paid through the bank's offer.
	PaymentOffer *OrderPaymentOfferView `json:"payment_offer"`
	// AmountPaidMinor is the money captured: TotalMinor less any bank offer,
	// once the order is paid; null before then.
	AmountPaidMinor *int64 `json:"amount_paid_minor"`

	PaymentMethod *string `json:"payment_method,omitempty"`
	PaymentStatus string  `json:"payment_status"`
	Status        string  `json:"status"`

	Items []OrderDetailItem `json:"items"`

	// DeliveryAddress is the decrypted order snapshot — the address as it
	// was when the order was placed, not the address the customer has now.
	// Nil when the order predates the snapshot, or when the cipher is not
	// configured; the rest of the order still renders in that case, because
	// a missing address is not a reason to fail an order screen.
	DeliveryAddress *pii.Address `json:"delivery_address,omitempty"`

	CanCancel bool `json:"can_cancel"`
	// CanRetryPayment is true only in payment_failed, for the payer: the
	// state POST /orders/:id/payment/intent re-reserves stock from. See
	// CanRetryPayment and RetryPaymentForOrder.
	CanRetryPayment bool    `json:"can_retry_payment"`
	TrackingURL     *string `json:"tracking_url,omitempty"`

	CreatedAt      time.Time `json:"created_at"`
	CreatedAtEpoch int64     `json:"created_at_epoch"`
}

// OrderDetailItem is one line of the order, in paise.
type OrderDetailItem struct {
	ID             uuid.UUID   `json:"id"`
	ProductID      uuid.UUID   `json:"product_id"`
	VariantID      uuid.UUID   `json:"variant_id"`
	SellerID       uuid.UUID   `json:"seller_id"`
	ProductTitle   string      `json:"product_title"`
	SKU            string      `json:"sku"`
	Quantity       int         `json:"quantity"`
	UnitMRPMinor   money.Paise `json:"unit_mrp_minor"`
	UnitPriceMinor money.Paise `json:"unit_price_minor"`
	TaxMinor       money.Paise `json:"tax_minor"`
	LineTotalMinor money.Paise `json:"line_total_minor"`
	Status         string      `json:"status"`
	TrackingNumber *string     `json:"tracking_number,omitempty"`
	// The line's picture, resolved through media-service in one batch for
	// the whole order. Empty when the product has no image or media-service
	// could not answer — the client draws a placeholder.
	ImageURL     string `json:"image_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

// GetOrderDetail assembles the buyer's order screen: totals in paise, the
// line items, the decrypted delivery address, whether the cancel button
// applies, and a tracking URL once a shipment carries one.
//
// Ownership is checked the same way GetOrder checks it — a stranger gets
// "not found" rather than a 403, so order ids cannot be probed.
func (s *Service) GetOrderDetail(ctx context.Context, orderID, userID uuid.UUID) (*OrderDetail, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, postgres.ErrOrderNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	if order == nil || order.CustomerUserID != userID {
		return nil, ErrOrderNotFound
	}

	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, err
	}
	// One media batch for the whole order, before the lines are projected.
	s.hydrateOrderItemImages(ctx, items)
	lines := make([]OrderDetailItem, 0, len(items))
	for _, it := range items {
		lines = append(lines, OrderDetailItem{
			ID:             it.ID,
			ProductID:      it.ProductID,
			VariantID:      it.VariantID,
			SellerID:       it.SellerID,
			ProductTitle:   it.ProductTitle,
			SKU:            it.SKU,
			Quantity:       it.Quantity,
			UnitMRPMinor:   money.Paise(it.UnitMRPMinor),
			UnitPriceMinor: money.Paise(it.UnitPriceMinor),
			TaxMinor:       money.Paise(it.TaxAmountMinor),
			LineTotalMinor: money.Paise(it.LineTotalMinor()),
			Status:         it.Status,
			TrackingNumber: it.TrackingNumber,
			ImageURL:       it.ImageURL,
			ThumbnailURL:   it.ThumbnailURL,
		})
	}

	// DiscountMinor counts the coupon: checkout writes it to
	// coupon_discount_minor, so subtotal − discount + shipping = total only
	// when both are counted.
	out := &OrderDetail{
		ID:              order.ID,
		OrderNumber:     order.OrderNumber,
		SubtotalMinor:   money.Paise(order.SubtotalMinorValue()),
		DiscountMinor:   money.Paise(order.DiscountMinorValue() + order.CouponDiscountMinor),
		ShippingMinor:   money.Paise(order.ShippingMinorValue()),
		TaxMinor:        money.Paise(order.TaxMinorValue()),
		TotalMinor:      money.Paise(order.TotalMinor()),
		Currency:        coalesceStr(order.CurrencyCode, "INR"),
		CouponCode:      order.CouponCode,
		PaymentMethod:   order.PaymentMethod,
		PaymentStatus:   order.PaymentStatus,
		Status:          order.Status,
		Items:           lines,
		CanCancel:       postgres.CustomerCanCancel(order.Status),
		CanRetryPayment: CanRetryPayment(order, userID),
		CreatedAt:       order.CreatedAt,
		CreatedAtEpoch:  order.CreatedAt.Unix(),
	}

	out.DeliveryAddress = s.openOrderAddress(ctx, order)

	offer, err := s.store.GetOrderPaymentOffer(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out.PaymentOffer, out.AmountPaidMinor = paymentOfferView(order, offer)

	// The first shipment that actually has a tracking URL. Multi-seller
	// orders can hold several; the buyer's screen shows one link and
	// GET /orders/:id/shipments carries the full set.
	if shipments, err := s.store.ListShipmentsByOrder(ctx, orderID); err == nil {
		for _, sh := range shipments {
			if sh != nil && sh.TrackingURL != nil && *sh.TrackingURL != "" {
				out.TrackingURL = sh.TrackingURL
				break
			}
		}
	} else {
		slog.Warn("order detail: could not load shipments", "order_id", orderID, "error", err)
	}

	return out, nil
}

// openOrderAddress decrypts the order's delivery-address snapshot.
//
// The sealed blob is the authority. The plaintext column is read only as
// the dual-write-window fallback the cutover mode permits — and after
// cutover that column holds routing fields alone, so serving it as "the
// address" would be quietly wrong rather than merely incomplete.
//
// Failures are logged and swallowed: an order screen that will not load
// because one field could not be decrypted is worse than one that renders
// the order without the address.
func (s *Service) openOrderAddress(ctx context.Context, order *postgres.Order) *pii.Address {
	if len(order.DeliveryAddressSnapshotEnc) > 0 && s.pii != nil {
		plain, err := s.pii.Open(ctx, pii.ScopeOrderSnapshot, order.DeliveryAddressSnapshotEnc)
		if err != nil {
			slog.Error("order detail: could not open the sealed address snapshot",
				"order_id", order.ID, "error", err)
			return nil
		}
		var addr pii.Address
		if err := json.Unmarshal([]byte(plain), &addr); err != nil {
			slog.Error("order detail: sealed address snapshot is not an address",
				"order_id", order.ID, "error", err)
			return nil
		}
		return &addr
	}
	if len(order.DeliveryAddressSnapshot) > 0 && s.piiCutover.AllowsPlaintextRead() {
		var addr pii.Address
		if err := json.Unmarshal(order.DeliveryAddressSnapshot, &addr); err != nil {
			slog.Error("order detail: plaintext address snapshot is not an address",
				"order_id", order.ID, "error", err)
			return nil
		}
		return &addr
	}
	return nil
}
