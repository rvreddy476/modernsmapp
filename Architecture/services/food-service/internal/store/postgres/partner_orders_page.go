package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
)

// Food server lane (9 Nov 2026): GET /v1/food/partner/restaurants/:id/orders
// takes an optional status filter and keyset cursor, so the Kitchen app can
// count its active orders instead of reading them out of the latest 100
// orders of any status. With neither, the call answers exactly as before:
// the latest PartnerOrdersDefaultLimit orders, newest first.

// PartnerOrdersDefaultLimit is the page size of the original call, and the cap.
const PartnerOrdersDefaultLimit = 100

var (
	ErrInvalidOrderCursor       = errors.New("invalid order cursor")
	ErrInvalidOrderStatusFilter = errors.New("invalid order status filter")
)

// partnerOrderStatuses are the food.order_status values a filter may name.
var partnerOrderStatuses = map[string]bool{
	orderstate.Draft: true, orderstate.Placed: true, orderstate.PaymentPending: true, orderstate.PaymentFailed: true,
	orderstate.Confirmed: true, orderstate.RestaurantRejected: true, orderstate.Preparing: true,
	orderstate.ReadyForPickup: true, orderstate.DeliveryAssigning: true, orderstate.DeliveryAssigned: true,
	orderstate.PickedUp: true, orderstate.OutForDelivery: true, orderstate.Delivered: true,
	orderstate.CancelledByCustomer: true, orderstate.CancelledByRestaurant: true, orderstate.CancelledByAdmin: true,
	orderstate.RefundPending: true, orderstate.Refunded: true, orderstate.Failed: true,
}

// OrderCursor is the position after the last order of a page: the list is
// ordered by (created_at, id) descending.
type OrderCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// PartnerOrderFilter narrows the restaurant's order list.
type PartnerOrderFilter struct {
	Statuses []string     // empty: every status
	After    *OrderCursor // nil: from the newest
	Limit    int          // 0: PartnerOrdersDefaultLimit
}

// ParsePartnerOrderStatuses reads ?status= values: repeated, comma separated
// or both, any case. An unknown status is refused, never silently dropped.
func ParsePartnerOrderStatuses(values []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			s := strings.ToUpper(strings.TrimSpace(part))
			if s == "" {
				continue
			}
			if !partnerOrderStatuses[s] {
				return nil, ErrInvalidOrderStatusFilter
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// EncodeOrderCursor is the opaque next_cursor a client sends back:
// "<created_at in Unix microseconds>.<order id>". Postgres keeps microseconds,
// so the position is exact.
func EncodeOrderCursor(c OrderCursor) string {
	return strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + "." + c.ID.String()
}

// DecodeOrderCursor reads a cursor EncodeOrderCursor wrote.
func DecodeOrderCursor(raw string) (OrderCursor, error) {
	at, id, ok := strings.Cut(raw, ".")
	if !ok {
		return OrderCursor{}, ErrInvalidOrderCursor
	}
	micros, err := strconv.ParseInt(at, 10, 64)
	if err != nil || micros <= 0 {
		return OrderCursor{}, ErrInvalidOrderCursor
	}
	orderID, err := uuid.Parse(id)
	if err != nil {
		return OrderCursor{}, ErrInvalidOrderCursor
	}
	return OrderCursor{CreatedAt: time.UnixMicro(micros).UTC(), ID: orderID}, nil
}

// partnerOrdersPageLimit clamps a requested page size to 1..100.
func partnerOrdersPageLimit(limit int) int {
	if limit <= 0 || limit > PartnerOrdersDefaultLimit {
		return PartnerOrdersDefaultLimit
	}
	return limit
}

// ListPartnerOrdersPage is one page of the owner's restaurant orders, newest
// first, and the cursor of the next page (nil on the last one). Only the
// restaurant's owner reads it (requireRestaurantOwner, as ListPartnerOrders).
func (s *Store) ListPartnerOrdersPage(ctx context.Context, ownerID, restaurantID uuid.UUID, f PartnerOrderFilter) ([]Order, *OrderCursor, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, nil, err
	}
	limit := partnerOrdersPageLimit(f.Limit)
	var statuses []string
	if len(f.Statuses) > 0 {
		statuses = f.Statuses
	}
	var afterAt *time.Time
	var afterID *uuid.UUID
	if f.After != nil {
		afterAt, afterID = &f.After.CreatedAt, &f.After.ID
	}
	// One row more than the page says whether another page follows.
	rows, err := s.db.Query(ctx, `
		SELECT id, order_number, user_id, restaurant_id, restaurant_name_snapshot,
			status::text, payment_status::text, payment_method::text,
			item_subtotal::float8, addon_total::float8, packaging_fee::float8,
			tax_total::float8, delivery_fee::float8, platform_fee::float8,
			restaurant_discount::float8, coupon_discount::float8, final_amount::float8,
			COALESCE(estimated_preparation_minutes, 0),
			COALESCE(estimated_delivery_minutes, 0),
			placed_at::text, COALESCE(delivered_at::text, ''),
			created_at
		FROM food.orders
		WHERE restaurant_id = $1
		  AND ($2::text[] IS NULL OR status::text = ANY($2::text[]))
		  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, restaurantID, statuses, afterAt, afterID, limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var orders []Order
	var created []time.Time
	for rows.Next() {
		var at time.Time
		order, err := scanOrder(rows, &at)
		if err != nil {
			return nil, nil, err
		}
		orders = append(orders, order)
		created = append(created, at)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	page, next := pageWithCursor(orders, created, limit)
	return page, next, nil
}

// pageWithCursor trims the look-ahead row: when more than limit orders were
// read, the page is the first limit and the cursor names its last order.
func pageWithCursor(orders []Order, created []time.Time, limit int) ([]Order, *OrderCursor) {
	if len(orders) <= limit {
		return orders, nil
	}
	last := limit - 1
	return orders[:limit], &OrderCursor{CreatedAt: created[last], ID: orders[last].ID}
}
