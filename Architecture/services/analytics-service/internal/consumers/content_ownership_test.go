package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/postclassify"
	"github.com/google/uuid"
)

type recordingStore struct {
	upserts   []pgstore.ContentOwnership
	updates   []string
	applied   bool
	updateErr error
}

func (s *recordingStore) UpsertContentOwnership(_ context.Context, o pgstore.ContentOwnership) error {
	s.upserts = append(s.upserts, o)
	return nil
}

func (s *recordingStore) UpdateContentType(_ context.Context, _, _ uuid.UUID, contentType string) (bool, error) {
	s.updates = append(s.updates, contentType)
	return s.applied, s.updateErr
}

func envelopeFor(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(events.EventEnvelope{
		EventType: eventType, Payload: body, OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// A post that uploaded as a plain video and turned out to be a flick is
// reclassified by post-service after transcode. Analytics has to follow:
// content_type here picks the display-view bar and, downstream, the RPM
// rate the creator fund settles at (flick 300 paise / 1000, long_video
// 5000). Before this consumer handled the event, the ownership row kept
// whatever kind the post had at create time, forever.
func TestReclassificationUpdatesTheOwnershipContentType(t *testing.T) {
	store := &recordingStore{applied: true}
	consumer := NewContentOwnershipConsumer(store)
	post, author := uuid.New(), uuid.New()

	value := envelopeFor(t, events.PostContentTypeChanged, events.PostContentTypeChangedPayload{
		PostID: post.String(), AuthorID: author.String(),
		OldType: postclassify.LongVideo, NewType: postclassify.Flick,
		ChangedAt: time.Now().UTC(),
	})
	if err := consumer.apply(context.Background(), value); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(store.updates) != 1 || store.updates[0] != postclassify.Flick {
		t.Fatalf("updates=%v, want one %q", store.updates, postclassify.Flick)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("a reclassification must not create an ownership row: %v", store.upserts)
	}
}

// No row to correct is not a reason to stall the partition: PostCreated
// is produced to the same topic under the same key, so it has already
// been through. Retrying forever would wedge the projection.
func TestReclassificationWithNoOwnershipRowIsNotRetried(t *testing.T) {
	store := &recordingStore{applied: false}
	consumer := NewContentOwnershipConsumer(store)

	value := envelopeFor(t, events.PostContentTypeChanged, events.PostContentTypeChangedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(),
		OldType: postclassify.Flick, NewType: postclassify.LongVideo,
	})
	if err := consumer.apply(context.Background(), value); err != nil {
		t.Fatalf("a missing row must be a no-op, got %v", err)
	}
}

// A database failure, by contrast, must stall — the record has to be
// reapplied rather than dropped, or the row stays on the wrong rate.
func TestReclassificationSurfacesADurableFailure(t *testing.T) {
	store := &recordingStore{updateErr: errors.New("connection refused")}
	consumer := NewContentOwnershipConsumer(store)

	value := envelopeFor(t, events.PostContentTypeChanged, events.PostContentTypeChangedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(),
		OldType: postclassify.LongVideo, NewType: postclassify.Flick,
	})
	err := consumer.apply(context.Background(), value)
	if err == nil {
		t.Fatal("a store failure must be returned so the record is retried")
	}
	var permanent permanentOwnershipError
	if errors.As(err, &permanent) {
		t.Fatal("a store failure is durable, not permanent — it must not be committed past")
	}
}

func TestReclassificationRejectsAnUnusablePayload(t *testing.T) {
	cases := map[string]events.PostContentTypeChangedPayload{
		"bad post_id":   {PostID: "not-a-uuid", AuthorID: uuid.NewString(), NewType: postclassify.Flick},
		"nil post_id":   {PostID: uuid.Nil.String(), AuthorID: uuid.NewString(), NewType: postclassify.Flick},
		"bad author_id": {PostID: uuid.NewString(), AuthorID: "not-a-uuid", NewType: postclassify.Flick},
		"no new_type":   {PostID: uuid.NewString(), AuthorID: uuid.NewString(), NewType: ""},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingStore{applied: true}
			consumer := NewContentOwnershipConsumer(store)
			err := consumer.apply(context.Background(), envelopeFor(t, events.PostContentTypeChanged, payload))
			var permanent permanentOwnershipError
			if !errors.As(err, &permanent) {
				t.Fatalf("want a permanent error so the record is committed past, got %v", err)
			}
			if len(store.updates) != 0 {
				t.Fatalf("nothing should have been written: %v", store.updates)
			}
		})
	}
}

// PostCreated still projects, and still carries post-service's
// postclassify decision verbatim.
func TestPostCreatedProjectsTheClassifiedKind(t *testing.T) {
	store := &recordingStore{}
	consumer := NewContentOwnershipConsumer(store)
	post, author := uuid.New(), uuid.New()

	value := envelopeFor(t, events.PostCreated, events.PostCreatedPayload{
		PostID: post.String(), AuthorID: author.String(),
		ContentType: postclassify.Flick, CreatedAt: time.Now().UTC(),
	})
	if err := consumer.apply(context.Background(), value); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].ContentType != postclassify.Flick {
		t.Fatalf("upserts=%v", store.upserts)
	}
}

// An event this consumer has no business with must not reach the store.
func TestUnrelatedEventsAreIgnored(t *testing.T) {
	store := &recordingStore{}
	consumer := NewContentOwnershipConsumer(store)
	value := envelopeFor(t, events.PostReacted, map[string]string{"post_id": uuid.NewString()})
	if err := consumer.apply(context.Background(), value); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(store.upserts) != 0 || len(store.updates) != 0 {
		t.Fatalf("store was touched: upserts=%v updates=%v", store.upserts, store.updates)
	}
}
