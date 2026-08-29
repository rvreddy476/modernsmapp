package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Per-conversation mute must reach the DEVICE decision, not just the chat
// inbox. Chat owns the setting and ships it on the event; this consumer must
// still write the durable row for a muted recipient (mute is not "hide") while
// sending no push. Before this, a muted conversation buzzed the phone on every
// message and only the chat list looked muted.

type recordedNotification struct {
	userID  uuid.UUID
	pushed  bool
	deepLnk string
}

type recordingNotifier struct {
	created []recordedNotification
	failOn  uuid.UUID
}

func (r *recordingNotifier) CreateNotification(_ context.Context, userID, _ uuid.UUID, _, _ string, _ uuid.UUID, deepLink string, _ time.Time) error {
	r.created = append(r.created, recordedNotification{userID: userID, pushed: true, deepLnk: deepLink})
	if userID == r.failOn {
		return context.DeadlineExceeded
	}
	return nil
}

func (r *recordingNotifier) CreateNotificationWithoutPush(_ context.Context, userID, _ uuid.UUID, _, _ string, _ uuid.UUID, deepLink string, _ time.Time) error {
	r.created = append(r.created, recordedNotification{userID: userID, pushed: false, deepLnk: deepLink})
	return nil
}

func (r *recordingNotifier) forUser(id uuid.UUID) (recordedNotification, bool) {
	for _, n := range r.created {
		if n.userID == id {
			return n, true
		}
	}
	return recordedNotification{}, false
}

func messageCreatedEvent(t *testing.T, conversationID, senderID uuid.UUID, recipients, muted []string) kafka.Message {
	t.Helper()
	payload, err := json.Marshal(messageCreatedPayload{
		MessageID:         uuid.NewString(),
		ConversationID:    conversationID.String(),
		SenderID:          senderID.String(),
		Type:              "text",
		CreatedAt:         time.Now().UTC(),
		RecipientIDs:      recipients,
		MutedRecipientIDs: muted,
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
	return kafka.Message{Value: envelope}
}

func TestMutedRecipientGetsTheInboxRowButNoPush(t *testing.T) {
	sender, quiet, loud := uuid.New(), uuid.New(), uuid.New()
	conversationID := uuid.New()
	notifier := &recordingNotifier{}
	consumer := &ChatConsumer{service: notifier}

	err := consumer.processMessage(context.Background(), messageCreatedEvent(
		t, conversationID, sender,
		[]string{quiet.String(), loud.String()},
		[]string{quiet.String()},
	))
	if err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	quietRow, ok := notifier.forUser(quiet)
	if !ok {
		t.Fatal("muted recipient got no notification row — mute must silence the device, not hide the message")
	}
	if quietRow.pushed {
		t.Fatal("muted recipient was pushed")
	}
	loudRow, ok := notifier.forUser(loud)
	if !ok || !loudRow.pushed {
		t.Fatalf("unmuted recipient did not get a pushed notification: %+v", loudRow)
	}
	if quietRow.deepLnk != loudRow.deepLnk || quietRow.deepLnk == "" {
		t.Fatalf("deep link differs by mute state: %q vs %q", quietRow.deepLnk, loudRow.deepLnk)
	}
}

// An older publisher omits the field entirely: everyone is pushed, exactly as
// before. The additive contract must not silence anyone by accident.
func TestAbsentMutedListPushesEveryRecipient(t *testing.T) {
	sender, a, b := uuid.New(), uuid.New(), uuid.New()
	notifier := &recordingNotifier{}
	consumer := &ChatConsumer{service: notifier}

	if err := consumer.processMessage(context.Background(), messageCreatedEvent(
		t, uuid.New(), sender, []string{a.String(), b.String()}, nil,
	)); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if len(notifier.created) != 2 {
		t.Fatalf("expected both recipients notified, got %d", len(notifier.created))
	}
	for _, n := range notifier.created {
		if !n.pushed {
			t.Fatalf("recipient %s was silenced without a mute", n.userID)
		}
	}
}

// The sender never notifies themselves, muted or not.
func TestSenderIsNeverNotified(t *testing.T) {
	sender, other := uuid.New(), uuid.New()
	notifier := &recordingNotifier{}
	consumer := &ChatConsumer{service: notifier}

	if err := consumer.processMessage(context.Background(), messageCreatedEvent(
		t, uuid.New(), sender,
		[]string{sender.String(), other.String()},
		[]string{sender.String()},
	)); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if _, ok := notifier.forUser(sender); ok {
		t.Fatal("the sender was notified of their own message")
	}
	if len(notifier.created) != 1 {
		t.Fatalf("expected exactly one notification, got %d", len(notifier.created))
	}
}

// One recipient failing must not stop the fan-out to the others.
func TestFanOutContinuesAfterOneRecipientFails(t *testing.T) {
	sender, broken, healthy := uuid.New(), uuid.New(), uuid.New()
	notifier := &recordingNotifier{failOn: broken}
	consumer := &ChatConsumer{service: notifier}

	err := consumer.processMessage(context.Background(), messageCreatedEvent(
		t, uuid.New(), sender, []string{broken.String(), healthy.String()}, nil,
	))
	if err == nil {
		t.Fatal("a failed recipient must surface an error for redelivery")
	}
	if _, ok := notifier.forUser(healthy); !ok {
		t.Fatal("fan-out stopped at the first failure")
	}
}
