package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// A stray user.deletion_requested must not erase anything: erasure has a
// 30-day recovery window and runs on user.purge_requested. With no store and
// no lifecycle handler wired, any attempt to touch media would panic.
func TestLegacyDeletionRequestedErasesNothing(t *testing.T) {
	payload, _ := json.Marshal(events.UserDeletionRequestedPayload{UserID: "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"})
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.EventUserDeletionRequested, Payload: payload})

	c := &Consumer{}
	if !c.handle(context.Background(), kafka.Message{Value: env}) {
		t.Fatal("handle reported shutdown for a legacy deletion event")
	}
}
