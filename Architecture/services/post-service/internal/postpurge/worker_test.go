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
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		deletedAt: map[uuid.UUID]*time.Time{},
		media:     map[uuid.UUID][]uuid.UUID{},
		queue:     map[[2]uuid.UUID]*postgres.PurgeMediaItem{},
		deferred:  map[[2]uuid.UUID]int{},
	}
}

func (f *fakeStore) ListPurgeablePosts(_ context.Context, before time.Time, limit int) ([]postgres.PurgeCandidate, error) {
	var out []postgres.PurgeCandidate
	for id, d := range f.deletedAt {
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

func (f *fakeStore) PurgePost(_ context.Context, postID uuid.UUID) ([]postgres.PurgeMediaItem, error) {
	d, ok := f.deletedAt[postID]
	if !ok || d == nil {
		return nil, nil // gone or restored
	}
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
