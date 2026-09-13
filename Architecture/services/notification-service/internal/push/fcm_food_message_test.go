package push

import "testing"

// Feast pushes name their Android channel through the transport-only
// android_channel_id data key (lane B5b). The builder must lift it into
// android.notification.channel_id and never leak it into data; operational
// pushes (kitchen new order, rider job offer) must be high priority.
func TestFoodPushesNameTheirAndroidChannel(t *testing.T) {
	cases := []struct {
		typ, channel string
		high         bool
	}{
		{"food_order_new", "kitchen_new_order", true},
		{"food_delivery_offer", "rider_job_offer", true},
		{"food_order_status", "food_orders", false},
	}
	for _, tc := range cases {
		msg := BuildFCMMessage("tok", "Title", "Body", map[string]string{
			"type":                tc.typ,
			"order_id":            "o1",
			AndroidChannelDataKey: tc.channel,
		})
		if _, has := msg["notification"]; !has {
			t.Fatalf("%s: lost its notification block", tc.typ)
		}
		data := msg["data"].(map[string]string)
		if _, leaked := data[AndroidChannelDataKey]; leaked {
			t.Fatalf("%s: transport key left in data: %v", tc.typ, data)
		}
		if data["type"] != tc.typ || data["order_id"] != "o1" {
			t.Fatalf("%s: routing keys lost: %v", tc.typ, data)
		}
		android, ok := msg["android"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: no android config", tc.typ)
		}
		notification, _ := android["notification"].(map[string]string)
		if notification["channel_id"] != tc.channel {
			t.Fatalf("%s: channel = %v", tc.typ, android)
		}
		if (android["priority"] == "high") != tc.high {
			t.Fatalf("%s: priority = %v, want high=%v", tc.typ, android["priority"], tc.high)
		}
	}
}

// The caller's data map is not mutated when the transport key is stripped.
func TestFoodChannelStripDoesNotMutateCallerData(t *testing.T) {
	in := map[string]string{"type": "food_order_status", AndroidChannelDataKey: "food_orders"}
	BuildFCMMessage("tok", "T", "B", in)
	if in[AndroidChannelDataKey] != "food_orders" {
		t.Fatal("caller data mutated")
	}
}

// A channel key must never turn a ringing call back into a system-rendered
// notification message (CALL-LB-4).
func TestCallPushIgnoresAndroidChannelKey(t *testing.T) {
	msg := BuildFCMMessage("tok", "Incoming call", "Tap to answer", map[string]string{
		"type":                "incoming_call",
		AndroidChannelDataKey: "calls",
	})
	android := msg["android"].(map[string]interface{})
	if _, has := android["notification"]; has {
		t.Fatalf("call push gained an android.notification block: %v", android)
	}
	if _, has := msg["notification"]; has {
		t.Fatal("call push gained a notification block")
	}
}
