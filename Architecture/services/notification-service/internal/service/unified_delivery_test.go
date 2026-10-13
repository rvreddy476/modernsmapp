package service

import (
	"context"
	"testing"

	"github.com/atpost/notification-service/internal/store/postgres"
)

func TestUnifiedPartnerWorkerDelivery(t *testing.T) {
	t.Run("home_services", func(t *testing.T) {
		p := dpProPush()
		f := newFakeDoorstepTransports(postgres.UserDevice{UserID: p.RecipientID, Platform: "android", PushToken: "fake", App: postgres.AppPartner})
		out, err := runDoorstepPush(context.Background(), f, p)
		if err != nil || out != DoorstepPushSent || len(f.sends) != 1 {
			t.Fatalf("out=%s sends=%d err=%v", out, len(f.sends), err)
		}
	})
	t.Run("food_delivery", func(t *testing.T) {
		p := riderPush()
		f := newFakeFoodTransports(postgres.UserDevice{UserID: p.RecipientID, Platform: "android", PushToken: "fake", App: postgres.AppPartner})
		out, err := runFoodPush(context.Background(), f, p)
		if err != nil || out != FoodPushSent || len(f.sends) != 1 {
			t.Fatalf("out=%s sends=%d err=%v", out, len(f.sends), err)
		}
	})
	t.Run("moped", func(t *testing.T) {
		p := captainOfferPush()
		f := newFakeRideTransports(postgres.UserDevice{UserID: p.RecipientID, Platform: "android", PushToken: "fake", App: postgres.AppPartner})
		out, err := runRidePush(context.Background(), f, p)
		if err != nil || out != RidePushSent || len(f.sends) != 1 {
			t.Fatalf("out=%s sends=%d err=%v", out, len(f.sends), err)
		}
	})
	for _, p := range []FoodPush{kitchenPush(), customerPush()} {
		t.Run("exclude_"+p.Type, func(t *testing.T) {
			f := newFakeFoodTransports(postgres.UserDevice{UserID: p.RecipientID, App: postgres.AppPartner, PushToken: "fake"})
			_, err := runFoodPush(context.Background(), f, p)
			if err != nil || len(f.sends) != 0 {
				t.Fatalf("cross-app notification: %v", err)
			}
		})
	}
}

func TestUnifiedFoodDeliveryReviewHasInboxAndNoInventedOrder(t *testing.T) {
	for _, typ := range []string{FoodTypeDeliveryAccount, FoodTypeDeliveryDocument} {
		p := FoodPush{DedupKey: "review-1", RecipientID: foodTestUser, App: AppFeastRider,
			Type: typ, EntityID: foodTestOffer, Title: "Review updated", Body: "Open your application", DeepLink: "/rider/account"}
		f := newFakeFoodTransports(foodDevice("partner-token", postgres.AppPartner), foodDevice("customer-token", AppMomentum))
		out, err := runFoodPush(context.Background(), f, p)
		if err != nil || out != FoodPushSent || len(f.inbox) != 1 || len(f.sends) != 1 || f.sends[0].token != "partner-token" {
			t.Fatalf("review routing: out=%s inbox=%d sends=%v err=%v", out, len(f.inbox), f.sends, err)
		}
		if _, exists := f.sends[0].data["order_id"]; exists {
			t.Fatal("account review invented an order")
		}
		if f.sends[0].data["entity_id"] != foodTestOffer.String() {
			t.Fatal("missing review entity")
		}
		out, err = runFoodPush(context.Background(), f, p)
		if err != nil || out != FoodPushDuplicate || len(f.inbox) != 1 || len(f.sends) != 1 {
			t.Fatal("duplicate review not suppressed")
		}
	}
}
