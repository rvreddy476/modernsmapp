package push

import "testing"

// Audit 2026-10-30 H1/M2: dating pushes keep the "dating" channel in data
// (the foreground app draws on the same private channel) and lift it into
// android.notification.channel_id; the trusted-contact safety pushes wake a
// dozing phone.

func TestDatingPushKeepsChannelInDataAndLiftsIt(t *testing.T) {
	for _, typ := range []string{
		"dating.spark.created",
		"dating.match.new_message",
		"dating.safety.panic.trusted_contact",
		"dating.safety.location_shared",
		"dating_scam_alert",
		"dating_date_checkin",
	} {
		in := map[string]string{"type": typ, "title": "t", "body": "b", AndroidChannelDataKey: DatingAndroidChannel}
		msg := BuildFCMMessage("tok", "t", "b", in)
		if _, ok := msg["notification"]; !ok {
			t.Fatalf("%s: lost the notification block", typ)
		}
		data := msg["data"].(map[string]string)
		if data[AndroidChannelDataKey] != "dating" {
			t.Errorf("%s: data channel %q, want dating", typ, data[AndroidChannelDataKey])
		}
		android, _ := msg["android"].(map[string]interface{})
		notification, _ := android["notification"].(map[string]string)
		if notification["channel_id"] != "dating" {
			t.Errorf("%s: android.notification.channel_id %q, want dating", typ, notification["channel_id"])
		}
	}
}

func TestTrustedContactSafetyPushesAreHighPriority(t *testing.T) {
	for _, typ := range []string{"dating.safety.panic.trusted_contact", "dating.safety.location_shared", "dating.safety.panic.responder"} {
		msg := BuildFCMMessage("tok", "t", "b", map[string]string{"type": typ, AndroidChannelDataKey: DatingAndroidChannel})
		android, _ := msg["android"].(map[string]interface{})
		if android["priority"] != "high" {
			t.Errorf("%s: priority %v, want high", typ, android["priority"])
		}
		if _, ok := msg["notification"]; !ok {
			t.Errorf("%s: a high-priority safety push must stay a notification message", typ)
		}
	}
	// Ordinary dating engagement stays normal priority.
	msg := BuildFCMMessage("tok", "t", "b", map[string]string{"type": "dating.spark.created"})
	if android, _ := msg["android"].(map[string]interface{}); android["priority"] == "high" {
		t.Errorf("dating.spark.created: high priority, want normal")
	}
}

func TestIsDatingType(t *testing.T) {
	for typ, want := range map[string]bool{
		"dating.spark.created":                true,
		"dating.safety.panic.trusted_contact": true,
		"dating_scam_alert":                   true,
		"dating_date_checkin":                 true,
		"dm":                                  false,
		"datingish":                           false,
		"dating":                              false,
		"like":                                false,
	} {
		if got := IsDatingType(typ); got != want {
			t.Errorf("IsDatingType(%q) = %v, want %v", typ, got, want)
		}
	}
}
