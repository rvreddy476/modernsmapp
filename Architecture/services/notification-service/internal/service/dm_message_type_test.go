package service

import (
	"testing"

	"github.com/google/uuid"
)

// A shared post ("Send to friends", chat message type "post") pushes "Sent you
// a post"; text and media keep the generic "You have a new message". Text DMs
// name no sender, so the post copy does not either. Title, data payload and
// the per-conversation collapse key are identical across types.
func TestDMPushCopyByMessageType(t *testing.T) {
	user, conversation := uuid.New(), uuid.New()
	genericTitle, genericBody := notifTitleBody("dm")
	_, _, textData := buildPushData(user, "dm", "conversation", conversation, "/messenger?user=x", DMRender(ChatMessageTypeText))

	cases := []struct {
		msgType  string
		wantBody string
	}{
		{ChatMessageTypeText, "You have a new message"},
		{ChatMessageTypeMedia, "You have a new message"},
		{ChatMessageTypePost, "Sent you a post"},
		{ChatMessageTypeLocation, "Shared a location"},
		{"", "You have a new message"},        // older producer omitted the type
		{"sticker", "You have a new message"}, // unknown future type stays generic
	}
	for _, tc := range cases {
		title, body, data := buildPushData(user, "dm", "conversation", conversation, "/messenger?user=x", DMRender(tc.msgType))
		if title != genericTitle || title != "New Message" {
			t.Errorf("%q: title %q, want %q", tc.msgType, title, genericTitle)
		}
		if body != tc.wantBody {
			t.Errorf("%q: body %q, want %q", tc.msgType, body, tc.wantBody)
		}
		if data["body"] != body || data["title"] != title {
			t.Errorf("%q: data payload copy %q/%q differs from the tray copy", tc.msgType, data["title"], data["body"])
		}
		if data["collapse_key"] != textData["collapse_key"] || data["collapse_key"] == "" {
			t.Errorf("%q: collapse key %q, want the conversation key %q", tc.msgType, data["collapse_key"], textData["collapse_key"])
		}
		if data["type"] != "dm" || data["entity_type"] != "conversation" || data["entity_id"] != conversation.String() {
			t.Errorf("%q: entity changed: %+v", tc.msgType, data)
		}
	}
	if genericBody != "You have a new message" {
		t.Fatalf("generic dm copy changed: %q", genericBody)
	}
}
