package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// reels-1 (reels readiness audit, F-2): the review-gate step of the
// MediaTranscodeCompleted consumer had no behaviour test, and it logged and
// dropped every lookup and flip error, so the offset was committed with the
// post still pending for ever.

// fakeGate is an in-memory reviewGateStore: one post per media asset, its
// review_status, and failures to inject.
type fakeGate struct {
	byMedia      map[uuid.UUID]uuid.UUID // media -> post
	status       map[uuid.UUID]string    // post -> review_status
	failed       map[uuid.UUID]bool      // post -> video_metadata marked failed
	lookupErr    error
	flipFailures int // the next N flips fail
	flips        int
	reviews      int
}

func newFakeGate() *fakeGate {
	return &fakeGate{byMedia: map[uuid.UUID]uuid.UUID{}, status: map[uuid.UUID]string{}, failed: map[uuid.UUID]bool{}}
}

func (f *fakeGate) pendingPost() (media, post uuid.UUID) {
	media, post = uuid.New(), uuid.New()
	f.byMedia[media] = post
	f.status[post] = "pending"
	return media, post
}

func (f *fakeGate) GetVideoMetadataByMediaAsset(_ context.Context, mediaID uuid.UUID) (*postgres.VideoMetadata, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	post, ok := f.byMedia[mediaID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	m := mediaID
	return &postgres.VideoMetadata{PostID: post, MediaAssetID: &m, UploadStatus: "pending"}, nil
}

func (f *fakeGate) FlipReviewStatusFromPending(_ context.Context, postID uuid.UUID, newStatus string) (bool, error) {
	f.flips++
	if f.flipFailures > 0 {
		f.flipFailures--
		return false, errors.New("lock timeout")
	}
	if f.status[postID] != "pending" {
		return false, nil
	}
	f.status[postID] = newStatus
	return true, nil
}

func (f *fakeGate) MarkVideoMetadataFailed(_ context.Context, postID uuid.UUID) error {
	f.failed[postID] = true
	return nil
}

func (f *fakeGate) InsertModerationReview(context.Context, *postgres.ModerationReview) error {
	f.reviews++
	return nil
}

func (f *fakeGate) ListPendingVideoPosts(context.Context, time.Time, *postgres.PendingVideoCursor, int) ([]postgres.PendingVideoPost, error) {
	return nil, nil
}

func (f *fakeGate) GetPendingVideoPost(context.Context, uuid.UUID) (*postgres.PendingVideoPost, error) {
	return nil, nil
}

// completed is a MediaTranscodeCompleted envelope with no playback URLs, so
// handle stops after the review-gate step (nothing to wire through).
func completed(t *testing.T, media uuid.UUID, processing, moderation string) *events.EventEnvelope {
	t.Helper()
	raw, err := json.Marshal(events.MediaTranscodeCompletedPayload{
		MediaAssetID: media.String(), ProcessingStatus: processing, ModerationStatus: moderation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &events.EventEnvelope{EventID: uuid.NewString(), EventType: events.MediaTranscodeCompleted, Payload: raw}
}

func TestReviewGateDecidesFromTheVerdict(t *testing.T) {
	cases := []struct {
		name, processing, moderation, want string
		mediaFailed                        bool
	}{
		{"ready and passed", "ready", "passed", "approved", false},
		{"ready and approved", "ready", "approved", "approved", false},
		{"transcode failed", "failed", "", "rejected", true},
		{"scan rejected", "ready", "rejected", "rejected", false},
		{"manual review waits", "ready", "manual_review", "pending", false},
		{"still processing waits", "processing", "pending", "pending", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := newFakeGate()
			media, post := gate.pendingPost()
			c := &MediaTranscodeConsumer{gate: gate}
			if err := c.handle(context.Background(), completed(t, media, tc.processing, tc.moderation)); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if got := gate.status[post]; got != tc.want {
				t.Fatalf("review_status = %q, want %q", got, tc.want)
			}
			if gate.failed[post] != tc.mediaFailed {
				t.Fatalf("video_metadata marked failed = %v, want %v", gate.failed[post], tc.mediaFailed)
			}
			if tc.want == "pending" && gate.flips != 0 {
				t.Fatalf("a non-terminal verdict flipped the post (%d flips)", gate.flips)
			}
			if tc.want != "pending" && gate.reviews != 1 {
				t.Fatalf("audit rows = %d, want 1", gate.reviews)
			}
		})
	}
}

// A flip that fails is returned (the message is retried, then
// dead-lettered), and the redelivery approves the post. It used to return
// nil: the offset was committed and the post stayed pending.
func TestReviewGateFlipFailureIsRetried(t *testing.T) {
	gate := newFakeGate()
	media, post := gate.pendingPost()
	gate.flipFailures = 1
	c := &MediaTranscodeConsumer{gate: gate}
	env := completed(t, media, "ready", "passed")
	if err := c.handle(context.Background(), env); err == nil {
		t.Fatal("a failed flip was swallowed: the offset would be committed with the post pending")
	}
	if gate.status[post] != "pending" {
		t.Fatalf("status after the failed flip = %q", gate.status[post])
	}
	if err := c.handle(context.Background(), env); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if gate.status[post] != "approved" {
		t.Fatalf("status after the redelivery = %q, want approved", gate.status[post])
	}
	// A third delivery is a no-op: the flip is a compare-and-set.
	if err := c.handle(context.Background(), env); err != nil || gate.reviews != 1 {
		t.Fatalf("duplicate delivery: err=%v audit rows=%d", err, gate.reviews)
	}
}

// Only "no row for this media" means nothing to gate; any other lookup
// failure is retried instead of being read as "no post".
func TestReviewGateLookupErrorsAreRetried(t *testing.T) {
	gate := newFakeGate()
	media, _ := gate.pendingPost()
	gate.lookupErr = errors.New("connection reset")
	c := &MediaTranscodeConsumer{gate: gate}
	if err := c.handle(context.Background(), completed(t, media, "ready", "passed")); err == nil {
		t.Fatal("a lookup failure was read as 'no post' and committed")
	}
	gate.lookupErr = nil
	if err := c.handle(context.Background(), completed(t, uuid.New(), "ready", "passed")); err != nil {
		t.Fatalf("media with no post: %v, want nil (nothing to gate)", err)
	}
}
