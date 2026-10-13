package events

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
)

// Only explicit authoritative recipient fields are accepted. Never infer an
// owner from an actor or route a merchant notice to the delivery partner app.
func planFoodOperationalNotice(typ string, payload json.RawMessage, base string, now time.Time) foodPushPlan {
	var e struct {
		RestaurantID  uuid.UUID `json:"restaurant_id"`
		OwnerID       uuid.UUID `json:"restaurant_owner_user_id"`
		PartnerID     uuid.UUID `json:"partner_id"`
		PartnerUserID uuid.UUID `json:"partner_user_id"`
		OrderID       uuid.UUID `json:"order_id"`
		UserID        uuid.UUID `json:"user_id"`
		DocumentID    uuid.UUID `json:"document_id"`
		SettlementID  uuid.UUID `json:"settlement_id"`
		Status        string    `json:"status"`
		OccurredAt    time.Time `json:"occurred_at"`
	}
	if json.Unmarshal(payload, &e) != nil {
		return foodPushPlan{outcome: foodOutcomeMalformed}
	}
	p := service.FoodPush{DedupKey: base + ":" + typ, CreatedAt: e.OccurredAt, AndroidChannel: service.FoodChannelOrders}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	recipientField := "restaurant_owner_user_id"
	switch typ {
	case "food.refund.approved", "food.refund.rejected":
		p.RecipientID, p.App, p.Type, p.OrderID = e.UserID, service.AppMomentum, service.FoodTypeOrderStatus, e.OrderID
		recipientField = "user_id"
		if e.OrderID == uuid.Nil {
			return foodPushPlan{outcome: foodOutcomeMalformed}
		}
		p.DeepLink = "/feast/orders/" + e.OrderID.String()
		p.Title, p.Body = "Refund request approved", "Your refund request was approved. Open your order to check processing progress."
		if typ == "food.refund.rejected" {
			p.Title, p.Body = "Refund request reviewed", "Open your order to review the refund decision and contact support."
		}
	case "food.delivery_partner.settlement_paid", "food.delivery_partner.settlement_generated":
		if e.PartnerID == uuid.Nil || e.SettlementID == uuid.Nil || (typ == "food.delivery_partner.settlement_paid" && e.Status != "PAID") {
			return foodPushPlan{outcome: foodOutcomeMalformed}
		}
		p.RecipientID, p.App, p.Type, p.EntityID = e.PartnerUserID, service.AppFeastRider, service.FoodTypeDeliveryAccount, e.SettlementID
		recipientField = "partner_user_id"
		p.DeepLink = "/rider/earnings"
		p.Title, p.Body = "Delivery payout recorded", "A payout has been recorded. Open Earnings to review your settlement and transfer reference."
		if typ == "food.delivery_partner.settlement_generated" {
			p.Title, p.Body = "Delivery settlement ready", "Open Earnings to review your settlement. This is not a confirmation of bank payment."
		}
	default:
		if e.RestaurantID == uuid.Nil {
			return foodPushPlan{outcome: foodOutcomeMalformed}
		}
		p.RecipientID, p.App, p.Type, p.EntityID = e.OwnerID, service.AppFeastKitchen, service.FoodTypeKitchenAccount, e.RestaurantID
		p.DeepLink = "/kitchen/application?restaurant_id=" + e.RestaurantID.String()
		if typ == "food.restaurant.settlement_paid" || typ == "food.restaurant.settlement_generated" {
			if e.SettlementID == uuid.Nil || (typ == "food.restaurant.settlement_paid" && e.Status != "PAID") {
				return foodPushPlan{outcome: foodOutcomeMalformed}
			}
			p.EntityID = e.SettlementID
			p.Title, p.Body = "Restaurant payout recorded", "A payout has been recorded. Open your restaurant settlement to review the transfer reference."
			p.DeepLink = "/kitchen/earnings?restaurant_id=" + e.RestaurantID.String()
			if typ == "food.restaurant.settlement_generated" {
				p.Title, p.Body = "Restaurant settlement ready", "Open your settlement to review the statement. This is not a confirmation of bank payment."
			}
		} else if typ == "food.restaurant.document_reviewed" {
			if e.DocumentID == uuid.Nil {
				return foodPushPlan{outcome: foodOutcomeMalformed}
			}
			p.EntityID = e.DocumentID
			switch strings.ToUpper(e.Status) {
			case "APPROVED":
				p.Title, p.Body = "Restaurant document approved", "Open your restaurant application to see the latest verification status."
			case "REJECTED":
				p.Title, p.Body = "Restaurant document needs attention", "Review the document decision and upload the requested correction."
			default:
				return foodPushPlan{outcome: foodOutcomeIgnored}
			}
		} else {
			text, ok := map[string]foodCopy{
				"ACTIVE":        {"Restaurant approved", "Open your restaurant workspace to review your availability and orders."},
				"APPROVED":      {"Restaurant approved", "Open your restaurant workspace to review your availability and orders."},
				"REJECTED":      {"Restaurant application needs attention", "Review your restaurant application for the decision and next steps."},
				"SUSPENDED":     {"Restaurant paused", "New orders are paused. Open your restaurant workspace for details."},
				"CLOSED":        {"Restaurant closed", "Open your restaurant workspace to review its status."},
				"FSSAI_EXPIRED": {"Restaurant document expired", "New orders are paused. Upload a current food-safety document for review."},
			}[strings.ToUpper(e.Status)]
			if !ok {
				return foodPushPlan{outcome: foodOutcomeIgnored}
			}
			p.Title, p.Body = text.title, text.body
		}
	}
	if p.RecipientID == uuid.Nil {
		return foodPushPlan{outcome: foodOutcomeMissingRecipient, missing: []string{recipientField}}
	}
	return foodPushPlan{outcome: foodOutcomePlanned, pushes: []service.FoodPush{p}}
}
