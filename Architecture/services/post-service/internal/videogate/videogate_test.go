package videogate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeStore keeps pending posts with their media state, in created_at order.
type fakeStore struct {
	rows     []postgres.PendingVideoPost
	status   map[uuid.UUID]string
	failed   map[uuid.UUID]bool
	listErr  error
	flipErr  error
	lastFrom []time.Time // olderThan of each list call
	cursors  []*postgres.PendingVideoCursor
}

func newFakeStore() *fakeStore {
	return &fakeStore{status: map[uuid.UUID]string{}, failed: map[uuid.UUID]bool{}}
}

func (f *fakeStore) add(created time.Time, processing, moderation string) uuid.UUID {
	id := uuid.New()
	f.rows = append(f.rows, postgres.PendingVideoPost{PostID: id, MediaID: uuid.New(), CreatedAt: created,
		ProcessingStatus: processing, ModerationStatus: moderation})
	f.status[id] = "pending"
	return id
}

func (f *fakeStore) ListPendingVideoPosts(_ context.Context, olderThan time.Time, after *postgres.PendingVideoCursor, limit int) ([]postgres.PendingVideoPost, error) {
	f.lastFrom = append(f.lastFrom, olderThan)
	f.cursors = append(f.cursors, after)
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []postgres.PendingVideoPost
	for _, r := range f.rows {
		if f.status[r.PostID] != "pending" || !r.CreatedAt.Before(olderThan) {
			continue
		}
		if after != nil && !(r.CreatedAt.After(after.CreatedAt) ||
			(r.CreatedAt.Equal(after.CreatedAt) && r.PostID.String() > after.PostID.String())) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) GetPendingVideoPost(_ context.Context, postID uuid.UUID) (*postgres.PendingVideoPost, error) {
	for _, r := range f.rows {
		if r.PostID == postID && f.status[postID] == "pending" {
			cp := r
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) FlipReviewStatusFromPending(_ context.Context, postID uuid.UUID, s string) (bool, error) {
	if f.flipErr != nil {
		return false, f.flipErr
	}
	if f.status[postID] != "pending" {
		return false, nil
	}
	f.status[postID] = s
	return true, nil
}

func (f *fakeStore) MarkVideoMetadataFailed(_ context.Context, postID uuid.UUID) error {
	f.failed[postID] = true
	return nil
}

func (f *fakeStore) InsertModerationReview(context.Context, *postgres.ModerationReview) error {
	return nil
}

func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		proc, mod, want string
		terminal, failed bool
	}{
		{"ready", "passed", "approved", true, false},
		{"ready", "approved", "approved", true, false},
		{"ready", "rejected", "rejected", true, false},
		{"failed", "", "rejected", true, true},
		{"rejected", "", "rejected", true, true},
		{"ready", "manual_review", "", false, false},
		{"ready", "pending", "", false, false},
		{"ready", "failed", "", false, false},
		{"processing", "passed", "", false, false},
		{"", "", "", false, false},
	} {
		d, terminal, failed := Decide(tc.proc, tc.mod)
		if d != tc.want || terminal != tc.terminal || failed != tc.failed {
			t.Errorf("Decide(%q,%q) = (%q,%v,%v), want (%q,%v,%v)", tc.proc, tc.mod, d, terminal, failed, tc.want, tc.terminal, tc.failed)
		}
	}
}

// The audit's acceptance: a pending post whose media is ready and passed is
// approved after one tick; a failed one is rejected and its video row marked
// failed (the creator sees "failed", not "pending"); a still-processing one
// and one younger than the threshold are left alone.
func TestSweepResolvesPendingVideoPosts(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	ready := st.add(now.Add(-time.Hour), "ready", "passed")
	broken := st.add(now.Add(-50*time.Minute), "failed", "")
	scanned := st.add(now.Add(-40*time.Minute), "ready", "rejected")
	busy := st.add(now.Add(-30*time.Minute), "processing", "pending")
	fresh := st.add(now.Add(-time.Minute), "ready", "passed")
	busted := map[uuid.UUID]bool{}
	w := NewSweeper(st, Config{MinAge: 5 * time.Minute, Batch: 10}, func(_ context.Context, id uuid.UUID) { busted[id] = true }).
		WithClock(func() time.Time { return now })

	rep := w.Tick(context.Background())
	if st.status[ready] != "approved" || st.status[broken] != "rejected" || st.status[scanned] != "rejected" {
		t.Fatalf("statuses: ready=%s broken=%s scanned=%s", st.status[ready], st.status[broken], st.status[scanned])
	}
	if st.status[busy] != "pending" || st.status[fresh] != "pending" {
		t.Fatalf("a post with no verdict, or younger than the threshold, was flipped: busy=%s fresh=%s", st.status[busy], st.status[fresh])
	}
	if !st.failed[broken] || st.failed[scanned] {
		t.Fatalf("failed marks: broken=%v scanned=%v", st.failed[broken], st.failed[scanned])
	}
	if rep.Swept != 4 || rep.Approved != 1 || rep.Rejected != 2 || rep.StillPending != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if !busted[ready] || !busted[broken] || busted[busy] {
		t.Fatalf("cache busts = %v", busted)
	}
	if want := now.Add(-5 * time.Minute); !st.lastFrom[0].Equal(want) {
		t.Fatalf("threshold = %v, want %v", st.lastFrom[0], want)
	}
}

// The batch is bounded and the sweep walks on: posts that stay pending at
// the head of the list cannot starve the ones behind them.
func TestSweepWalksPastPostsThatStayPending(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	for i := 0; i < 3; i++ {
		st.add(now.Add(-time.Duration(60-i)*time.Minute), "processing", "pending")
	}
	tail := st.add(now.Add(-10*time.Minute), "ready", "passed")
	w := NewSweeper(st, Config{MinAge: 5 * time.Minute, Batch: 2}, nil).WithClock(func() time.Time { return now })

	if rep := w.Tick(context.Background()); rep.Swept != 2 || st.status[tail] != "pending" {
		t.Fatalf("first tick: %+v tail=%s", rep, st.status[tail])
	}
	w.Tick(context.Background())
	if st.status[tail] != "approved" {
		t.Fatalf("the post behind a full batch of still-pending posts was never reached: %s", st.status[tail])
	}
	// A tick that comes back short ends the walk; the one after starts over.
	w.Tick(context.Background())
	w.Tick(context.Background())
	if st.cursors[3] != nil {
		t.Fatalf("cursor after the walk ran out = %+v, want a restart", st.cursors[3])
	}
}

// Errors are counted and the sweep carries on; nothing is flipped on a
// failed write, and the next tick retries.
func TestSweepErrorsAreRetriedNextTick(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	id := st.add(now.Add(-time.Hour), "ready", "passed")
	w := NewSweeper(st, Config{MinAge: 5 * time.Minute, Batch: 10}, nil).WithClock(func() time.Time { return now })

	st.listErr = errors.New("db down")
	if rep := w.Tick(context.Background()); rep.Swept != 0 {
		t.Fatalf("list failure: %+v", rep)
	}
	st.listErr = nil
	st.flipErr = errors.New("lock timeout")
	if rep := w.Tick(context.Background()); rep.Failed != 1 || st.status[id] != "pending" {
		t.Fatalf("flip failure: %+v status=%s", rep, st.status[id])
	}
	st.flipErr = nil
	if w.Tick(context.Background()); st.status[id] != "approved" {
		t.Fatalf("after recovery: %s", st.status[id])
	}
}

// CreatePost's second look: a pending video post whose media is now ready is
// approved at once; a post that is not pending is left alone.
func TestReconcileOne(t *testing.T) {
	st := newFakeStore()
	id := st.add(time.Now(), "ready", "passed")
	if out := ReconcileOne(context.Background(), st, id, nil); out != Approved || st.status[id] != "approved" {
		t.Fatalf("out=%v status=%s", out, st.status[id])
	}
	if out := ReconcileOne(context.Background(), st, id, nil); out != StillPending {
		t.Fatalf("a decided post was judged again: %v", out)
	}
}
