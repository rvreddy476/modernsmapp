package events

import (
	"encoding/json"
	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestFoodOperationalAudienceAndRefundCopy(t *testing.T) {
	owner, rider, customer := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		event, app, typ, recipient, title string
		id                                uuid.UUID
	}{
		{"food.restaurant.status_changed", service.AppFeastKitchen, service.FoodTypeKitchenAccount, "restaurant_owner_user_id", "Restaurant approved", owner},
		{"food.restaurant.document_reviewed", service.AppFeastKitchen, service.FoodTypeKitchenAccount, "restaurant_owner_user_id", "Restaurant document approved", owner},
		{"food.restaurant.settlement_paid", service.AppFeastKitchen, service.FoodTypeKitchenAccount, "restaurant_owner_user_id", "Restaurant payout recorded", owner},
		{"food.delivery_partner.settlement_paid", service.AppFeastRider, service.FoodTypeDeliveryAccount, "partner_user_id", "Delivery payout recorded", rider},
		{"food.refund.approved", service.AppMomentum, service.FoodTypeOrderStatus, "user_id", "Refund request approved", customer},
		{"food.refund.rejected", service.AppMomentum, service.FoodTypeOrderStatus, "user_id", "Refund request reviewed", customer},
	}
	for _, tt := range cases {
		t.Run(tt.event, func(t *testing.T) {
			data := map[string]any{"restaurant_id": uuid.New(), "restaurant_owner_user_id": owner, "partner_id": uuid.New(), "partner_user_id": rider, "user_id": customer, "document_id": uuid.New(), "settlement_id": uuid.New(), "order_id": uuid.New(), "status": "APPROVED"}
			if tt.event == "food.restaurant.settlement_paid" || tt.event == "food.delivery_partner.settlement_paid" {
				data["status"] = "PAID"
			}
			raw, _ := json.Marshal(data)
			plan := planFoodPushes(tt.event, raw, "stable-event", time.Now())
			if plan.outcome != foodOutcomePlanned || len(plan.pushes) != 1 {
				t.Fatalf("plan: %+v", plan)
			}
			p := plan.pushes[0]
			if p.App != tt.app || p.Type != tt.typ || p.RecipientID != tt.id || p.Title != tt.title {
				t.Fatalf("wrong audience/copy: %+v", p)
			}
			delete(data, tt.recipient)
			raw, _ = json.Marshal(data)
			if plan := planFoodPushes(tt.event, raw, "stable-event", time.Now()); plan.outcome != foodOutcomeMissingRecipient || len(plan.pushes) != 0 {
				t.Fatalf("inferred recipient: %+v", plan)
			}
		})
	}
}

func TestKitchenAcceptanceDeadlinePreserved(t *testing.T) {
	deadline := time.Now().Add(-time.Minute).UTC()
	raw, _ := json.Marshal(map[string]any{"order_id": uuid.New(), "user_id": uuid.New(), "restaurant_owner_user_id": uuid.New(), "status": "CONFIRMED", "accept_deadline_at": deadline})
	plan := planFoodPushes("food.order.confirmed", raw, "event", time.Now())
	found := false
	for _, p := range plan.pushes {
		if p.Type == service.FoodTypeOrderNew {
			found = true
			if p.AcceptDeadlineAt == nil || !p.AcceptDeadlineAt.Equal(deadline) {
				t.Fatal("deadline reset")
			}
		}
	}
	if !found {
		t.Fatal("missing kitchen notice")
	}
}

func TestFoodCompletionNotifiesAllAuthoritativeAudiences(t *testing.T) {
	customer, owner, rider := uuid.New(), uuid.New(), uuid.New()
	raw, _ := json.Marshal(map[string]any{"order_id": uuid.New(), "user_id": customer, "restaurant_owner_user_id": owner, "delivery_partner_user_id": rider, "status": "DELIVERED"})
	plan := planFoodPushes("food.delivery.delivered", raw, "completion", time.Now())
	if len(plan.pushes) != 3 {
		t.Fatalf("audiences: %+v", plan)
	}
	want := map[string]uuid.UUID{service.AppMomentum: customer, service.AppFeastKitchen: owner, service.AppFeastRider: rider}
	for _, p := range plan.pushes {
		if p.RecipientID != want[p.App] || p.AndroidChannel != service.FoodChannelOrders {
			t.Fatalf("wrong operational audience %+v", p)
		}
	}
}
