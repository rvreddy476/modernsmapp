package service

// MStore order notices (lane C1, 2026-09-30).
//
// commerce-service publishes the order lifecycle on social.events.v1 with the
// parties in the payload (user_id for the buyer, seller_user_id for the
// seller, order_number). The consumer turns each event into OrderNotification
// values; this file renders and delivers them through the ordinary resolution
// path (suppression, the `orders` preference category, master toggle, quiet
// hours), under an identity so a redelivered event cannot notify twice.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Order notification types. Every one is registered in Templates and maps to
// the `orders` preference category (push_orders / inapp_orders).
const (
	OrderTypeSellerNewOrder = "seller_new_order"
	OrderTypeConfirmed      = "order_confirmed"
	OrderTypeShipped        = "order_shipped"
	OrderTypeDelivered      = "order_delivered"
	OrderTypeCancelled      = "order_cancelled"
	OrderTypeRefunded       = "order_refunded"
	OrderTypePaymentFailed  = "order_payment_failed"
)

// OrderNotificationTypes lists every order type, for the registry tests.
var OrderNotificationTypes = []string{
	OrderTypeSellerNewOrder, OrderTypeConfirmed, OrderTypeShipped, OrderTypeDelivered,
	OrderTypeCancelled, OrderTypeRefunded, OrderTypePaymentFailed,
}

// OrderEntityType is the entity every order notice points at.
const OrderEntityType = "order"

// OrderNotification is one recipient's notice about one order.
type OrderNotification struct {
	RecipientID uuid.UUID
	ActorID     uuid.UUID // the other party; uuid.Nil when unknown
	Type        string
	OrderID     uuid.UUID
	OrderNumber string
	DeepLink    string
	CreatedAt   time.Time
}

// Identity keys the idempotent inbox write: one notice per order, recipient
// and type, however often Kafka redelivers the event.
func (n OrderNotification) Identity() string {
	return n.OrderID.String() + ":" + n.RecipientID.String() + ":" + n.Type
}

// RenderOrderNotification renders the push copy from the registered template.
// An event without an order number still reads as a sentence: the
// "#{order_number}" token and the space before it are dropped.
func RenderOrderNotification(n OrderNotification) RenderOverride {
	t := GetTemplate(n.Type)
	render := func(tpl string) string {
		if n.OrderNumber == "" {
			tpl = strings.ReplaceAll(tpl, " #{order_number}", "")
			tpl = strings.ReplaceAll(tpl, "#{order_number}", "your order")
		}
		return RenderTitle(tpl, map[string]string{"order_number": n.OrderNumber})
	}
	return RenderOverride{
		Title: render(t.TitleTemplate),
		Body:  render(t.BodyTemplate),
		// Per order and recipient: "shipped" replaces "confirmed" on the
		// device rather than stacking beside it.
		CollapseKey: "order:" + n.OrderID.String() + ":" + n.RecipientID.String(),
	}
}

// CreateOrderNotification delivers one order notice.
func (s *Service) CreateOrderNotification(ctx context.Context, n OrderNotification) error {
	if s.recipientSuppressed(ctx, n.RecipientID, n.Type) {
		return nil
	}
	decision := s.resolveGeneralDelivery(ctx, n.RecipientID, n.Type)
	return s.deliverWithDecision(ctx, decision, n.RecipientID, n.ActorID, n.Type,
		OrderEntityType, n.OrderID, n.DeepLink, n.CreatedAt, n.Identity(), RenderOrderNotification(n))
}
