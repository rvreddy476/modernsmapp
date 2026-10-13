package events

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func planFoodDeliveryReview(typ string, payload json.RawMessage, base string, now time.Time) foodPushPlan {
	var e struct {
		PartnerID  uuid.UUID `json:"partner_id"`
		UserID     uuid.UUID `json:"partner_user_id"`
		DocumentID uuid.UUID `json:"document_id"`
		Status     string    `json:"status"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	if json.Unmarshal(payload, &e) != nil || e.PartnerID == uuid.Nil {
		return foodPushPlan{outcome: foodOutcomeMalformed}
	}
	if e.UserID == uuid.Nil {
		return foodPushPlan{outcome: foodOutcomeMissingRecipient, missing: []string{"partner_user_id"}}
	}
	status := strings.ToUpper(e.Status)
	if typ == events.EventFoodDeliveryPartnerApproved {
		status = "APPROVED"
	}
	copy := map[string]foodCopy{
		"APPROVED":       {"Delivery application approved", "Open your delivery workspace to review your account and availability."},
		"ACTIVE":         {"Delivery account active", "Open your delivery workspace for the latest account status."},
		"PENDING_REVIEW": {"Application under review", "Your delivery application is being reviewed."},
		"REJECTED":       {"Delivery application needs attention", "Open your application to review the decision and required corrections."},
		"SUSPENDED":      {"Delivery account paused", "Open your delivery account for details. New assignments are paused."},
		"CLOSED":         {"Delivery account closed", "Open your account to review its status."},
		"DRAFT":          {"Application needs updating", "Open your application to complete the required details."},
	}
	text, ok := copy[status]
	pushType, entityID := service.FoodTypeDeliveryAccount, e.PartnerID
	if typ == "food.delivery_partner.document_reviewed" {
		if e.DocumentID == uuid.Nil {
			return foodPushPlan{outcome: foodOutcomeMalformed}
		}
		pushType, entityID = service.FoodTypeDeliveryDocument, e.DocumentID
		ok = status == "APPROVED" || status == "REJECTED"
		text = foodCopy{"Document approved", "A delivery document was approved. Open your application for the latest status."}
		if status == "REJECTED" {
			text = foodCopy{"Document needs attention", "Open your delivery documents to review the decision and upload a correction."}
		}
	}
	if !ok {
		return foodPushPlan{outcome: foodOutcomeIgnored}
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = now
	}
	return foodPushPlan{outcome: foodOutcomePlanned, pushes: []service.FoodPush{{
		DedupKey: base + ":" + typ, RecipientID: e.UserID, App: service.AppFeastRider, Type: pushType,
		EntityID: entityID, AndroidChannel: service.FoodChannelOrders, Title: text.title, Body: text.body,
		DeepLink: "/rider/account", CreatedAt: e.OccurredAt,
	}}}
}
