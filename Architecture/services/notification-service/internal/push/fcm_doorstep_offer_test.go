package push

import "testing"

func TestAllPartnerOffersAreExpiringDataMessages(t *testing.T) {
	for _, typ := range []string{"doorstep.pro.offer.new", "doorstep.pro.offer.expired", "captain.offer", "food_delivery_offer", "commerce.delivery.offer"} {
		msg := BuildFCMMessage("device", "Request", "Open app", map[string]string{
			"type": typ, "entity_id": "offer", "expires_at": "2026-10-10T10:00:20Z",
			AndroidChannelDataKey: "doorstep_pro_offers", AndroidTTLDataKey: "20s",
		})
		if _, ok := msg["notification"]; ok {
			t.Fatal("system must not render stale offers")
		}
		a := msg["android"].(map[string]interface{})
		if _, ok := a["notification"]; ok {
			t.Fatal("offer must reach expiry-aware presenter")
		}
		if (a["priority"] == "high") != (typ != "doorstep.pro.offer.expired") || a["ttl"] != "20s" {
			t.Fatalf("wrong delivery config: %v", a)
		}
		d := msg["data"].(map[string]string)
		if d["title"] != "Request" || d["entity_id"] != "offer" || d["expires_at"] == "" {
			t.Fatal("lost presentation or expiry data")
		}
	}
}
