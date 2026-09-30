package postgres

// The parties an order event is addressed to (lane C1, section 2g).
//
// notification-service addresses a buyer notice by `user_id`, a seller notice
// by `seller_user_id`, and names the order by `order_number`; it never reads
// this database. The payment-lifecycle events were written with whatever the
// writing function happened to hold — `commerce.order.paid` had no order
// number and no seller, `payment_failed` and `refunded` had only the order id
// — so the seller never heard about a new order and the buyer never heard
// about a failed payment, a cancellation or a refund.
//
// Rather than edit each writer (two of them sit inside the settlement and
// refund paths, which are frozen), the outbox write fills the missing keys
// from the order row, in the SAME transaction, for exactly these event types.
// A key the writer set is never overwritten.

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// orderEventsCarryingParties are the order events whose payload gains the
// parties. Shipped and delivered are written by the service with them already.
var orderEventsCarryingParties = map[string]bool{
	"commerce.order.paid":           true,
	"commerce.order.payment_failed": true,
	"commerce.order.cancelled":      true,
	"commerce.order.refunded":       true,
}

// OrderEventParties is who an order event concerns.
type OrderEventParties struct {
	OrderID      uuid.UUID
	OrderNumber  string
	UserID       uuid.UUID
	BuyerEmail   string // the order's own invoice e-mail; "" when it holds none
	SellerID     uuid.UUID
	SellerUserID uuid.UUID
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// orderEventPartiesQ reads the parties with one primary-key lookup. The seller
// is the first line's: the bag is single-seller (D2), so every line agrees.
func orderEventPartiesQ(ctx context.Context, q rowQuerier, orderID uuid.UUID) (*OrderEventParties, error) {
	p := OrderEventParties{OrderID: orderID}
	var sellerID, sellerUserID *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT o.order_number, o.customer_user_id, COALESCE(o.invoice_email, ''), li.seller_id, s.user_id
		  FROM orders o
		  LEFT JOIN LATERAL (
		        SELECT oi.seller_id FROM order_items oi
		         WHERE oi.order_id = o.id
		         ORDER BY oi.created_at, oi.id LIMIT 1) li ON TRUE
		  LEFT JOIN sellers s ON s.id = li.seller_id
		 WHERE o.id = $1`, orderID).
		Scan(&p.OrderNumber, &p.UserID, &p.BuyerEmail, &sellerID, &sellerUserID)
	if err != nil {
		return nil, err
	}
	if sellerID != nil {
		p.SellerID = *sellerID
	}
	if sellerUserID != nil {
		p.SellerUserID = *sellerUserID
	}
	return &p, nil
}

// OrderEventParties reads the parties outside a transaction, for events the
// service publishes itself (the stub settlement's paid event).
func (s *Store) OrderEventParties(ctx context.Context, orderID uuid.UUID) (*OrderEventParties, error) {
	return orderEventPartiesQ(ctx, s.db, orderID)
}

// FillMissing adds every party key the payload does not already carry.
func (p *OrderEventParties) FillMissing(m map[string]any) {
	set := func(k string, v any) {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	set("order_id", p.OrderID)
	set("order_number", p.OrderNumber)
	set("user_id", p.UserID)
	set("buyer_email", p.BuyerEmail)
	if p.SellerID != uuid.Nil {
		set("seller_id", p.SellerID)
	}
	if p.SellerUserID != uuid.Nil {
		set("seller_user_id", p.SellerUserID)
	}
}

// withOrderPartiesTx is the outbox write's enrichment step. A payload that is
// not a map, names no order, or names an order that does not exist is passed
// through untouched: enrichment must never be the reason an event is lost.
func withOrderPartiesTx(ctx context.Context, tx pgx.Tx, eventType string, payload any) (any, error) {
	if !orderEventsCarryingParties[eventType] {
		return payload, nil
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return payload, nil
	}
	var orderID uuid.UUID
	switch v := m["order_id"].(type) {
	case uuid.UUID:
		orderID = v
	case string:
		id, err := uuid.Parse(v)
		if err != nil {
			return payload, nil
		}
		orderID = id
	default:
		return payload, nil
	}
	parties, err := orderEventPartiesQ(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return payload, nil
		}
		return nil, err
	}
	parties.FillMissing(m)
	return m, nil
}
