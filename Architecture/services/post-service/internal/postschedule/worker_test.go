package postschedule

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeStore models the posts table for the worker: a publish_at per post
// (nil = live) and a deleted flag. Publish is the same compare-and-set the
// SQL performs: it only succeeds while publish_at is still set, so a
// second, concurrent run against the same row is a no-op.
type fakeStore struct {
	mu        sync.Mutex
	publishAt map[uuid.UUID]*time.Time
	deleted   map[uuid.UUID]bool
	published []uuid.UUID
	listErr   error
	// mediaFailed: scheduled videos whose media failed (REG-6); rejected
	// and notified record what ResolveFailedScheduledVideo did.
	mediaFailed map[uuid.UUID]bool
	rejected    map[uuid.UUID]bool
	notified    []uuid.UUID
}

func (f *fakeStore) ListFailedScheduledVideos(_ context.Context, limit int) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []uuid.UUID
	for id := range f.mediaFailed {
		if at := f.publishAt[id]; at != nil && !f.deleted[id] {
			out = append(out, id)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ResolveFailedScheduledVideo models the guarded UPDATE: only a still
// scheduled, failed, live post moves, and the notice is written with it.
func (f *fakeStore) ResolveFailedScheduledVideo(_ context.Context, postID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if at := f.publishAt[postID]; at == nil || f.deleted[postID] || !f.mediaFailed[postID] {
		return false, nil
	}
	f.publishAt[postID] = nil
	if f.rejected == nil {
		f.rejected = map[uuid.UUID]bool{}
	}
	f.rejected[postID] = true
	f.notified = append(f.notified, postID)
	return true, nil
}

// failMedia marks a scheduled post's video as terminally failed. The fake's
// ListDueScheduledPosts still skips nothing for it, so the test also proves
// the resolve runs BEFORE the publish pass.
func (f *fakeStore) failMedia(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mediaFailed == nil {
		f.mediaFailed = map[uuid.UUID]bool{}
	}
	f.mediaFailed[id] = true
}

func newFakeStore() *fakeStore {
	return &fakeStore{publishAt: map[uuid.UUID]*time.Time{}, deleted: map[uuid.UUID]bool{}}
}

func (f *fakeStore) schedule(at time.Time) uuid.UUID {
	id := uuid.New()
	f.mu.Lock()
	f.publishAt[id] = &at
	f.mu.Unlock()
	return id
}

func (f *fakeStore) ListDueScheduledPosts(_ context.Context, now time.Time, limit int) ([]postgres.ScheduledCandidate, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.ScheduledCandidate
	for id, at := range f.publishAt {
		if at != nil && !at.After(now) && !f.deleted[id] {
			out = append(out, postgres.ScheduledCandidate{PostID: id, PublishAt: *at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishAt.Before(out[j].PublishAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PublishScheduled is the service half, modelled on store.PublishScheduledPost:
// a guarded flip. dueOnly refuses a future publish_at.
func (f *fakeStore) PublishScheduled(_ context.Context, postID uuid.UUID, _ *uuid.UUID, dueOnly bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, ok := f.publishAt[postID]
	if !ok || at == nil || f.deleted[postID] {
		return false, nil
	}
	if dueOnly && at.After(time.Now()) {
		return false, nil
	}
	f.publishAt[postID] = nil
	f.published = append(f.published, postID)
	return true, nil
}

type failingPublisher struct{ err error }

func (p failingPublisher) PublishScheduled(context.Context, uuid.UUID, *uuid.UUID, bool) (bool, error) {
	return false, p.err
}

func TestTickPublishesDueAndLeavesFuture(t *testing.T) {
	st := newFakeStore()
	now := time.Now()
	due := st.schedule(now.Add(-time.Minute))
	later := st.schedule(now.Add(time.Hour))

	w := NewWorker(st, st, Config{Interval: time.Second}, nil)
	rep := w.Tick(context.Background())
	if rep.Published != 1 || rep.Skipped != 0 || rep.Failed != 0 {
		t.Fatalf("report %+v", rep)
	}
	if st.publishAt[due] != nil {
		t.Fatal("due post must be live")
	}
	if st.publishAt[later] == nil {
		t.Fatal("future post must stay scheduled")
	}

	// A second tick finds nothing: publishing cleared publish_at.
	if rep := w.Tick(context.Background()); rep.Published != 0 {
		t.Fatalf("second tick republished: %+v", rep)
	}
	if len(st.published) != 1 {
		t.Fatalf("published %d times, want exactly once", len(st.published))
	}
}

// Exactly once under a concurrent run: N workers tick the same store at the
// same moment; every due post is published exactly one time.
func TestConcurrentTicksPublishExactlyOnce(t *testing.T) {
	st := newFakeStore()
	now := time.Now()
	var ids []uuid.UUID
	for i := 0; i < 25; i++ {
		ids = append(ids, st.schedule(now.Add(-time.Duration(i+1)*time.Second)))
	}

	const replicas = 8
	var wg sync.WaitGroup
	reports := make([]TickReport, replicas)
	for r := 0; r < replicas; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			reports[r] = NewWorker(st, st, Config{}, nil).Tick(context.Background())
		}(r)
	}
	wg.Wait()

	total := 0
	for _, rep := range reports {
		total += rep.Published
		if rep.Failed != 0 {
			t.Fatalf("a tick failed: %+v", rep)
		}
	}
	if total != len(ids) {
		t.Fatalf("published %d across replicas, want %d", total, len(ids))
	}
	seen := map[uuid.UUID]int{}
	for _, id := range st.published {
		seen[id]++
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("post %s published %d times", id, seen[id])
		}
	}
}

// A post that was deleted while scheduled is never published.
func TestTickSkipsDeleted(t *testing.T) {
	st := newFakeStore()
	id := st.schedule(time.Now().Add(-time.Minute))
	st.deleted[id] = true
	rep := NewWorker(st, st, Config{}, nil).Tick(context.Background())
	if rep.Published != 0 || len(st.published) != 0 {
		t.Fatalf("deleted post published: %+v", rep)
	}
}

// A publish error is counted and does not stop the tick.
func TestTickCountsFailures(t *testing.T) {
	st := newFakeStore()
	st.schedule(time.Now().Add(-time.Minute))
	st.schedule(time.Now().Add(-2 * time.Minute))
	rep := NewWorker(st, failingPublisher{errors.New("boom")}, Config{}, nil).Tick(context.Background())
	if rep.Failed != 2 || rep.Published != 0 {
		t.Fatalf("report %+v", rep)
	}
	// Still scheduled: the next tick retries.
	for _, at := range st.publishAt {
		if at == nil {
			t.Fatal("a failed publish must leave the post scheduled")
		}
	}
}

func TestTickListFailureIsNotFatal(t *testing.T) {
	st := newFakeStore()
	st.listErr = errors.New("db down")
	rep := NewWorker(st, st, Config{}, nil).Tick(context.Background())
	if rep != (TickReport{}) {
		t.Fatalf("report %+v", rep)
	}
}

// REG-6: a scheduled video whose media failed for good leaves the schedule
// for the rejected state, its creator is told once, and it is never
// published — whether its slot has passed or not. A healthy scheduled post
// beside it is untouched.
func TestTickTakesAFailedMediaVideoOffTheScheduleAndTellsTheCreator(t *testing.T) {
	st := newFakeStore()
	now := time.Now()
	failedDue := st.schedule(now.Add(-time.Minute))
	failedLater := st.schedule(now.Add(time.Hour))
	healthy := st.schedule(now.Add(time.Hour))
	st.failMedia(failedDue)
	st.failMedia(failedLater)

	w := NewWorker(st, st, Config{}, nil)
	rep := w.Tick(context.Background())
	if rep.MediaFailed != 2 || rep.Published != 0 || rep.Failed != 0 {
		t.Fatalf("report %+v, want 2 media-failed and nothing published", rep)
	}
	for _, id := range []uuid.UUID{failedDue, failedLater} {
		if st.publishAt[id] != nil {
			t.Fatalf("failed-media post %s is still scheduled", id)
		}
		if !st.rejected[id] {
			t.Fatalf("failed-media post %s was not rejected", id)
		}
	}
	for _, id := range st.published {
		if id == failedDue || id == failedLater {
			t.Fatal("a failed-media video was published")
		}
	}
	if st.publishAt[healthy] == nil || st.rejected[healthy] {
		t.Fatal("a healthy scheduled post was touched")
	}
	if len(st.notified) != 2 {
		t.Fatalf("notified %d times, want once per failed video", len(st.notified))
	}
	// A second tick finds nothing left to resolve and tells nobody again.
	if rep := w.Tick(context.Background()); rep.MediaFailed != 0 || len(st.notified) != 2 {
		t.Fatalf("second tick re-resolved: %+v, notices %d", rep, len(st.notified))
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Setenv("POST_SCHEDULE_INTERVAL", "")
	if cfg := ConfigFromEnv(); cfg.Interval != 30*time.Second || cfg.Batch != 100 {
		t.Fatalf("defaults %+v", cfg)
	}
	t.Setenv("POST_SCHEDULE_INTERVAL", "5s")
	if cfg := ConfigFromEnv(); cfg.Interval != 5*time.Second {
		t.Fatalf("env %+v", cfg)
	}
	t.Setenv("POST_SCHEDULE_INTERVAL", "nonsense")
	if cfg := ConfigFromEnv(); cfg.Interval != 30*time.Second {
		t.Fatalf("bad env must keep the default: %+v", cfg)
	}
}
