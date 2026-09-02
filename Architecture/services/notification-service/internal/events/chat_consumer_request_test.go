package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// A message request reaches the recipient as ONE notification — the
// message_request row with its Accept / Decline / Block actions — not as
// that row plus a "dm" for the same message.
func TestMessageCreated_FirstRequestMessageRaisesNoDMNotification(t *testing.T) {
	recipient := uuid.New()
	sender := uuid.New()
	payload, err := json.Marshal(messageCreatedPayload{
		MessageID:      uuid.NewString(),
		ConversationID: uuid.NewString(),
		SenderID:       sender.String(),
		Type:           "text",
		CreatedAt:      time.Now().UTC(),
		RecipientIDs:   []string{recipient.String()},
		IsRequest:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(chatEnvelope{
		EventID:   uuid.NewString(),
		EventType: chatEventMessageCreated,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}

	notifier := &recordingNotifier{}
	consumer := &ChatConsumer{service: notifier}
	if err := consumer.processMessage(context.Background(), kafka.Message{Value: envelope}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if len(notifier.created) != 0 {
		t.Fatalf("expected no dm notification for a first request message, got %d", len(notifier.created))
	}
}

// The request itself still lands: the MessageRequestCreated event writes the
// actionable row for the receiver, and only the receiver.
func TestMessageRequestCreated_NotifiesReceiverOnly(t *testing.T) {
	receiver := uuid.New()
	sender := uuid.New()
	conversation := uuid.New()
	payload, err := json.Marshal(messageRequestCreatedPayload{
		ConversationID: conversation.String(),
		SenderID:       sender.String(),
		ReceiverID:     receiver.String(),
		Preview:        "hello",
		OccurredAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(chatEnvelope{
		EventID:   uuid.NewString(),
		EventType: chatEventMessageRequestCreated,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}

	notifier := &recordingNotifier{}
	consumer := &ChatConsumer{service: notifier}
	if err := consumer.processMessage(context.Background(), kafka.Message{Value: envelope}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if len(notifier.created) != 1 {
		t.Fatalf("expected exactly one notification, got %d", len(notifier.created))
	}
	got := notifier.created[0]
	if got.userID != receiver {
		t.Fatalf("notified %s, want receiver %s", got.userID, receiver)
	}
	if !got.pushed {
		t.Fatal("a message request must push: the receiver has a decision to make")
	}
}
