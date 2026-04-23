package events

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/mailer"
	"github.com/google/uuid"
)

// commerceEventPayload is the shape commerce-service publishes for order lifecycle events.
// Fields are optional — consumer uses what is present.
type commerceEventPayload struct {
	OrderID        string  `json:"order_id"`
	OrderNumber    string  `json:"order_number"`
	UserID         string  `json:"user_id"`
	SellerID       string  `json:"seller_id"`
	Amount         float64 `json:"amount"`
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

// handleCommerceEvent routes commerce event types to email + in-app notification.
// Safe to call with a nil mailer — SendEmail no-ops.
func (c *Consumer) handleCommerceEvent(ctx context.Context, eventType string, raw []byte) error {
	var p commerceEventPayload
	if err := unmarshalPayload(raw, &p); err != nil {
		return err
	}
	money := fmt.Sprintf("%.2f", p.Amount)
	orderUUID, _ := uuid.Parse(p.OrderID)
	buyerUUID, _ := uuid.Parse(p.UserID)
	sellerUUID, _ := uuid.Parse(p.SellerID)
	now := time.Now()

	// Create in-app notification for the buyer for every buyer-facing event.
	notifyBuyer := func(notifType, deepLink string) {
		if buyerUUID == uuid.Nil || orderUUID == uuid.Nil {
			return
		}
		actor := sellerUUID // seller acts, buyer receives
		if err := c.service.CreateNotification(ctx, buyerUUID, actor, notifType, "order", orderUUID, deepLink, now); err != nil {
			slog.Warn("commerce in-app notify failed", "type", notifType, "error", err)
		}
	}

	deepLinkOrder := fmt.Sprintf("/orders/%s", p.OrderID)
	deepLinkSeller := fmt.Sprintf("/seller/orders/%s", p.OrderID)

	switch eventType {
	case "commerce.order.created":
		notifyBuyer("commerce_order_created", deepLinkOrder)
		if p.BuyerEmail != "" {
			data := mailer.OrderConfirmationData{
				OrderNumber:   p.OrderNumber,
				PaymentMethod: p.PaymentMethod,
				Total:         money,
			}
			if err := c.service.SendEmail(ctx, p.BuyerEmail, mailer.OrderConfirmationTemplate, data); err != nil {
				slog.Warn("commerce email: order created", "error", err)
			}
		}

	case "commerce.order.paid":
		notifyBuyer("commerce_order_paid", deepLinkOrder)
		if p.BuyerEmail != "" {
			data := mailer.PaymentReceiptData{
				OrderNumber:   p.OrderNumber,
				Amount:        money,
				TransactionID: p.PaymentID,
			}
			if err := c.service.SendEmail(ctx, p.BuyerEmail, mailer.PaymentReceiptTemplate, data); err != nil {
				slog.Warn("commerce email: order paid", "error", err)
			}
		}

	case "commerce.order.shipped":
		notifyBuyer("commerce_order_shipped", deepLinkOrder)
		if p.BuyerEmail != "" {
			data := mailer.ShipmentShippedData{
				OrderNumber:    p.OrderNumber,
				Courier:        p.Courier,
				TrackingNumber: p.TrackingNumber,
				TrackURL:       p.TrackingURL,
			}
			if err := c.service.SendEmail(ctx, p.BuyerEmail, mailer.ShipmentShippedTemplate, data); err != nil {
				slog.Warn("commerce email: shipped", "error", err)
			}
		}

	case "commerce.order.delivered":
		notifyBuyer("commerce_order_delivered", deepLinkOrder)
		if p.BuyerEmail != "" {
			data := mailer.ShipmentDeliveredData{OrderNumber: p.OrderNumber}
			if err := c.service.SendEmail(ctx, p.BuyerEmail, mailer.ShipmentDeliveredTemplate, data); err != nil {
				slog.Warn("commerce email: delivered", "error", err)
			}
		}

	case "commerce.invoice.issued":
		notifyBuyer("commerce_invoice_issued", deepLinkOrder)
		if p.BuyerEmail != "" {
			data := mailer.InvoiceEmailData{
				OrderNumber:   p.OrderNumber,
				InvoiceNumber: p.InvoiceNumber,
				InvoiceURL:    p.InvoiceURL,
			}
			if err := c.service.SendEmail(ctx, p.BuyerEmail, mailer.InvoiceEmailTemplate, data); err != nil {
				slog.Warn("commerce email: invoice", "error", err)
			}
		}

	case "commerce.seller.new_order":
		// Seller-side in-app notification.
		if sellerUUID != uuid.Nil && orderUUID != uuid.Nil {
			if err := c.service.CreateNotification(ctx, sellerUUID, buyerUUID, "commerce_seller_new_order", "order", orderUUID, deepLinkSeller, now); err != nil {
				slog.Warn("commerce seller in-app notify failed", "error", err)
			}
		}
		if p.SellerEmail != "" {
			data := mailer.SellerNewOrderData{OrderNumber: p.OrderNumber, Amount: money}
			if err := c.service.SendEmail(ctx, p.SellerEmail, mailer.SellerNewOrderTemplate, data); err != nil {
				slog.Warn("commerce email: seller new order", "error", err)
			}
		}
	}
	return nil
}
