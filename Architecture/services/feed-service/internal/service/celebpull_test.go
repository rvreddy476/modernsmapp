package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Celeb pull: pulled rows merge into the home read by timeline key, one row
// per post, and the merge keeps the page cursor exact.

func rowAt(post uuid.UUID, at time.Time) scylla.FeedItem {
	return scylla.FeedItem{PostID: post, CreatedAt: at, CursorToken: scylla.TimelineTS(post, at).String()}
}

func TestMergeTimelineRows_NewestFirstDedupedAndBounded(t *testing.T) {
	base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	shared := uuid.New() // pushed before the author became a celeb, and pulled now
	home := []scylla.FeedItem{
		rowAt(uuid.New(), base),
		rowAt(shared, base.Add(-2*time.Minute)),
		rowAt(uuid.New(), base.Add(-4*time.Minute)),
	}
	pulled := []scylla.FeedItem{
		rowAt(uuid.New(), base.Add(time.Minute)),
		rowAt(shared, base.Add(-2*time.Minute)),
		rowAt(uuid.New(), base.Add(-3*time.Minute)),
	}
	got := mergeTimelineRows(home, pulled, 4)
	if len(got) != 4 {
		t.Fatalf("merged %d rows, want 4 (limit)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i-1].CreatedAt.After(got[i].CreatedAt) {
			t.Fatalf("rows not newest first at %d: %v then %v", i, got[i-1].CreatedAt, got[i].CreatedAt)
		}
	}
	seen := map[uuid.UUID]int{}
	for _, r := range got {
		seen[r.PostID]++
	}
	if seen[shared] != 1 {
		t.Fatalf("the post present in both sources appears %d times", seen[shared])
	}
	// The row cut by the limit (-4m) is older than every row kept, so the
	// next page — starting below the oldest kept key — reads it.
	if oldest := got[len(got)-1].CreatedAt; !oldest.After(base.Add(-4 * time.Minute)) {
		t.Fatalf("the limit cut a row newer than one it kept")
	}
}

func TestFanoutPost_ThresholdFlagsCelebAndStopsThePush(t *testing.T) {
	author := uuid.New()
	followers := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	s, tl := fanoutStubs(t, followers, nil, nil, false)
	marker := &recordingCelebs{}
	s.celebs = marker
	s.celeb = celebConfig{pull: true, threshold: 3}

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if !marker.flagged(author) {
		t.Fatal("an author at the threshold was not flagged celeb")
	}
	for _, f := range followers {
		if got := tl.rows(f); got != 0 {
			t.Fatalf("a follower of a newly flagged celeb got %d pushed rows; the read path pulls them", got)
		}
	}
	if got := tl.rows(author); got != 1 {
		t.Fatalf("author got %d own rows, want 1", got)
	}
}

func TestFanoutPost_ThresholdIgnoredWithoutPull(t *testing.T) {
	author, follower := uuid.New(), uuid.New()
	s, tl := fanoutStubs(t, []uuid.UUID{follower}, nil, nil, false)
	marker := &recordingCelebs{}
	s.celebs = marker
	s.celeb = celebConfig{pull: false, threshold: 1}

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "post", "public", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if marker.flagged(author) {
		t.Fatal("flagged a celeb with the pull read path off: their posts would reach nobody")
	}
	if got := tl.rows(follower); got != 1 {
		t.Fatalf("follower got %d rows, want 1", got)
	}
}

type recordingCelebs struct {
	mu  sync.Mutex
	set map[uuid.UUID]bool
}

func (r *recordingCelebs) IsCeleb(_ context.Context, id uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.set[id], nil
}

func (r *recordingCelebs) SetCelebStatus(_ context.Context, id uuid.UUID, celeb bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.set == nil {
		r.set = map[uuid.UUID]bool{}
	}
	r.set[id] = celeb
	return nil
}

func (r *recordingCelebs) flagged(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.set[id]
}
