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

	exclusions []pgstore.ContentTypeExclusion
	cleared    []uuid.UUID

	// eligibilityRev mirrors the SQL gate in Store.ApplyContentEligibility:
	// a write lands only when its revision advances (or equals, when the
	// caller says the source is authoritative), or when the caller has
	// decided the revision is to be ignored. eligibility holds what
	// landed, in order.
	eligibilityRev int64
	eligibility    []pgstore.ContentEligibility
}

func (s *recordingStore) UpsertContentOwnership(_ context.Context, o pgstore.ContentOwnership) error {
	s.upserts = append(s.upserts, o)
	return nil
}

func (s *recordingStore) UpdateContentType(_ context.Context, _, _ uuid.UUID, contentType string) (bool, error) {
	s.updates = append(s.updates, contentType)
	return s.applied, s.updateErr
}

func (s *recordingStore) RecordContentTypeExclusion(_ context.Context, e pgstore.ContentTypeExclusion) error {
	s.exclusions = append(s.exclusions, e)
	return nil
}

func (s *recordingStore) ClearContentTypeExclusion(_ context.Context, contentID uuid.UUID) error {
	s.cleared = append(s.cleared, contentID)
	return nil
}

func (s *recordingStore) ApplyContentEligibility(_ context.Context, e pgstore.ContentEligibility) (bool, error) {
	switch {
	case e.IgnoreRev:
	case e.AllowEqualRev && e.Rev >= s.eligibilityRev:
	case !e.AllowEqualRev && e.Rev > s.eligibilityRev:
	default:
		return false, nil
	}
	if e.Rev > s.eligibilityRev {
		s.eligibilityRev = e.Rev
	}
	s.eligibility = append(s.eligibility, e)
	return true, nil
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

// A PostCreated whose content_type is not a monetizable kind — a text
// post, a poll, an empty field from an old producer — must not be
// projected as anything the rate lookup or the display-view rule could
// mistake for a label. It is stored as 'unknown' and the raw value is
// quarantined in content_type_exclusions so it can be found later. The
// old default of "post" was exactly such a label.
func TestOwnershipUnknownTypeIsQuarantined(t *testing.T) {
	for _, raw := range []string{"", "post", "poll", "image", "livestream"} {
		t.Run("raw="+raw, func(t *testing.T) {
			store := &recordingStore{}
			consumer := NewContentOwnershipConsumer(store)
			post, author := uuid.New(), uuid.New()
			value := envelopeFor(t, events.PostCreated, events.PostCreatedPayload{
				PostID: post.String(), AuthorID: author.String(),
				ContentType: raw, CreatedAt: time.Now().UTC(),
			})
			if err := consumer.apply(context.Background(), value); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(store.upserts) != 1 {
				t.Fatalf("upserts=%v, want exactly one", store.upserts)
			}
			if got := store.upserts[0].ContentType; got != "unknown" {
				t.Fatalf("stored content_type=%q, want \"unknown\" (never \"post\")", got)
			}
			if len(store.exclusions) != 1 {
				t.Fatalf("exclusions=%v, want exactly one", store.exclusions)
			}
			ex := store.exclusions[0]
			if ex.ContentID != post || ex.CreatorID != author || ex.RawContentType != raw || ex.Source != events.PostCreated {
				t.Fatalf("exclusion=%+v, want content=%s creator=%s raw=%q source=%s", ex, post, author, raw, events.PostCreated)
			}
		})
	}

	// Legacy synonyms are mapped, not quarantined: a "reel" is a flick
	// and a "video" is a long_video, and the row must say so.
	for raw, want := range map[string]string{"reel": postclassify.Flick, "short": postclassify.Flick, "video": postclassify.LongVideo} {
		t.Run("synonym="+raw, func(t *testing.T) {
			store := &recordingStore{}
			consumer := NewContentOwnershipConsumer(store)
			value := envelopeFor(t, events.PostCreated, events.PostCreatedPayload{
				PostID: uuid.NewString(), AuthorID: uuid.NewString(),
				ContentType: raw, CreatedAt: time.Now().UTC(),
			})
			if err := consumer.apply(context.Background(), value); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(store.upserts) != 1 || store.upserts[0].ContentType != want {
				t.Fatalf("upserts=%v, want one %q", store.upserts, want)
			}
			if len(store.exclusions) != 0 {
				t.Fatalf("a canonical synonym must not be quarantined: %v", store.exclusions)
			}
		})
	}
}

// A reclassification to a non-canonical kind is quarantined the same
// way, and one back to a canonical kind clears the quarantine.
func TestReclassificationToUnknownKindIsQuarantined(t *testing.T) {
	store := &recordingStore{applied: true}
	consumer := NewContentOwnershipConsumer(store)
	post, author := uuid.New(), uuid.New()

	value := envelopeFor(t, events.PostContentTypeChanged, events.PostContentTypeChangedPayload{
		PostID: post.String(), AuthorID: author.String(),
		OldType: postclassify.Flick, NewType: "poll",
	})
	if err := consumer.apply(context.Background(), value); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(store.updates) != 1 || store.updates[0] != "unknown" {
		t.Fatalf("updates=%v, want one \"unknown\"", store.updates)
	}
	if len(store.exclusions) != 1 || store.exclusions[0].RawContentType != "poll" {
		t.Fatalf("exclusions=%v, want one for \"poll\"", store.exclusions)
	}

	value = envelopeFor(t, events.PostContentTypeChanged, events.PostContentTypeChangedPayload{
		PostID: post.String(), AuthorID: author.String(),
		OldType: "poll", NewType: "video",
	})
	if err := consumer.apply(context.Background(), value); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(store.updates) != 2 || store.updates[1] != postclassify.LongVideo {
		t.Fatalf("updates=%v, want the second to be %q", store.updates, postclassify.LongVideo)
	}
	if len(store.cleared) != 1 || store.cleared[0] != post {
		t.Fatalf("cleared=%v, want %s", store.cleared, post)
	}
}

// PostSearchEligibilityChanged carries a monotonic search_rev. A
// late-delivered event with a revision at or below the one already
// applied must be dropped — committed past, never retried, and never
// written — or a stale approval resurrects a post that was since taken
// down. A zero revision is unusable and is honoured only for a delete.
func TestStaleEligibilityRevIsDropped(t *testing.T) {
	post, author := uuid.New(), uuid.New()
	eligibility := func(rev int64, visibility, review string, deleted bool) []byte {
		return envelopeFor(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
			PostID: post.String(), AuthorID: author.String(),
			Visibility: visibility, ReviewStatus: review, Deleted: deleted,
			SearchRev: rev, ChangedAt: time.Now().UTC(),
		})
	}

	t.Run("older revision is dropped", func(t *testing.T) {
		store := &recordingStore{eligibilityRev: 5}
		consumer := NewContentOwnershipConsumer(store)
		if err := consumer.apply(context.Background(), eligibility(3, "public", "approved", false)); err != nil {
			t.Fatalf("a stale event must be committed past, got %v", err)
		}
		if len(store.eligibility) != 0 {
			t.Fatalf("a stale revision was applied: %+v", store.eligibility)
		}
		if store.eligibilityRev != 5 {
			t.Fatalf("revision moved backwards to %d", store.eligibilityRev)
		}
	})

	t.Run("advancing revision is applied with the verdict of the rule", func(t *testing.T) {
		store := &recordingStore{eligibilityRev: 5}
		consumer := NewContentOwnershipConsumer(store)
		if err := consumer.apply(context.Background(), eligibility(6, "public", "rejected", false)); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 1 {
			t.Fatalf("eligibility=%+v, want one write", store.eligibility)
		}
		got := store.eligibility[0]
		if got.State != pgstore.EligibilityIneligible || got.Rev != 6 || got.ContentID != post || got.CreatorID != author {
			t.Fatalf("applied %+v, want ineligible at rev 6 for %s/%s", got, post, author)
		}
		if got.EffectiveFrom.IsZero() {
			t.Fatal("an eligibility change must carry the date it takes effect")
		}
		if err := consumer.apply(context.Background(), eligibility(7, "public", "approved", false)); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 2 || store.eligibility[1].State != pgstore.EligibilityEligible {
			t.Fatalf("eligibility=%+v, want a second, eligible write", store.eligibility)
		}
	})

	t.Run("zero revision without delete fails closed and is not written", func(t *testing.T) {
		store := &recordingStore{eligibilityRev: 5}
		consumer := NewContentOwnershipConsumer(store)
		err := consumer.apply(context.Background(), eligibility(0, "public", "approved", false))
		var permanent permanentOwnershipError
		if !errors.As(err, &permanent) {
			t.Fatalf("want a permanent error so the record is committed past, got %v", err)
		}
		if len(store.eligibility) != 0 {
			t.Fatalf("a zero-revision approval was applied: %+v", store.eligibility)
		}
	})

	t.Run("zero revision with delete is honoured", func(t *testing.T) {
		store := &recordingStore{eligibilityRev: 5}
		consumer := NewContentOwnershipConsumer(store)
		if err := consumer.apply(context.Background(), eligibility(0, "public", "approved", true)); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 1 || store.eligibility[0].State != pgstore.EligibilityDeleted || !store.eligibility[0].IgnoreRev {
			t.Fatalf("eligibility=%+v, want one deleted write that ignores the revision gate", store.eligibility)
		}
	})

	t.Run("PostDeleted and PostRestored are belt and braces", func(t *testing.T) {
		store := &recordingStore{eligibilityRev: 5}
		consumer := NewContentOwnershipConsumer(store)
		deleted := envelopeFor(t, events.PostDeleted, events.PostDeletedPayload{
			PostID: post.String(), AuthorID: author.String(),
			DeletedAt: time.Now().UTC(), SearchRev: 6,
		})
		if err := consumer.apply(context.Background(), deleted); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 1 || store.eligibility[0].State != pgstore.EligibilityDeleted || store.eligibility[0].Rev != 6 {
			t.Fatalf("eligibility=%+v, want deleted at rev 6", store.eligibility)
		}
		// A stale PostDeleted (rev 4) is dropped too.
		stale := envelopeFor(t, events.PostDeleted, events.PostDeletedPayload{
			PostID: post.String(), AuthorID: author.String(),
			DeletedAt: time.Now().UTC(), SearchRev: 4,
		})
		if err := consumer.apply(context.Background(), stale); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 1 {
			t.Fatalf("a stale PostDeleted was applied: %+v", store.eligibility)
		}
		// A restore never grants eligibility on its own: it lands as
		// ineligible until the authoritative event at the same
		// revision says otherwise.
		restored := envelopeFor(t, events.PostRestored, events.PostRestoredPayload{
			PostID: post.String(), AuthorID: author.String(),
			Visibility: "public", RestoredAt: time.Now().UTC(), SearchRev: 7,
		})
		if err := consumer.apply(context.Background(), restored); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 2 || store.eligibility[1].State != pgstore.EligibilityIneligible || store.eligibility[1].Rev != 7 {
			t.Fatalf("eligibility=%+v, want a restore to land as ineligible at rev 7", store.eligibility)
		}
		if err := consumer.apply(context.Background(), eligibility(7, "public", "approved", false)); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(store.eligibility) != 3 || store.eligibility[2].State != pgstore.EligibilityEligible {
			t.Fatalf("eligibility=%+v, want the authoritative same-revision event to apply", store.eligibility)
		}
	})
}
