package service

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeLikedStore is the reactions table as the liked list sees it: one row
// per (viewer, post), the first like's time kept on a repeat, the keyset
// read newest first. The short-form / live filter is SQL and is proved in
// liked_reels_integration_test.go.
type fakeLikedStore struct {
	mu    sync.Mutex
	rows  map[uuid.UUID]map[uuid.UUID]time.Time // viewer → post → liked at
	now   time.Time
	reads []int // the limit each read asked for
	err   error
}

func newFakeLikedStore() *fakeLikedStore {
	return &fakeLikedStore{rows: map[uuid.UUID]map[uuid.UUID]time.Time{}, now: time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)}
}

func (f *fakeLikedStore) UpsertPostReaction(_ context.Context, postID, userID uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.rows[userID] == nil {
		f.rows[userID] = map[uuid.UUID]time.Time{}
	}
	if _, ok := f.rows[userID][postID]; !ok {
		f.now = f.now.Add(time.Second)
		f.rows[userID][postID] = f.now
	}
	return nil
}

func (f *fakeLikedStore) DeletePostReaction(_ context.Context, postID, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.rows[userID], postID)
	return nil
}

func (f *fakeLikedStore) ListLikedShortFormPosts(_ context.Context, userID uuid.UUID, limit int, after *postgres.LikedCursor) ([]postgres.LikedPost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, limit)
	if f.err != nil {
		return nil, f.err
	}
	var all []postgres.LikedPost
	for id, at := range f.rows[userID] {
		all = append(all, postgres.LikedPost{PostID: id, LikedAt: at})
	}
	less := func(a, b postgres.LikedPost) bool { // a before b: newer, then larger id
		if !a.LikedAt.Equal(b.LikedAt) {
			return a.LikedAt.After(b.LikedAt)
		}
		return a.PostID.String() > b.PostID.String()
	}
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })
	var out []postgres.LikedPost
	for _, r := range all {
		if after != nil && !less(postgres.LikedPost{PostID: after.PostID, LikedAt: after.LikedAt}, r) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// likedRig likes n posts in order (posts[0] first, so newest last) and
// lets the read gate refuse a chosen set.
type likedRig struct {
	svc    *Service
	store  *fakeLikedStore
	viewer uuid.UUID
	posts  []uuid.UUID
	deny   map[uuid.UUID]error
}

func newLikedRig(t *testing.T, n int) *likedRig {
	t.Helper()
	r := &likedRig{store: newFakeLikedStore(), viewer: uuid.New(), deny: map[uuid.UUID]error{}}
	r.svc = NewForHandlerTests(HandlerTestDeps{
		LikedReels: r.store,
		ReadGate: func(_ context.Context, postID uuid.UUID, viewerID *uuid.UUID) error {
			if viewerID == nil || *viewerID != r.viewer {
				t.Errorf("read gate asked for viewer %v, want %s", viewerID, r.viewer)
			}
			return r.deny[postID]
		},
	})
	for i := 0; i < n; i++ {
		id := uuid.New()
		r.posts = append(r.posts, id)
		if err := r.svc.indexReaction(context.Background(), id, r.viewer, "like", true); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// newest returns the liked posts at the given positions, counted from the
// newest like (0 = the last one liked).
func (r *likedRig) newest(pos ...int) []string {
	out := make([]string, len(pos))
	for i, p := range pos {
		out[i] = r.posts[len(r.posts)-1-p].String()
	}
	return out
}

func (r *likedRig) page(t *testing.T, limit int, cursor string) ([]string, string) {
	t.Helper()
	ids, next, err := r.svc.ListLikedReels(context.Background(), r.viewer, limit, cursor)
	if err != nil {
		t.Fatalf("ListLikedReels(limit %d, cursor %q): %v", limit, cursor, err)
	}
	return ids, next
}

func TestNormalizeLikedReelsLimit(t *testing.T) {
	for raw, want := range map[string]int{
		"": 50, "abc": 50, "0": 50, "-3": 50, "1": 1, "50": 50, "100": 100, "101": 100, "5000": 100,
	} {
		if got := NormalizeLikedReelsLimit(raw); got != want {
			t.Errorf("NormalizeLikedReelsLimit(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestListLikedReelsPagesNewestFirstByCursor(t *testing.T) {
	r := newLikedRig(t, 5)

	ids, next := r.page(t, 2, "")
	if want := r.newest(0, 1); !reflect.DeepEqual(ids, want) || next == "" {
		t.Fatalf("page 1 = %v next %q, want %v and a cursor", ids, next, want)
	}
	ids, next = r.page(t, 2, next)
	if want := r.newest(2, 3); !reflect.DeepEqual(ids, want) || next == "" {
		t.Fatalf("page 2 = %v next %q, want %v and a cursor", ids, next, want)
	}
	ids, next = r.page(t, 2, next)
	if want := r.newest(4); !reflect.DeepEqual(ids, want) || next != "" {
		t.Fatalf("page 3 = %v next %q, want %v and no cursor", ids, next, want)
	}
}

func TestListLikedReelsExactPageEndsWithoutCursor(t *testing.T) {
	r := newLikedRig(t, 2)
	ids, next := r.page(t, 2, "")
	if want := r.newest(0, 1); !reflect.DeepEqual(ids, want) || next != "" {
		t.Fatalf("page = %v next %q, want %v and no cursor", ids, next, want)
	}
}

func TestListLikedReelsDropsWhatTheReadGateRefuses(t *testing.T) {
	r := newLikedRig(t, 6)
	// The three newest are now private, deleted for the viewer's purposes,
	// or behind a graph outage: the gate's error of any kind drops them.
	r.deny[r.posts[5]] = ErrPostNotVisible
	r.deny[r.posts[4]] = ErrPostNotFound
	r.deny[r.posts[3]] = errors.New("graph-service timeout")

	ids, next := r.page(t, 2, "")
	if want := r.newest(3, 4); !reflect.DeepEqual(ids, want) || next == "" {
		t.Fatalf("page 1 = %v next %q, want %v (filled from a second read) and a cursor", ids, next, want)
	}
	ids, next = r.page(t, 2, next)
	if want := r.newest(5); !reflect.DeepEqual(ids, want) || next != "" {
		t.Fatalf("page 2 = %v next %q, want %v and no cursor", ids, next, want)
	}
}

func TestListLikedReelsShortPageKeepsTheCursorWhenOutOfRounds(t *testing.T) {
	r := newLikedRig(t, 30)
	for _, id := range r.posts[1:] {
		r.deny[id] = ErrPostNotVisible // only the oldest like is still visible
	}

	ids, next := r.page(t, 2, "")
	if len(ids) != 0 || next == "" {
		t.Fatalf("page 1 = %v next %q, want an empty page that still has a cursor", ids, next)
	}
	if got := len(r.store.reads); got != likedReelsMaxRounds {
		t.Fatalf("store reads = %d, want the round cap %d", got, likedReelsMaxRounds)
	}
	var all []string
	for pages := 0; next != "" && pages < 10; pages++ {
		ids, next = r.page(t, 2, next)
		all = append(all, ids...)
	}
	if want := r.newest(29); !reflect.DeepEqual(all, want) || next != "" {
		t.Fatalf("following the cursor gave %v (next %q), want %v", all, next, want)
	}
}

func TestListLikedReelsUnlikeRemovesAndRelikeKeepsItsPlace(t *testing.T) {
	r := newLikedRig(t, 3)
	ctx := context.Background()

	if err := r.svc.indexReaction(ctx, r.posts[1], r.viewer, "", false); err != nil {
		t.Fatal(err)
	}
	ids, _ := r.page(t, 50, "")
	if want := r.newest(0, 2); !reflect.DeepEqual(ids, want) {
		t.Fatalf("after unlike = %v, want %v", ids, want)
	}
	// A repeat like of a post already liked does not move it to the top.
	if err := r.svc.indexReaction(ctx, r.posts[0], r.viewer, "love", true); err != nil {
		t.Fatal(err)
	}
	ids, _ = r.page(t, 50, "")
	if want := r.newest(0, 2); !reflect.DeepEqual(ids, want) {
		t.Fatalf("after a repeat like = %v, want %v", ids, want)
	}
}

func TestListLikedReelsIsTheViewersOwn(t *testing.T) {
	r := newLikedRig(t, 2)
	other := uuid.New()
	ids, next, err := r.svc.ListLikedReels(context.Background(), other, 50, "")
	if err != nil || len(ids) != 0 || next != "" {
		t.Fatalf("another viewer's list = (%v, %q, %v), want empty", ids, next, err)
	}
}

func TestListLikedReelsRefusesABadCursorAndFailsClosedUnwired(t *testing.T) {
	r := newLikedRig(t, 1)
	if _, _, err := r.svc.ListLikedReels(context.Background(), r.viewer, 10, "garbage"); !errors.Is(err, ErrInvalidLikedCursor) {
		t.Fatalf("bad cursor err = %v, want ErrInvalidLikedCursor", err)
	}
	if len(r.store.reads) != 0 {
		t.Fatalf("a bad cursor read the store %d times; it must be refused first", len(r.store.reads))
	}

	unwired := NewForHandlerTests(HandlerTestDeps{})
	if _, _, err := unwired.ListLikedReels(context.Background(), r.viewer, 10, ""); !errors.Is(err, ErrAuthoringStoreUnavailable) {
		t.Fatalf("unwired err = %v, want ErrAuthoringStoreUnavailable", err)
	}
	// No index store: the like paths' index write is a no-op, not a panic.
	if err := unwired.indexReaction(context.Background(), uuid.New(), r.viewer, "like", true); err != nil {
		t.Fatalf("unwired index write = %v, want nil", err)
	}
}

func TestListLikedReelsPassesStoreErrors(t *testing.T) {
	r := newLikedRig(t, 1)
	r.store.err = errors.New("db down")
	if _, _, err := r.svc.ListLikedReels(context.Background(), r.viewer, 10, ""); err == nil {
		t.Fatal("a store error was swallowed")
	}
	if err := r.svc.indexReaction(context.Background(), r.posts[0], r.viewer, "", false); err == nil {
		t.Fatal("an index write error was swallowed; the idempotent like routes must return it")
	}
}

func TestReelReactPathsWriteTheLikedIndexFirst(t *testing.T) {
	store := newFakeLikedStore()
	store.err = errors.New("db down")
	svc := NewForHandlerTests(HandlerTestDeps{LikedReels: store})
	// scyllaStore is nil: reaching it would panic, so an error here proves
	// the index write runs first and stops the call.
	if err := svc.ReactToReel(context.Background(), uuid.New(), uuid.New(), "like"); err == nil {
		t.Fatal("ReactToReel ignored the index write's failure")
	}
	if err := svc.UnreactToReel(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("UnreactToReel ignored the index write's failure")
	}
}
