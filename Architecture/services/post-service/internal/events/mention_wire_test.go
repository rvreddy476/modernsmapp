package events

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// The user.mentioned wire contract (bug 10 Nov 2026: post mentions never
// reached the mentioned person). notification-service decodes exactly this:
// a shared EventEnvelope whose event_type is "user.mentioned" and whose
// payload carries mentioned_user_id / author_id / post_id / occurred_at. The
// payload is pinned byte for byte in testdata/contracts/user_mentioned_post.json,
// which notification-service copies into its own consumer test.
func TestUserMentionedMessageMatchesTheConsumerContract(t *testing.T) {
	mentioned := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	author := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	post := "11111111-1111-4111-8111-111111111111"
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

	msg, err := UserMentionedMessage(context.Background(), mentioned, author, post, at)
	if err != nil {
		t.Fatal(err)
	}
	// Keyed by the author, like every other per-user event on the topic.
	if string(msg.Key) != author.String() {
		t.Fatalf("key = %q, want the author %s", msg.Key, author)
	}

	var env events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		t.Fatalf("value is not an EventEnvelope: %v", err)
	}
	// The literal, not only the constant: the consumer's switch matches the
	// string, and a renamed constant on one side would still compile.
	if env.EventType != "user.mentioned" {
		t.Fatalf("event_type = %q, want user.mentioned", env.EventType)
	}
	if env.ActorUserID == nil || *env.ActorUserID != author.String() {
		t.Fatalf("actor_user_id = %v, want the author", env.ActorUserID)
	}
	if env.EventID == "" {
		t.Fatal("event_id is empty")
	}

	golden, err := os.ReadFile(filepath.Join("testdata", "contracts", "user_mentioned_post.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []byte(env.Payload), bytes.TrimSpace(golden); !bytes.Equal(got, want) {
		t.Fatalf("payload drifted from the contract\n got: %s\nwant: %s", got, want)
	}
}
