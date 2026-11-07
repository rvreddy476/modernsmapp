package service

import (
	"context"
	"testing"

	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// outboxRecordingStore keeps the MessageCreated payloads a send queues.
type outboxRecordingStore struct {
	*datingSendStore
	created []sharedEvents.MessageCreatedPayload
}

func (s *outboxRecordingStore) InsertOutboxEventOnce(_ context.Context, _ string, eventType string, payload interface{}) error {
	if p, ok := payload.(sharedEvents.MessageCreatedPayload); ok && eventType == sharedEvents.MessageCreated {
		s.created = append(s.created, p)
	}
	return nil
}

// MessageCreated carries the conversation's type ("direct" | "group", as on
// ConversationReported) so notification-service picks the group or DM deep
// link without guessing from the recipient count (a two-person group used
// to get the DM link).
func TestMessageCreatedCarriesTheConversationType(t *testing.T) {
	for _, convType := range []string{"direct", "group"} {
		f := newDatingFixture(t, "", false)
		f.store.conversations[f.convID].Type = convType
		rec := &outboxRecordingStore{datingSendStore: f.store}
		f.svc.convStore = rec
		f.send(t, f.userA, "type-"+convType)
		if len(rec.created) != 1 || rec.created[0].ConversationType != convType {
			t.Fatalf("%s conversation: MessageCreated = %+v, want conversation_type %q", convType, rec.created, convType)
		}
	}

	// Fails open: no conversation row, no field (the consumer falls back).
	f := newDatingFixture(t, "", false)
	if got := f.svc.conversationTypeOf(context.Background(), uuid.New()); got != "" {
		t.Fatalf("missing conversation: type %q, want empty", got)
	}
}
