package push

import (
	"testing"
)

// The Android calling push contract (CALL-LB-4). A `notification` block is
// system-rendered in background and NEVER reaches FirebaseMessagingService —
// the app cannot show its full-screen incoming-call UI from one. Ringing
// pushes must therefore be DATA-ONLY and HIGH priority, with title/body
// carried in data for the app's own presenter. Every other type keeps the
// pre-existing shape.

func TestCallPushesAreDataOnlyAndHighPriority(t *testing.T) {
	for _, callType := range []string{"incoming_call", "incoming_video_call"} {
		msg := BuildFCMMessage("tok", "Incoming call", "Tap to answer", map[string]string{
			"type":      callType,
			"entity_id": "abc",
			"deep_link": "/call/abc",
		})

		// THE load-bearing assertion: the pre-fix builder always attached a
		// notification block, which silently broke the background ring.
		if _, has := msg["notification"]; has {
			t.Fatalf("%s: call push carries a notification block — background delivery would be system-rendered and never reach the app", callType)
		}
		android, ok := msg["android"].(map[string]interface{})
		if !ok || android["priority"] != "high" {
			t.Fatalf("%s: call push is not high priority: %v", callType, msg["android"])
		}
		data, ok := msg["data"].(map[string]string)
		if !ok {
			t.Fatalf("%s: missing data payload", callType)
		}
		if data["title"] != "Incoming call" || data["body"] != "Tap to answer" {
			t.Fatalf("%s: title/body must ride in data for the app presenter: %v", callType, data)
		}
		if data["type"] != callType || data["entity_id"] != "abc" {
			t.Fatalf("%s: routing keys lost: %v", callType, data)
		}
	}
}

// CALL-LB-4: push delivery to the device is at-least-once, so a redelivered
// ringing push must REPLACE the previous one via a stable call-ID collapse
// key, not stack a second incoming-call notification.
func TestCallPushesCarryTheCallCollapseKey(t *testing.T) {
	msg := BuildFCMMessage("tok", "Incoming call", "Tap to answer", map[string]string{
		"type":         "incoming_call",
		"entity_id":    "abc",
		"collapse_key": "call:abc",
	})
	android, ok := msg["android"].(map[string]interface{})
	if !ok || android["collapse_key"] != "call:abc" {
		t.Fatalf("call push lost its collapse key: %v", msg["android"])
	}
	if android["priority"] != "high" {
		t.Fatalf("collapse key must not displace high priority: %v", android)
	}
	if _, has := msg["notification"]; has {
		t.Fatalf("collapse key variant regressed to a notification block")
	}
}

func TestNonCallPushesKeepTheNotificationBlock(t *testing.T) {
	msg := BuildFCMMessage("tok", "New Message", "You have a new message", map[string]string{
		"type":         "dm",
		"entity_id":    "conv",
		"collapse_key": "dm:conv",
	})

	notification, ok := msg["notification"].(map[string]string)
	if !ok || notification["title"] != "New Message" {
		t.Fatalf("non-call push lost its notification block: %v", msg)
	}
	android, ok := msg["android"].(map[string]interface{})
	if !ok || android["collapse_key"] != "dm:conv" {
		t.Fatalf("collapse key lost: %v", msg["android"])
	}
	if _, has := android["priority"]; has {
		t.Fatalf("non-call push must not force high priority: %v", android)
	}
}
