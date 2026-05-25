package service

import (
	"context"
	"testing"
)

// TestPublishWithNoStoreAndNoWriter verifies the HP1 outbox migration
// kept the fallback path safe: when neither the Postgres-backed store
// (production) nor the Kafka writer (unit tests) is wired, publish()
// must be a no-op rather than panic on a nil dereference.
//
// This is the path the existing payout_test / pricing_test tests rely
// on — they construct &Service{} directly and would crash if publish
// reached the writer.
func TestPublishWithNoStoreAndNoWriter(t *testing.T) {
	s := &Service{}
	// Must not panic.
	s.publish(context.Background(), "commerce.test.smoke", map[string]any{"hello": "world"})
	s.publishWithIdempotency(context.Background(), "commerce.test.idemp", "key-123", map[string]any{"x": 1})
}
