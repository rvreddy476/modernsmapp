package push

import "testing"

// Mopedu pushes (2026-09-18). The captain's offer is HIGH priority (a Dozing
// phone must wake for a 20-second offer) and carries an FCM ttl so a stale
// offer is dropped rather than delivered minutes later; the customer's ride
// updates name the ride_updates channel and, except "arrived", ride at
// normal priority.
func TestRidePushesNameTheirChannelPriorityAndTTL(t *testing.T) {
	cases := []struct {
		typ, channel, ttl string
		high              bool
	}{
		{"captain.offer", "captain_offer", "20s", true},
		{"captain.payment.received", "captain_on_duty", "", false},
		{"ride.assigned", "ride_updates", "", false},
		{"ride.arrived", "ride_updates", "", true},
		{"ride.payment.paid", "ride_updates", "", false},
	}
	for _, tc := range cases {
		data := map[string]string{
			"type":                tc.typ,
			"entity_id":           "e1",
			"deep_link":           "/mopedu/x",
			AndroidChannelDataKey: tc.channel,
		}
		if tc.ttl != "" {
			data[AndroidTTLDataKey] = tc.ttl
		}
		msg := BuildFCMMessage("tok", "Title", "Body", data)
		if _, has := msg["notification"]; !has {
			t.Fatalf("%s: lost its notification block", tc.typ)
		}
		got := msg["data"].(map[string]string)
		if _, leaked := got[AndroidChannelDataKey]; leaked {
			t.Fatalf("%s: channel transport key left in data: %v", tc.typ, got)
		}
		if _, leaked := got[AndroidTTLDataKey]; leaked {
			t.Fatalf("%s: ttl transport key left in data: %v", tc.typ, got)
		}
		if got["type"] != tc.typ || got["entity_id"] != "e1" || got["deep_link"] != "/mopedu/x" {
			t.Fatalf("%s: routing keys lost: %v", tc.typ, got)
		}
		android, ok := msg["android"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: no android config", tc.typ)
		}
		if notification, _ := android["notification"].(map[string]string); notification["channel_id"] != tc.channel {
			t.Fatalf("%s: channel = %v", tc.typ, android)
		}
		if (android["priority"] == "high") != tc.high {
			t.Fatalf("%s: priority = %v, want high=%v", tc.typ, android["priority"], tc.high)
		}
		if ttl, _ := android["ttl"].(string); ttl != tc.ttl {
			t.Fatalf("%s: ttl = %q, want %q", tc.typ, ttl, tc.ttl)
		}
	}
	if in := (map[string]string{"type": "captain.offer", AndroidTTLDataKey: "20s"}); func() bool {
		BuildFCMMessage("tok", "T", "B", in)
		return in[AndroidTTLDataKey] != "20s"
	}() {
		t.Fatal("caller data mutated by the ttl strip")
	}
}
