package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/notification-service/internal/push"
)

// Chat-parity fix pass 2: a chat push names the Android channel of its
// conversation's alert tone, in data AND in android.notification.channel_id,
// so a background push the system draws plays the tone's sound. The ids are
// the ones the Android app registers (NotificationChannelSpec MESSAGES*).

// chatFCM renders one chat message push the way delivery does and shapes the
// FCM message from it.
func chatFCM(t *testing.T, notifType string, render RenderOverride) (data map[string]string, fcmData map[string]string, channelID string) {
	t.Helper()
	conv := uuid.New()
	title, body, data := buildPushData(uuid.New(), notifType, "conversation", conv, "/messenger?user=x", render)
	msg := push.BuildFCMMessage("tok", title, body, data)
	if _, ok := msg["notification"]; !ok {
		t.Fatalf("%s: chat push lost its notification block: %+v", notifType, msg)
	}
	fcmData, _ = msg["data"].(map[string]string)
	android, _ := msg["android"].(map[string]interface{})
	notification, _ := android["notification"].(map[string]string)
	return data, fcmData, notification["channel_id"]
}

func TestChatPushChannelPerAlertTone(t *testing.T) {
	cases := []struct {
		tone string
		want string
	}{
		{"default", "messages"},
		{"chime", "messages_chime"},
		{"pop", "messages_pop"},
		{"bell", "messages_bell"},
		{"silent", "messages_silent"},
		{" Chime ", "messages_chime"}, // normalised like alert_tone
		{"", "messages"},              // absent tone
		{"airhorn", "messages"},       // unknown tone
	}
	for _, tc := range cases {
		render := ChatMessageRender(ChatMessagePush{MessageType: "text", Text: "hi", AlertTone: tc.tone}, uuid.New(), true, "Asha")
		data, fcmData, channelID := chatFCM(t, "dm", render)
		if data[push.AndroidChannelDataKey] != tc.want {
			t.Errorf("tone %q: push data channel %q, want %q", tc.tone, data[push.AndroidChannelDataKey], tc.want)
		}
		if fcmData[push.AndroidChannelDataKey] != tc.want {
			t.Errorf("tone %q: FCM data channel %q, want %q", tc.tone, fcmData[push.AndroidChannelDataKey], tc.want)
		}
		if channelID != tc.want {
			t.Errorf("tone %q: android.notification.channel_id %q, want %q", tc.tone, channelID, tc.want)
		}
	}
}

func TestChatPushUnknownToneOmitsAlertToneButKeepsDefaultChannel(t *testing.T) {
	render := ChatMessageRender(ChatMessagePush{MessageType: "text", AlertTone: "airhorn"}, uuid.New(), false, "")
	data, _, _ := chatFCM(t, "dm", render)
	if _, ok := data["alert_tone"]; ok {
		t.Errorf("unknown tone forwarded as alert_tone: %+v", data)
	}
	if data[push.AndroidChannelDataKey] != ChatChannelMessages {
		t.Errorf("unknown tone channel %q, want messages", data[push.AndroidChannelDataKey])
	}
}

func TestChatPushChannelWhenPreviewOff(t *testing.T) {
	// Preview off returns before the copy is built; the tone's channel must
	// still ride along.
	render := ChatMessageRender(ChatMessagePush{MessageType: "media", MediaKind: "image", AlertTone: "bell"}, uuid.New(), false, "Asha")
	_, fcmData, channelID := chatFCM(t, "dm", render)
	if fcmData[push.AndroidChannelDataKey] != ChatChannelBell || channelID != ChatChannelBell {
		t.Errorf("preview off: data %q channel_id %q, want messages_bell", fcmData[push.AndroidChannelDataKey], channelID)
	}
}

func TestMessageRequestPushUsesDefaultChannel(t *testing.T) {
	// The request path is the generic CreateNotification: no render data,
	// no tone. It still lands on the default messages channel.
	data, fcmData, channelID := chatFCM(t, "message_request", RenderOverride{})
	if data[push.AndroidChannelDataKey] != ChatChannelMessages {
		t.Errorf("request push data channel %q, want messages", data[push.AndroidChannelDataKey])
	}
	if fcmData[push.AndroidChannelDataKey] != ChatChannelMessages || channelID != ChatChannelMessages {
		t.Errorf("request FCM: data %q channel_id %q, want messages", fcmData[push.AndroidChannelDataKey], channelID)
	}
	if _, ok := data["alert_tone"]; ok {
		t.Errorf("request push carries an alert_tone: %+v", data)
	}
}

func TestLegacyDMRenderUsesDefaultChannel(t *testing.T) {
	// DMRender carries no data map (a "dm" built without chatPushData).
	data, _, channelID := chatFCM(t, "dm", DMRender(ChatMessageTypePost))
	if data[push.AndroidChannelDataKey] != ChatChannelMessages || channelID != ChatChannelMessages {
		t.Errorf("legacy dm: data %q channel_id %q, want messages", data[push.AndroidChannelDataKey], channelID)
	}
}

func TestNonChatPushGetsNoChatChannel(t *testing.T) {
	_, _, data := buildPushData(uuid.New(), "like", "post", uuid.New(), "/p/x", RenderOverride{})
	if v, ok := data[push.AndroidChannelDataKey]; ok {
		t.Errorf("non-chat push named channel %q", v)
	}
}
