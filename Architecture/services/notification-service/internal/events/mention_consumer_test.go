package events

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type mentionCall struct {
	userID, actorID uuid.UUID
	notifType       string
	entityType      string
	entityID        uuid.UUID
	deepLink        string
	createdAt       time.Time
}

type fakeMentionNotifier struct{ got []mentionCall }

func (f *fakeMentionNotifier) CreateNotification(_ context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time) error {
	f.got = append(f.got, mentionCall{userID, actorID, notifType, entityType, entityID, deepLink, createdAt})
	return nil
}

// Bug 10 Nov 2026: a post @mention never reached the mentioned person. The
// payload below is post-service's pinned wire contract, copied byte for byte
// from post-service/internal/events/testdata/contracts/user_mentioned_post.json
// (post-service's TestUserMentionedMessageMatchesTheConsumerContract writes
// exactly it). Wrapped in the envelope post-service's producer writes (keyed
// by the author, actor set), it must become one "mention" notification for
// the mentioned person, from the author, opening the post.
func TestPostMentionOnTheWireBecomesAMentionNotification(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("testdata", "contracts", "user_mentioned_post.json"))
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.TrimSpace(payload)
	author := "22222222-2222-4222-8222-222222222222"
	value, err := json.Marshal(events.EventEnvelope{
		EventID: "4b1c7a52-7d2a-4d8e-9f00-000000000001", EventType: "user.mentioned",
		OccurredAt: time.Date(2026, 11, 10, 9, 30, 1, 0, time.UTC), ActorUserID: &author,
		Payload: json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeMentionNotifier{}
	c := &Consumer{mentionNotify: f}
	if err := c.processMessage(context.Background(), kafka.Message{Key: []byte(author), Value: value}); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 {
		t.Fatalf("notifications = %d, want 1", len(f.got))
	}
	want := mentionCall{
		userID:     uuid.MustParse("66666666-6666-4666-8666-666666666666"),
		actorID:    uuid.MustParse(author),
		notifType:  "mention",
		entityType: "post",
		entityID:   uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		deepLink:   "/post/11111111-1111-4111-8111-111111111111",
		createdAt:  time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC),
	}
	if got := f.got[0]; got.userID != want.userID || got.actorID != want.actorID || got.notifType != want.notifType ||
		got.entityType != want.entityType || got.entityID != want.entityID || got.deepLink != want.deepLink ||
		!got.createdAt.Equal(want.createdAt) {
		t.Fatalf("notification = %+v\nwant           %+v", got, want)
	}
}

// The service is what NewConsumerWithDialer stores as the mention notifier,
// so the seam above is the path a real event takes.
var _ mentionNotifier = (*service.Service)(nil)
