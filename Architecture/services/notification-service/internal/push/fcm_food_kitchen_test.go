package push

import "testing"

// Food server lane (9 Nov 2026), food audit M4: food_order_new reaches the
// Feast Kitchen app as a DATA-ONLY message, so FirebaseMessagingService runs
// in the background and KitchenOrderPushHandler rings the looping alarm. The
// app reads type and accept_deadline_at (feature/kitchen NewOrderPush); the
// android ttl drops a delivery FCM could only make after the deadline.
func TestKitchenNewOrderIsDataOnlyWithDeadlineAndTTL(t *testing.T) {
	msg := BuildFCMMessage("tok", "New order", "Accept before the timer runs out.", map[string]string{
		"type":                "food_order_new",
		"order_id":            "o1",
		"accept_deadline_at":  "2026-11-09T10:03:00Z",
		"collapse_key":        "food_order_new:o1",
		AndroidChannelDataKey: "kitchen_new_order",
		AndroidTTLDataKey:     "180s",
	})
	if _, has := msg["notification"]; has {
		t.Fatalf("kitchen new order carries a notification block: %v", msg)
	}
	android, _ := msg["android"].(map[string]interface{})
	if _, has := android["notification"]; has {
		t.Fatalf("kitchen new order carries android.notification: %v", android)
	}
	if android["priority"] != "high" {
		t.Fatalf("priority = %v, want high", android["priority"])
	}
	if android["ttl"] != "180s" {
		t.Fatalf("android.ttl = %v, want 180s", android["ttl"])
	}
	if android["collapse_key"] != "food_order_new:o1" {
		t.Fatalf("collapse_key = %v", android["collapse_key"])
	}
	data, _ := msg["data"].(map[string]string)
	for k, want := range map[string]string{
		"type": "food_order_new", "order_id": "o1", "accept_deadline_at": "2026-11-09T10:03:00Z",
		"title": "New order", "body": "Accept before the timer runs out.",
	} {
		if data[k] != want {
			t.Fatalf("data[%s] = %q, want %q (%v)", k, data[k], want, data)
		}
	}
	for _, transport := range []string{AndroidChannelDataKey, AndroidTTLDataKey} {
		if _, leaked := data[transport]; leaked {
			t.Fatalf("transport key %s left in data: %v", transport, data)
		}
	}
}

// A kitchen web device still gets a visible notification: a browser has no
// app handler to ring from data alone. Android stays data-only.
func TestKitchenNewOrderKeepsANotificationOnTheWebOnly(t *testing.T) {
	data := map[string]string{"type": "food_order_new", "order_id": "o1"}
	if _, has := MessageForPlatform("tok", "web", "New order", "b", data)["notification"]; !has {
		t.Fatal("web kitchen new order has no notification block")
	}
	if _, has := MessageForPlatform("tok", "android", "New order", "b", data)["notification"]; has {
		t.Fatal("android kitchen new order gained a notification block")
	}
	// Other data-only pushes are untouched on the web, as before.
	if _, has := MessageForPlatform("tok", "web", "t", "b", map[string]string{"type": "food_delivery_offer"})["notification"]; has {
		t.Fatal("web rider offer gained a notification block")
	}
}

// The rider's offer was already data-only; it carries the android ttl the
// partner queue computes from the offer's expiry (item 2: unchanged, pinned).
func TestRiderOfferCarriesItsAndroidTTL(t *testing.T) {
	msg := BuildFCMMessage("tok", "New delivery job", "b", map[string]string{
		"type": "food_delivery_offer", "offer_id": "f1", AndroidTTLDataKey: "19s",
	})
	android, _ := msg["android"].(map[string]interface{})
	if android["ttl"] != "19s" {
		t.Fatalf("offer android.ttl = %v, want 19s", android["ttl"])
	}
	if _, has := msg["notification"]; has {
		t.Fatal("offer gained a notification block")
	}
}
