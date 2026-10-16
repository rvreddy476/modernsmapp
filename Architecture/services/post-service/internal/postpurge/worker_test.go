package postpurge

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeStore models just enough of Postgres for the worker: posts with a
// deleted_at, post_media rows, and the post_purge_media queue. PurgePost
// applies the same "unreferenced by any OTHER post" rule as the SQL.
type fakeStore struct {
	deletedAt map[uuid.UUID]*time.Time  // post → deleted_at (nil = live)
	media     map[uuid.UUID][]uuid.UUID // post → media ids
	queue     map[[2]uuid.UUID]*postgres.PurgeMediaItem
	deferred  map[[2]uuid.UUID]int
	purged    []uuid.UUID

	// held: posts under an active restriction. heldAfterScan: held, but the
	// scan does not know yet (a hold placed between scan and lock).
	held          map[uuid.UUID]bool
	heldAfterScan map[uuid.UUID]bool
	// failPurge: post → remaining PurgePost failures. backoff: post → the
	// next attempt time DeferPurgePost set; attempts: its attempt count.
	failPurge  map[uuid.UUID]int
	backoff    map[uuid.UUID]time.Time
	attempts   map[uuid.UUID]int
	purgeCalls map[uuid.UUID]int
	now        func() time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		deletedAt:     map[uuid.UUID]*time.Time{},
		media:         map[uuid.UUID][]uuid.UUID{},
		queue:         map[[2]uuid.UUID]*postgres.PurgeMediaItem{},
		deferred:      map[[2]uuid.UUID]int{},
		held:          map[uuid.UUID]bool{},
		heldAfterScan: map[uuid.UUID]bool{},
		failPurge:     map[uuid.UUID]int{},
		backoff:       map[uuid.UUID]time.Time{},
		attempts:      map[uuid.UUID]int{},
		purgeCalls:    map[uuid.UUID]int{},
		now:           func() time.Time { return time.Now().UTC() },
	}
}

func (f *fakeStore) ListPurgeablePosts(_ context.Context, before time.Time, limit int) ([]postgres.PurgeCandidate, error) {
	var out []postgres.PurgeCandidate
	for id, d := range f.deletedAt {
		if f.held[id] {
			continue
		}
		if next, ok := f.backoff[id]; ok && next.After(f.now()) {
			continue
		}
		if d != nil && !d.After(before) {
			out = append(out, postgres.PurgeCandidate{PostID: id, DeletedAt: *d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.Before(out[j].DeletedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) CountHeldPurgeablePosts(_ context.Context, before time.Time) (int, error) {
	n := 0
	for id := range f.held {
		if d := f.deletedAt[id]; d != nil && !d.After(before) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) DeferPurgePost(_ context.Context, postID uuid.UUID, _ string) (int, time.Time, error) {
	f.attempts[postID]++
	next := f.now().Add(time.Duration(1<<f.attempts[postID]) * time.Minute)
	f.backoff[postID] = next
	return f.attempts[postID], next, nil
}

func (f *fakeStore) PurgePost(_ context.Context, postID uuid.UUID) ([]postgres.PurgeMediaItem, error) {
	f.purgeCalls[postID]++
	d, ok := f.deletedAt[postID]
	if !ok || d == nil {
		return nil, nil // gone or restored
	}
	if f.held[postID] || f.heldAfterScan[postID] {
		return nil, postgres.ErrPurgeHeld
	}
	if f.failPurge[postID] > 0 {
		f.failPurge[postID]--
		return nil, errors.New(`ERROR: update or delete on table "posts" violates foreign key constraint (SQLSTATE 23503)`)
	}
	delete(f.backoff, postID)
	var queued []postgres.PurgeMediaItem
	for _, m := range f.media[postID] {
		referencedElsewhere := false
		for other, ms := range f.media {
			if other == postID {
				continue
			}
			for _, om := range ms {
				if om == m {
					referencedElsewhere = true
				}
			}
		}
		if !referencedElsewhere {
			it := postgres.PurgeMediaItem{MediaID: m, PostID: postID}
			f.queue[[2]uuid.UUID{m, postID}] = &it
			queued = append(queued, it)
		}
	}
	delete(f.media, postID)
	delete(f.deletedAt, postID)
	f.purged = append(f.purged, postID)
	return queued, nil
}

func (f *fakeStore) PendingPurgeMedia(_ context.Context, limit int) ([]postgres.PurgeMediaItem, error) {
	var out []postgres.PurgeMediaItem
	for _, it := range f.queue {
		out = append(out, *it)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) ResolvePurgeMedia(_ context.Context, mediaID, postID uuid.UUID) error {
	delete(f.queue, [2]uuid.UUID{mediaID, postID})
	return nil
}

func (f *fakeStore) DeferPurgeMedia(_ context.Context, mediaID, postID uuid.UUID, _ string) error {
	k := [2]uuid.UUID{mediaID, postID}
	f.deferred[k]++
	if it := f.queue[k]; it != nil {
		it.Attempts++
	}
	return nil
}

type fakeMedia struct {
	deleted []uuid.UUID
	failFor map[uuid.UUID]int // media → remaining failures
}

func (m *fakeMedia) DeleteMediaForPurgedPost(_ context.Context, mediaID, _ uuid.UUID) error {
	if m.failFor[mediaID] > 0 {
		m.failFor[mediaID]--
		return errors.New("media-service unavailable")
	}
	m.deleted = append(m.deleted, mediaID)
	return nil
}

func TestTick_PurgesOnlyPastWindowAndDeletesOnlyUnreferencedMedia(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	md := &fakeMedia{failFor: map[uuid.UUID]int{}}

	old := uuid.New()    // deleted 31 days ago → purge
	recent := uuid.New() // deleted 1 hour ago → keep (restorable)
	live := uuid.New()   // never deleted
	shared := uuid.New() // referenced by old AND live
	alone := uuid.New()  // referenced by old only
	recentOnly := uuid.New()

	oldAt := now.Add(-31 * 24 * time.Hour)
	recentAt := now.Add(-time.Hour)
	st.deletedAt[old] = &oldAt
	st.deletedAt[recent] = &recentAt
	st.deletedAt[live] = nil
	st.media[old] = []uuid.UUID{shared, alone}
	st.media[live] = []uuid.UUID{shared}
	st.media[recent] = []uuid.UUID{recentOnly}

	w := NewWorker(st, md, Config{After: 30 * 24 * time.Hour, Interval: time.Minute}, nil).
		WithClock(func() time.Time { return now })
	rep := w.Tick(context.Background())

	if rep.Purged != 1 || len(st.purged) != 1 || st.purged[0] != old {
		t.Fatalf("purged = %v (report %+v), want only %s", st.purged, rep, old)
	}
	if _, stillThere := st.deletedAt[recent]; !stillThere {
		t.Fatal("a post inside the restore window was purged")
	}
	if _, stillThere := st.deletedAt[live]; !stillThere {
		t.Fatal("a live post was purged")
	}
	if len(md.deleted) != 1 || md.deleted[0] != alone {
		t.Fatalf("media deleted = %v, want only the unreferenced asset %s (shared %s must survive)", md.deleted, alone, shared)
	}
	if rep.MediaQueued != 1 || rep.MediaDeleted != 1 {
		t.Fatalf("report %+v: want 1 queued, 1 deleted", rep)
	}
	if len(st.queue) != 0 {
		t.Fatalf("queue should be drained, has %d rows", len(st.queue))
	}
}

func TestTick_MediaFailureIsDeferredAndRetriedNextTick(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	post := uuid.New()
	media := uuid.New()
	at := now.Add(-48 * time.Hour)
	st.deletedAt[post] = &at
	st.media[post] = []uuid.UUID{media}
	md := &fakeMedia{failFor: map[uuid.UUID]int{media: 1}}

	w := NewWorker(st, md, Config{After: time.Hour, Interval: time.Minute}, nil).
		WithClock(func() time.Time { return now })

	rep := w.Tick(context.Background())
	if rep.Purged != 1 || rep.MediaDeferred != 1 || rep.MediaDeleted != 0 {
		t.Fatalf("first tick %+v: want purge + one deferred media", rep)
	}
	if st.deferred[[2]uuid.UUID{media, post}] != 1 {
		t.Fatal("failed media call was not recorded as deferred")
	}
	if len(st.queue) != 1 {
		t.Fatal("queue row must survive a failed media-service call")
	}

	rep = w.Tick(context.Background())
	if rep.MediaDeleted != 1 || len(md.deleted) != 1 || md.deleted[0] != media {
		t.Fatalf("second tick %+v deleted=%v: the deferred row must be retried", rep, md.deleted)
	}
	if len(st.queue) != 0 {
		t.Fatal("resolved row must leave the queue")
	}
	// The post itself must not be "purged" twice.
	if len(st.purged) != 1 {
		t.Fatalf("post purged %d times, want 1", len(st.purged))
	}
}

// A post a case holds is evidence: past the window it is skipped (and
// counted), never purged; the first tick after the hold is released purges
// it like any other.
func TestTick_HeldPostIsSkippedUntilReleased(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.now = func() time.Time { return now }
	heldPost, freePost := uuid.New(), uuid.New()
	at := now.Add(-48 * time.Hour)
	st.deletedAt[heldPost] = &at
	st.deletedAt[freePost] = &at
	st.held[heldPost] = true

	w := NewWorker(st, &fakeMedia{failFor: map[uuid.UUID]int{}}, Config{After: time.Hour, Interval: time.Minute}, nil).
		WithClock(func() time.Time { return now })

	rep := w.Tick(context.Background())
	if rep.Purged != 1 || rep.Held != 1 || rep.Failed != 0 {
		t.Fatalf("tick %+v: want 1 purged, 1 held, 0 failed", rep)
	}
	if _, stillThere := st.deletedAt[heldPost]; !stillThere {
		t.Fatal("a held post was purged")
	}
	if st.purgeCalls[heldPost] != 0 {
		t.Fatalf("held post reached PurgePost %d times; the scan must skip it", st.purgeCalls[heldPost])
	}

	delete(st.held, heldPost) // the case releases its hold
	rep = w.Tick(context.Background())
	if rep.Purged != 1 || rep.Held != 0 {
		t.Fatalf("tick after release %+v: want the post purged and nothing held", rep)
	}
	if _, stillThere := st.deletedAt[heldPost]; stillThere {
		t.Fatal("a released post was not purged")
	}
}

// A hold placed between the scan and PurgePost's lock comes back as
// ErrPurgeHeld: skipped, not a failure, no back-off row.
func TestTick_HoldRaceIsASkipNotAFailure(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.now = func() time.Time { return now }
	post := uuid.New()
	at := now.Add(-48 * time.Hour)
	st.deletedAt[post] = &at
	st.heldAfterScan[post] = true

	w := NewWorker(st, &fakeMedia{failFor: map[uuid.UUID]int{}}, Config{After: time.Hour, Interval: time.Minute}, nil).
		WithClock(func() time.Time { return now })
	rep := w.Tick(context.Background())
	if rep.Purged != 0 || rep.Failed != 0 {
		t.Fatalf("tick %+v: want nothing purged and nothing failed", rep)
	}
	if st.attempts[post] != 0 {
		t.Fatal("a held post was recorded as a purge failure")
	}
	if _, stillThere := st.deletedAt[post]; !stillThere {
		t.Fatal("a held post was purged")
	}
}

// A post whose purge fails is backed off, not retried on every tick, and
// purged once the cause is gone and its back-off has elapsed.
func TestTick_FailedPurgeBacksOff(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st := newFakeStore()
	st.now = clock
	bad, good := uuid.New(), uuid.New()
	at := now.Add(-48 * time.Hour)
	st.deletedAt[bad] = &at
	st.deletedAt[good] = &at
	st.failPurge[bad] = 2

	w := NewWorker(st, &fakeMedia{failFor: map[uuid.UUID]int{}}, Config{After: time.Hour, Interval: 30 * time.Second}, nil).
		WithClock(clock)

	rep := w.Tick(context.Background())
	if rep.Failed != 1 || rep.Purged != 1 {
		t.Fatalf("first tick %+v: want the good post purged and the bad one failed", rep)
	}
	if st.attempts[bad] != 1 {
		t.Fatalf("failure recorded %d times, want 1", st.attempts[bad])
	}

	// Ten more 30 s ticks inside the back-off: no retry.
	for i := 0; i < 10; i++ {
		now = now.Add(5 * time.Second)
		w.Tick(context.Background())
	}
	if st.purgeCalls[bad] != 1 {
		t.Fatalf("bad post retried %d times inside its back-off, want 1 call in total", st.purgeCalls[bad])
	}

	now = now.Add(time.Hour) // past the first back-off; fails a second time
	if rep := w.Tick(context.Background()); rep.Failed != 1 || st.attempts[bad] != 2 {
		t.Fatalf("second attempt %+v attempts=%d: want a second recorded failure", rep, st.attempts[bad])
	}
	now = now.Add(time.Hour) // the cause is gone now
	if rep := w.Tick(context.Background()); rep.Purged != 1 {
		t.Fatalf("third attempt %+v: want the post purged", rep)
	}
	if _, stillThere := st.deletedAt[bad]; stillThere {
		t.Fatal("bad post never purged")
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("POST_PURGE_AFTER", "")
	t.Setenv("POST_PURGE_INTERVAL", "")
	cfg := ConfigFromEnv()
	if cfg.After != 720*time.Hour || cfg.Interval != 5*time.Minute {
		t.Fatalf("defaults = %+v, want 720h / 5m", cfg)
	}
	t.Setenv("POST_PURGE_AFTER", "2m")
	t.Setenv("POST_PURGE_INTERVAL", "30s")
	cfg = ConfigFromEnv()
	if cfg.After != 2*time.Minute || cfg.Interval != 30*time.Second {
		t.Fatalf("env = %+v, want 2m / 30s", cfg)
	}
}
