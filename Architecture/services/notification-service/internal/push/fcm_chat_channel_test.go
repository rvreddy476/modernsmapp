package push

import "testing"

// Chat-parity fix pass 2: chat pushes ("dm", "message_request") keep
// android_channel_id in data (clients read it) as well as lifting it into
// android.notification.channel_id; every other type keeps it transport-only.

func TestChatPushKeepsChannelInDataAndLiftsIt(t *testing.T) {
	for _, typ := range []string{"dm", "message_request"} {
		in := map[string]string{"type": typ, "conversation_id": "c1", AndroidChannelDataKey: "messages_pop"}
		msg := BuildFCMMessage("tok", "New Message", "Asha: hi", in)
		if _, ok := msg["notification"]; !ok {
			t.Fatalf("%s: lost the notification block", typ)
		}
		data := msg["data"].(map[string]string)
		if data[AndroidChannelDataKey] != "messages_pop" {
			t.Errorf("%s: data channel %q, want messages_pop", typ, data[AndroidChannelDataKey])
		}
		android, _ := msg["android"].(map[string]interface{})
		notification, _ := android["notification"].(map[string]string)
		if notification["channel_id"] != "messages_pop" {
			t.Errorf("%s: android.notification.channel_id %q, want messages_pop", typ, notification["channel_id"])
		}
		if in[AndroidChannelDataKey] != "messages_pop" {
			t.Errorf("%s: caller's map mutated", typ)
		}
	}
}

func TestNonChatPushStillStripsChannelFromData(t *testing.T) {
	msg := BuildFCMMessage("tok", "t", "b", map[string]string{"type": "food_order_status", AndroidChannelDataKey: "food_orders"})
	if _, leaked := msg["data"].(map[string]string)[AndroidChannelDataKey]; leaked {
		t.Errorf("non-chat type leaked the transport key into data")
	}
}
