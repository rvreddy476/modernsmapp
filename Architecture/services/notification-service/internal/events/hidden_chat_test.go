package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Hidden chats: a recipient who hid the conversation gets ONLY the
// content-free push — never the "dm" row (which carries the sender as actor,
// the conversation as entity, and counts on the badge).

func (r *recordingNotifier) CreateHiddenChatMessagePush(_ context.Context, userID uuid.UUID, muted bool) error {
	r.created = append(r.created, recordedNotification{userID: userID, pushed: !muted, notifType: "hidden_chat"})
	return nil
}

func (f *notifyCapture) CreateHiddenChatMessagePush(_ context.Context, userID uuid.UUID, _ bool) error {
	f.got = append(f.got, capturedNotification{recipient: userID, notifType: "hidden_chat"})
	return nil
}

func (f *flakyNotifier) CreateHiddenCallPush(_ context.Context, _ uuid.UUID, notifType string, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identities = append(f.identities, "hidden:"+notifType)
	return nil
}

func lockedMessageEvent(t *testing.T, sender uuid.UUID, recipients, locked []string) kafka.Message {
	t.Helper()
	payload, err := json.Marshal(messageCreatedPayload{
		MessageID:          uuid.NewString(),
		ConversationID:     uuid.NewString(),
		SenderID:           sender.String(),
		Type:               "text",
		Text:               "meet at 9",
		CreatedAt:          time.Now().UTC(),
		RecipientIDs:       recipients,
		LockedRecipientIDs: locked,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(chatEnvelope{EventID: uuid.NewString(), EventType: chatEventMessageCreated, Payload: payload})
	return kafka.Message{Value: envelope}
}

func TestLockedRecipientGetsOnlyTheContentFreePush(t *testing.T) {
	sender, hider, other := uuid.New(), uuid.New(), uuid.New()
	notifier := &recordingNotifier{}
	consumer := &ChatConsumer{service: notifier}
	if err := consumer.processMessage(context.Background(), lockedMessageEvent(t, sender,
		[]string{hider.String(), other.String()}, []string{hider.String()})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	var hiderRows, otherRows []recordedNotification
	for _, n := range notifier.created {
		switch n.userID {
		case hider:
			hiderRows = append(hiderRows, n)
		case other:
			otherRows = append(otherRows, n)
		}
	}
	if len(hiderRows) != 1 || hiderRows[0].notifType != "hidden_chat" {
		t.Fatalf("the member who hid the chat must get exactly the hidden-chat push, got %+v", hiderRows)
	}
	if len(otherRows) != 1 || otherRows[0].notifType != "dm" || otherRows[0].msg.Text != "meet at 9" {
		t.Fatalf("the other member is unaffected (normal dm with preview), got %+v", otherRows)
	}
}

func TestHiddenDatingMessageSkipsTheMatchRow(t *testing.T) {
	recipient := uuid.New()
	capture := &notifyCapture{}
	consumer := &ChatConsumer{service: capture}
	if err := consumer.handleChatDatingMessageNew(context.Background(), chatDatingMessageNewPayload{
		ConversationID: uuid.NewString(), MatchID: uuid.NewString(), SenderID: uuid.NewString(),
		RecipientID: recipient.String(), HiddenChat: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(capture.got) != 1 || capture.got[0].notifType != "hidden_chat" {
		t.Fatalf("hidden dating message must be the hidden push only, got %+v", capture.got)
	}
}

func TestHiddenCallRingsWithoutARow(t *testing.T) {
	notifier := &flakyNotifier{}
	consumer := newCallConsumerForTest(nil, nil, notifier, time.Millisecond)
	payload, _ := json.Marshal(map[string]any{
		"call_id": uuid.NewString(), "invite_id": uuid.NewString(),
		"inviter_user_id": uuid.NewString(), "invitee_user_id": uuid.NewString(),
		"call_type": "direct_audio", "created_at": time.Now().UTC(), "hidden_chat": true,
	})
	env, _ := json.Marshal(map[string]any{"event_id": uuid.NewString(), "event_type": "CallInvited", "payload": json.RawMessage(payload)})
	if err := consumer.processMessage(context.Background(), kafka.Message{Value: env}); err != nil {
		t.Fatal(err)
	}
	_, ids, rows := notifier.state()
	if rows != 0 || len(ids) != 1 || ids[0] != "hidden:incoming_call" {
		t.Fatalf("hidden-chat call must ring push-only with no inbox row, got ids=%v rows=%d", ids, rows)
	}
}

func TestHiddenChatPushDataCarriesNothingAboutTheChat(t *testing.T) {
	recipient := uuid.New()
	data := service.HiddenChatPushData(recipient)
	if data["title"] != "New message" || data["body"] != "" {
		t.Fatalf("copy: %q / %q", data["title"], data["body"])
	}
	if data["entity_id"] != uuid.Nil.String() || data["deep_link"] != "/messenger" {
		t.Fatalf("must not name the conversation: %v", data)
	}
	if data["collapse_key"] != "hidden_chat:"+recipient.String() {
		t.Fatalf("all hidden messages collapse into one notification: %v", data)
	}
	for k, v := range data {
		if strings.Contains(k, "sender") || strings.Contains(k, "avatar") || strings.Contains(k, "conversation") || strings.Contains(k, "message_id") {
			t.Fatalf("hidden push leaks %s=%s", k, v)
		}
	}
}
