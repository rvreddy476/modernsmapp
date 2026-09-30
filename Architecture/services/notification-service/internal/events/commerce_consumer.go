package events

// MStore order notices (commerce-service → social.events.v1).
//
// Lane C1, 2026-09-30. commerce-service now puts the parties on every order
// lifecycle event (user_id, order_number, and on commerce.order.paid the
// seller's seller_user_id), so this consumer can address both sides:
//
//	commerce.order.paid            buyer  order_confirmed      + seller seller_new_order
//	commerce.order.shipped         buyer  order_shipped
//	commerce.order.delivered       buyer  order_delivered
//	commerce.order.cancelled       buyer  order_cancelled
//	commerce.order.refunded        buyer  order_refunded
//	commerce.order.payment_failed  buyer  order_payment_failed ("Payment failed, try again")
//	commerce.order.created         nothing — an order is not confirmed until it is paid,
//	                               and "order placed" before payment told buyers an
//	                               unpaid order was on its way
//
// Buyer deep link /shop/orders/{order_id}; seller /shop/sell/orders/{order_id}.
// The notices are delivered through service.CreateOrderNotification: the
// `orders` preference category, an idempotent inbox identity per order,
// recipient and type, and push data {type, entity_type: "order", entity_id,
// deep_link, title, body}.
//
// The receipt / shipping / invoice e-mails are unchanged and still need
// buyer_email on the payload.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/mailer"
	"github.com/google/uuid"
)

// Commerce event types. paid/shipped/delivered live in shared/events too.
const (
	commerceOrderCreated       = "commerce.order.created"
	commerceOrderPaid          = "commerce.order.paid"
	commerceOrderShipped       = "commerce.order.shipped"
	commerceOrderDelivered     = "commerce.order.delivered"
	commerceOrderCancelled     = "commerce.order.cancelled"
	commerceOrderRefunded      = "commerce.order.refunded"
	commerceOrderPaymentFailed = "commerce.order.payment_failed"
	commerceInvoiceIssued      = "commerce.invoice.issued"
	commerceSellerNewOrder     = "commerce.seller.new_order"
)

// commerceEventPayload is the shape commerce-service publishes for order
// lifecycle events. Fields are optional — the consumer uses what is present.
type commerceEventPayload struct {
	OrderID        string  `json:"order_id"`
	OrderNumber    string  `json:"order_number"`
	UserID         string  `json:"user_id"`
	SellerID       string  `json:"seller_id"` // sellers.id — NOT a user id
	SellerUserID   string  `json:"seller_user_id"`
	Amount         float64 `json:"amount"` // money-exempt: legacy rupee display value
	AmountMinor    int64   `json:"amount_minor"`
	PaymentID      string  `json:"payment_id"`
	PaymentMethod  string  `json:"payment_method"`
	ShipmentID     string  `json:"shipment_id"`
	TrackingNumber string  `json:"tracking_number"`
	TrackingURL    string  `json:"tracking_url"`
	Courier        string  `json:"courier"`
	InvoiceNumber  string  `json:"invoice_number"`
	InvoiceURL     string  `json:"invoice_url"`

	// Optional recipient info — commerce-service enriches when it can resolve the user.
	BuyerEmail  string `json:"buyer_email"`
	BuyerName   string `json:"buyer_name"`
	SellerEmail string `json:"seller_email"`
	SellerName  string `json:"seller_name"`
}

// commerceBuyerNotice is the buyer's notice type per event.
var commerceBuyerNotice = map[string]string{
	commerceOrderPaid:          service.OrderTypeConfirmed,
	commerceOrderShipped:       service.OrderTypeShipped,
	commerceOrderDelivered:     service.OrderTypeDelivered,
	commerceOrderCancelled:     service.OrderTypeCancelled,
	commerceOrderRefunded:      service.OrderTypeRefunded,
	commerceOrderPaymentFailed: service.OrderTypePaymentFailed,
}

// commerceNoticePlan is what one event should notify, and which payload
// fields it lacked for a notice it would otherwise have sent.
type commerceNoticePlan struct {
	notices []service.OrderNotification
	missing []string
}

func buyerOrderDeepLink(orderID uuid.UUID) string  { return "/shop/orders/" + orderID.String() }
func sellerOrderDeepLink(orderID uuid.UUID) string { return "/shop/sell/orders/" + orderID.String() }

// planCommerceNotices is the pure core: payload in, notices out.
func planCommerceNotices(eventType string, p commerceEventPayload, now time.Time) commerceNoticePlan {
	var plan commerceNoticePlan
	buyerType, buyerFacing := commerceBuyerNotice[eventType]
	if !buyerFacing {
		return plan
	}
	orderID, err := uuid.Parse(p.OrderID)
	if err != nil || orderID == uuid.Nil {
		plan.missing = append(plan.missing, "order_id")
		return plan
	}
	buyer, _ := uuid.Parse(p.UserID)
	sellerUser, _ := uuid.Parse(p.SellerUserID)

	if buyer != uuid.Nil {
		plan.notices = append(plan.notices, service.OrderNotification{
			RecipientID: buyer, ActorID: sellerUser, Type: buyerType,
			OrderID: orderID, OrderNumber: p.OrderNumber,
			DeepLink: buyerOrderDeepLink(orderID), CreatedAt: now,
		})
	} else {
		plan.missing = append(plan.missing, "user_id")
	}

	if eventType == commerceOrderPaid {
		// The seller hears about an order once it is PAID — the only point
		// at which there is something to pack.
		if sellerUser != uuid.Nil {
			plan.notices = append(plan.notices, service.OrderNotification{
				RecipientID: sellerUser, ActorID: buyer, Type: service.OrderTypeSellerNewOrder,
				OrderID: orderID, OrderNumber: p.OrderNumber,
				DeepLink: sellerOrderDeepLink(orderID), CreatedAt: now,
			})
		} else {
			plan.missing = append(plan.missing, "seller_user_id")
		}
	}
	return plan
}

// orderNotifier is the service seam; *service.Service satisfies it.
type orderNotifier interface {
	CreateOrderNotification(ctx context.Context, n service.OrderNotification) error
}

// handleCommerceEvent delivers an event's notices, then its e-mail.
func (c *Consumer) handleCommerceEvent(ctx context.Context, eventType string, raw []byte) error {
	var p commerceEventPayload
	if err := unmarshalPayload(raw, &p); err != nil {
		return err
	}
	plan := planCommerceNotices(eventType, p, time.Now())
	for _, field := range plan.missing {
		slog.Warn("commerce consumer: event lacks a notice recipient; commerce-service must add the field",
			"event", eventType, "field", field, "order_id", p.OrderID)
	}
	if c.orderNotify != nil {
		for _, n := range plan.notices {
			if err := c.orderNotify.CreateOrderNotification(ctx, n); err != nil {
				slog.Warn("commerce order notice failed", "type", n.Type, "order_id", n.OrderID, "error", err)
			}
		}
	}
	if eventType == commerceInvoiceIssued {
		// Unchanged legacy in-app notice for the invoice.
		orderUUID, _ := uuid.Parse(p.OrderID)
		buyerUUID, _ := uuid.Parse(p.UserID)
		if c.service != nil && orderUUID != uuid.Nil && buyerUUID != uuid.Nil {
			if err := c.service.CreateNotification(ctx, buyerUUID, uuid.Nil, "commerce_invoice_issued",
				service.OrderEntityType, orderUUID, buyerOrderDeepLink(orderUUID), time.Now()); err != nil {
				slog.Warn("commerce in-app notify failed", "type", "commerce_invoice_issued", "error", err)
			}
		}
	}
	c.sendCommerceEmail(ctx, eventType, p)
	return nil
}

// commerceAmount renders the display amount: paise when the event carries
// them, else the legacy rupee value.
func commerceAmount(p commerceEventPayload) string {
	if p.AmountMinor > 0 {
		return fmt.Sprintf("%d.%02d", p.AmountMinor/100, p.AmountMinor%100)
	}
	return fmt.Sprintf("%.2f", p.Amount)
}

// sendCommerceEmail sends the transactional e-mail an event carries an
// address for. Nil-safe: no service, no mail.
func (c *Consumer) sendCommerceEmail(ctx context.Context, eventType string, p commerceEventPayload) {
	if c.service == nil {
		return
	}
	money := commerceAmount(p)
	send := func(to, tpl string, data any, what string) {
		if to == "" {
			return
		}
		if err := c.service.SendEmail(ctx, to, tpl, data); err != nil {
			slog.Warn("commerce email failed", "email", what, "error", err)
		}
	}
	switch eventType {
	case commerceOrderPaid:
		send(p.BuyerEmail, mailer.PaymentReceiptTemplate, mailer.PaymentReceiptData{
			OrderNumber: p.OrderNumber, Amount: money, TransactionID: p.PaymentID,
		}, "order paid")
	case commerceOrderShipped:
		send(p.BuyerEmail, mailer.ShipmentShippedTemplate, mailer.ShipmentShippedData{
			OrderNumber: p.OrderNumber, Courier: p.Courier,
			TrackingNumber: p.TrackingNumber, TrackURL: p.TrackingURL,
		}, "shipped")
	case commerceOrderDelivered:
		send(p.BuyerEmail, mailer.ShipmentDeliveredTemplate, mailer.ShipmentDeliveredData{
			OrderNumber: p.OrderNumber,
		}, "delivered")
	case commerceInvoiceIssued:
		send(p.BuyerEmail, mailer.InvoiceEmailTemplate, mailer.InvoiceEmailData{
			OrderNumber: p.OrderNumber, InvoiceNumber: p.InvoiceNumber, InvoiceURL: p.InvoiceURL,
		}, "invoice")
	case commerceSellerNewOrder:
		// Emitted only by the fenced legacy checkout. The seller's in-app
		// notice now comes from commerce.order.paid (seller_user_id); this
		// event's seller_id is a sellers.id, never a user to notify.
		send(p.SellerEmail, mailer.SellerNewOrderTemplate, mailer.SellerNewOrderData{
			OrderNumber: p.OrderNumber, Amount: money,
		}, "seller new order")
	}
}
