package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// post.visibility_changed used to be defined and never consumed; it must now
// reach handlePostVisibilityChanged, and a malformed one must fail (so it is
// retried and then parked on the DLQ) rather than be silently dropped.

func visibilityMessage(t *testing.T, p events.PostVisibilityChangedPayload) kafka.Message {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events.EventEnvelope{
		EventID:   uuid.New().String(),
		EventType: events.EventPostVisibilityChanged,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: raw}
}

func TestVisibilityChanged_IsRoutedAndValidated(t *testing.T) {
	c := &Consumer{} // no stores: the handler validates, then has nothing to write

	ok := visibilityMessage(t, events.PostVisibilityChangedPayload{
		PostID: uuid.New().String(), AuthorID: uuid.New().String(),
		OldVisibility: "public", NewVisibility: "unlisted",
	})
	if err := c.processMessage(context.Background(), ok); err != nil {
		t.Fatalf("well-formed event: %v", err)
	}

	bad := visibilityMessage(t, events.PostVisibilityChangedPayload{
		PostID: "not-a-uuid", AuthorID: uuid.New().String(), NewVisibility: "unlisted",
	})
	err := c.processMessage(context.Background(), bad)
	if err == nil || !strings.Contains(err.Error(), "PostVisibilityChanged") {
		t.Fatalf("malformed event: err = %v, want a PostVisibilityChanged error (proof it was routed)", err)
	}
}
