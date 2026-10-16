package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// MessageCreated.type ("text" | "media" | "post") must reach the delivery
// call, because it is what turns a shared post's push into "Sent you a post"
// (TikTok parity T-25). The row itself stays a "dm" on the conversation.
func TestMessageCreatedThreadsTheMessageType(t *testing.T) {
	for _, msgType := range []string{"text", "media", "post"} {
		t.Run(msgType, func(t *testing.T) {
			sender, recipient, conversation := uuid.New(), uuid.New(), uuid.New()
			payload, err := json.Marshal(messageCreatedPayload{
				MessageID:      uuid.NewString(),
				ConversationID: conversation.String(),
				SenderID:       sender.String(),
				Type:           msgType,
				CreatedAt:      time.Now().UTC(),
				RecipientIDs:   []string{recipient.String()},
			})
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := json.Marshal(chatEnvelope{
				EventID: uuid.NewString(), EventType: chatEventMessageCreated, Payload: payload,
			})
			if err != nil {
				t.Fatal(err)
			}

			notifier := &recordingNotifier{}
			consumer := &ChatConsumer{service: notifier}
			if err := consumer.processMessage(context.Background(), kafka.Message{Value: envelope}); err != nil {
				t.Fatalf("processMessage: %v", err)
			}
			row, ok := notifier.forUser(recipient)
			if !ok {
				t.Fatal("recipient was not notified")
			}
			if row.notifType != "dm" || row.messageType != msgType || !row.pushed {
				t.Fatalf("got %+v, want a pushed dm carrying type %q", row, msgType)
			}
		})
	}
}
