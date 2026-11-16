package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// feed-14 / feed-49: POST /v1/posts/:postId/like (Service.ToggleLike).
//
// The stored reaction is a fake that answers the way Scylla's conditional
// writes do (the first writer changes the row and is told so, everyone else
// is told they changed nothing); Redis is miniredis, so the Lua script and
// the 24 h TTL are the real ones; the outbox, the engagement topic and the
// tune row are recorded in memory.

// fakeHearts is reactions_by_post + post_counters.
type fakeHearts struct {
	mu    sync.Mutex
	rows  map[string]string // post|user -> reaction
	likes map[uuid.UUID]int64

	// readGate, when set, holds every GetReaction until it is closed, so a
	// burst of taps all read the same stored state before any of them writes.
	readGate chan struct{}
	reads    sync.WaitGroup

	reactErr     error // returned with changed = false
	counterErr   error // returned with changed = true (row written, counter failed)
	reactCalls   int
	unreactCalls int
}

func newFakeHearts() *fakeHearts {
	return &fakeHearts{rows: map[string]string{}, likes: map[uuid.UUID]int64{}}
}

func heartKey(post, user uuid.UUID) string { return post.String() + "|" + user.String() }

func (f *fakeHearts) GetReaction(_ context.Context, post, user uuid.UUID) (string, error) {
	f.mu.Lock()
	r := f.rows[heartKey(post, user)]
	gate := f.readGate
	f.mu.Unlock()
	if gate != nil {
		f.reads.Done()
		<-gate
	}
	return r, nil
}

// React is INSERT … IF NOT EXISTS: only the write that created the row
// reports true and moves the count.
func (f *fakeHearts) React(_ context.Context, post, user uuid.UUID, reaction string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactCalls++
	if f.reactErr != nil {
		return false, f.reactErr
	}
	k := heartKey(post, user)
	if _, exists := f.rows[k]; exists {
		f.rows[k] = reaction
		return false, nil
	}
	f.rows[k] = reaction
	if f.counterErr != nil {
		return true, f.counterErr
	}
	f.likes[post]++
	return true, nil
}

// Unreact is DELETE … IF EXISTS.
func (f *fakeHearts) Unreact(_ context.Context, post, user uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unreactCalls++
	k := heartKey(post, user)
	if _, exists := f.rows[k]; !exists {
		return false, nil
	}
	delete(f.rows, k)
	f.likes[post]--
	return true, nil
}

func (f *fakeHearts) GetCounts(_ context.Context, post uuid.UUID) (*scylla.Counts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &scylla.Counts{Likes: f.likes[post]}, nil
}

func (f *fakeHearts) BatchGetCounts(context.Context, []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error) {
	return nil, nil
}

func (f *fakeHearts) BatchGetReactions(context.Context, []uuid.UUID, uuid.UUID) (map[uuid.UUID]string, error) {
	return nil, nil
}

func (f *fakeHearts) stored(post, user uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[heartKey(post, user)]
}

func (f *fakeHearts) count(post uuid.UUID) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.likes[post]
}

func (f *fakeHearts) calls() (reacts, unreacts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reactCalls, f.unreactCalls
}

// fakeLikeIndex is the liked list's Postgres record.
type fakeLikeIndex struct {
	mu      sync.Mutex
	rows    map[string]bool
	failing error
}

func (f *fakeLikeIndex) UpsertPostReaction(_ context.Context, post, user uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing != nil {
		return f.failing
	}
	f.rows[heartKey(post, user)] = true
	return nil
}

func (f *fakeLikeIndex) DeletePostReaction(_ context.Context, post, user uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing != nil {
		return f.failing
	}
	delete(f.rows, heartKey(post, user))
	return nil
}

func (f *fakeLikeIndex) ListLikedShortFormPosts(context.Context, uuid.UUID, int, *postgres.LikedCursor) ([]postgres.LikedPost, error) {
	return nil, nil
}

func (f *fakeLikeIndex) has(post, user uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[heartKey(post, user)]
}

// likeLedger records what a like announced.
type likeLedger struct {
	mu          sync.Mutex
	outbox      map[string]int // event type -> rows
	engagement  map[string]int // engagement event type -> events
	tunesClear  int
	lastPayload events.PostReactedPayload
}

func (l *likeLedger) InsertOutboxEvent(_ context.Context, eventType, aggregateType string, _ uuid.UUID, payload interface{}) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if aggregateType != "post" {
		return fmt.Errorf("aggregate type %q", aggregateType)
	}
	l.outbox[eventType]++
	if p, ok := payload.(events.PostReactedPayload); ok {
		l.lastPayload = p
	}
	return nil
}

func (l *likeLedger) publish(_ context.Context, e *engagement.EngagementEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.engagement[e.EventType]++
	return nil
}

func (l *likeLedger) clearTune(context.Context, uuid.UUID, uuid.UUID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tunesClear++
	return nil
}

// net is likes announced minus unlikes announced, on each channel.
func (l *likeLedger) net() (outbox, engagementNet int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.outbox[events.PostReacted] - l.outbox[events.PostUnreacted],
		l.engagement[engagement.EventPostLiked] - l.engagement[engagement.EventPostUnliked]
}

func (l *likeLedger) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.outbox {
		n += c
	}
	for _, c := range l.engagement {
		n += c
	}
	return n
}

type likeRig struct {
	svc    *Service
	hearts *fakeHearts
	index  *fakeLikeIndex
	ledger *likeLedger
	redis  *miniredis.Miniredis
	post   *postgres.Post
}

func newLikeRig(t *testing.T) *likeRig {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := &likeRig{
		hearts: newFakeHearts(),
		index:  &fakeLikeIndex{rows: map[string]bool{}},
		ledger: &likeLedger{outbox: map[string]int{}, engagement: map[string]int{}},
		redis:  mr,
		post:   &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Visibility: "public"},
	}
	r.svc = &Service{
		rdb:         rdb,
		rateLimiter: deadRedisLimiter(), // fails open; the limit is not under test
		likedReels:  r.index,
		engagementPostSeam: func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
			if postID != r.post.ID {
				return nil, ErrPostNotFound
			}
			return r.post, nil
		},
		counterReadSeam: r.hearts,
		likeSeams: likeSeams{
			heartSeam:             r.hearts,
			reactionOutboxSeam:    r.ledger,
			engagementPublishSeam: r.ledger.publish,
			tuneClearSeam:         r.ledger.clearTune,
		},
	}
	return r
}

func (r *likeRig) tap(t *testing.T, user uuid.UUID) *LikeToggleResult {
	t.Helper()
	res, err := r.svc.ToggleLike(context.Background(), r.post.ID, user)
	if err != nil {
		t.Fatalf("ToggleLike: %v", err)
	}
	return res
}

func (r *likeRig) redisLikes() string {
	return r.redis.HGet(fmt.Sprintf("post:eng:%s", r.post.ID), "likes")
}

// likeCounters is every number a like moves, in one comparable value.
type likeCounters struct {
	Stored        int64  // Scylla post_counters.like_count
	Redis         string // post:eng:<post> likes
	OutboxNet     int    // PostReacted - PostUnreacted
	EngagementNet int    // engagement.post.liked - unliked (engagement_counters, PG like_count)
}

func (r *likeRig) counters() likeCounters {
	outbox, eng := r.ledger.net()
	return likeCounters{Stored: r.hearts.count(r.post.ID), Redis: r.redisLikes(), OutboxNet: outbox, EngagementNet: eng}
}

func TestToggleLike_SetThenUnset(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()

	liked := r.tap(t, user)
	if !liked.Liked || liked.Count != 1 {
		t.Fatalf("first tap = %+v, want liked with count 1", liked)
	}
	if r.hearts.stored(r.post.ID, user) != "like" || !r.index.has(r.post.ID, user) {
		t.Fatal("the like is not stored (reactions_by_post and the liked list's record)")
	}
	if got := r.counters(); got != (likeCounters{Stored: 1, Redis: "1", OutboxNet: 1, EngagementNet: 1}) {
		t.Fatalf("after the like: %+v", got)
	}
	if r.ledger.tunesClear != 1 {
		t.Fatalf("a like being set clears the viewer's tune once, cleared %d times", r.ledger.tunesClear)
	}
	if p := r.ledger.lastPayload; p.PostID != r.post.ID.String() || p.ReactorID != user.String() || p.PostAuthorID != r.post.AuthorID.String() || p.ReactType != "like" {
		t.Fatalf("PostReacted payload = %+v", p)
	}

	unliked := r.tap(t, user)
	if unliked.Liked || unliked.Count != 0 {
		t.Fatalf("second tap = %+v, want not liked with count 0", unliked)
	}
	if r.hearts.stored(r.post.ID, user) != "" || r.index.has(r.post.ID, user) {
		t.Fatal("the like is still stored after the unlike")
	}
	if got := r.counters(); got != (likeCounters{Stored: 0, Redis: "0", OutboxNet: 0, EngagementNet: 0}) {
		t.Fatalf("after the unlike: %+v", got)
	}
	if r.ledger.outbox[events.PostUnreacted] != 1 || r.ledger.engagement[engagement.EventPostUnliked] != 1 {
		t.Fatalf("the unlike must announce itself once on each channel: %+v %+v", r.ledger.outbox, r.ledger.engagement)
	}
	if r.ledger.tunesClear != 1 {
		t.Fatal("an unlike touched the tune row")
	}
}

// feed-49's reproduction: like, wait past the 24 h membership key, tap
// twice. The two taps are an unlike and a like, and every counter ends
// where it started. It used to end unliked with every counter one too high.
func TestToggleLike_After24Hours_TwoTapsLeaveEveryCounterWhereItStarted(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()

	r.tap(t, user)
	start := r.counters()
	if start != (likeCounters{Stored: 1, Redis: "1", OutboxNet: 1, EngagementNet: 1}) {
		t.Fatalf("after the like: %+v", start)
	}

	r.redis.FastForward(25 * time.Hour)
	if r.redis.Exists(fmt.Sprintf("liked:%s:%s", user, r.post.ID)) {
		t.Fatal("precondition: the Redis membership key should have expired")
	}

	first := r.tap(t, user)
	if first.Liked || first.Count != 0 {
		t.Fatalf("the tap after 24 h = %+v; the viewer had liked the post, so it is an unlike with count 0", first)
	}
	if got := r.counters(); got != (likeCounters{Stored: 0, Redis: "0", OutboxNet: 0, EngagementNet: 0}) {
		t.Fatalf("after the unlike: %+v, want every counter back at zero", got)
	}

	second := r.tap(t, user)
	if !second.Liked || second.Count != 1 {
		t.Fatalf("the second tap = %+v, want liked with count 1", second)
	}
	if got := r.counters(); got != start {
		t.Fatalf("after like, 24 h, two taps: %+v, want the starting %+v", got, start)
	}
	if r.hearts.stored(r.post.ID, user) != "like" {
		t.Fatal("the viewer should end liked")
	}
}

// Fifty taps that all read "not liked" before any of them writes: one
// insert wins. Every answer is liked, and the like is counted once.
func TestToggleLike_FiftyConcurrentTapsCountOnce(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	const taps = 50

	r.hearts.readGate = make(chan struct{})
	r.hearts.reads.Add(taps)
	results := make([]*LikeToggleResult, taps)
	errs := make([]error, taps)
	var wg sync.WaitGroup
	for i := 0; i < taps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = r.svc.ToggleLike(context.Background(), r.post.ID, user)
		}(i)
	}
	r.hearts.reads.Wait() // all fifty have read the stored state
	close(r.hearts.readGate)
	wg.Wait()

	for i := range results {
		if errs[i] != nil {
			t.Fatalf("tap %d: %v", i, errs[i])
		}
		if !results[i].Liked {
			t.Fatalf("tap %d answered liked=false; all fifty asked for the like", i)
		}
	}
	if got := r.counters(); got != (likeCounters{Stored: 1, Redis: "1", OutboxNet: 1, EngagementNet: 1}) {
		t.Fatalf("after 50 concurrent taps: %+v, want one like on every counter", got)
	}
	if n := r.ledger.total(); n != 2 {
		t.Fatalf("%d announcements, want 2 (one outbox row, one engagement event)", n)
	}
	if reacts, _ := r.hearts.calls(); reacts != taps {
		t.Fatalf("%d conditional inserts, want all %d taps to have asked", reacts, taps)
	}
	if r.hearts.stored(r.post.ID, user) != "like" {
		t.Fatal("the viewer should end liked")
	}
}

// Fifty different viewers are fifty likes.
func TestToggleLike_FiftyViewersCountFifty(t *testing.T) {
	r := newLikeRig(t)
	const viewers = 50
	var wg sync.WaitGroup
	errs := make([]error, viewers)
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.svc.ToggleLike(context.Background(), r.post.ID, uuid.New())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("viewer %d: %v", i, err)
		}
	}
	if got := r.counters(); got != (likeCounters{Stored: viewers, Redis: "50", OutboxNet: viewers, EngagementNet: viewers}) {
		t.Fatalf("after 50 viewers: %+v", got)
	}
}

// A like made through POST /reactions (stored, but unknown to Redis) is
// taken off by a tap here, once, and the Redis counter does not go negative.
func TestToggleLike_UnlikesALikeMadeThroughReactions(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	if _, err := r.hearts.React(context.Background(), r.post.ID, user, "like"); err != nil {
		t.Fatal(err)
	}

	res := r.tap(t, user)
	if res.Liked || res.Count != 0 {
		t.Fatalf("tap = %+v, want an unlike with count 0 (it used to count the viewer a second time)", res)
	}
	if got := r.counters(); got != (likeCounters{Stored: 0, Redis: "0", OutboxNet: -1, EngagementNet: -1}) {
		t.Fatalf("counters = %+v", got)
	}
}

// An old emoji reads as the heart, so a tap takes it off.
func TestToggleLike_AnyStoredReactionIsTakenOff(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	if _, err := r.hearts.React(context.Background(), r.post.ID, user, "love"); err != nil {
		t.Fatal(err)
	}
	if res := r.tap(t, user); res.Liked || r.hearts.stored(r.post.ID, user) != "" {
		t.Fatalf("tap on a stored 'love' = %+v, stored %q; want it removed", res, r.hearts.stored(r.post.ID, user))
	}
}

// A write that changed nothing is refused as an error and counts nothing;
// the retry asks for the same state.
func TestToggleLike_StoreFailureCountsNothingAndIsRetryable(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	r.hearts.reactErr = errors.New("scylla timeout")

	if _, err := r.svc.ToggleLike(context.Background(), r.post.ID, user); err == nil {
		t.Fatal("a failed reaction write answered success")
	}
	if got := r.counters(); got != (likeCounters{}) || r.ledger.total() != 0 {
		t.Fatalf("a failed like was counted or announced: %+v, %d announcements", got, r.ledger.total())
	}

	r.hearts.reactErr = nil
	if res := r.tap(t, user); !res.Liked || res.Count != 1 {
		t.Fatalf("retry = %+v, want liked with count 1", res)
	}
}

// The row was written and its counter update failed: the viewer's state is
// what they asked for, so the tap succeeds (an error would make the retry
// flip it back) and the like is announced.
func TestToggleLike_RowWrittenButCounterFailed_StillSucceeds(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	r.hearts.counterErr = errors.New("counter write timeout")

	res, err := r.svc.ToggleLike(context.Background(), r.post.ID, user)
	if err != nil || !res.Liked {
		t.Fatalf("tap = %+v, %v; want liked", res, err)
	}
	if outbox, eng := r.ledger.net(); outbox != 1 || eng != 1 || r.redisLikes() != "1" {
		t.Fatalf("the stored like was not announced: outbox %d, engagement %d, redis %q", outbox, eng, r.redisLikes())
	}
}

// The liked list's record fails first: nothing is stored or counted.
func TestToggleLike_IndexFailureStopsBeforeTheReactionWrite(t *testing.T) {
	r := newLikeRig(t)
	r.index.failing = errors.New("postgres down")
	if _, err := r.svc.ToggleLike(context.Background(), r.post.ID, uuid.New()); err == nil {
		t.Fatal("want the index error")
	}
	if reacts, _ := r.hearts.calls(); reacts != 0 || r.ledger.total() != 0 {
		t.Fatalf("reaction written (%d) or announced (%d) after the index failed", reacts, r.ledger.total())
	}
}

func TestToggleLike_Gates(t *testing.T) {
	r := newLikeRig(t)
	if _, err := r.svc.ToggleLike(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("a post the viewer may not see: %v, want ErrPostNotFound", err)
	}
	r.post.NoLikes = true
	if _, err := r.svc.ToggleLike(context.Background(), r.post.ID, uuid.New()); !errors.Is(err, ErrLikesDisabled) {
		t.Fatalf("likes turned off: %v, want ErrLikesDisabled", err)
	}
	if reacts, _ := r.hearts.calls(); reacts != 0 {
		t.Fatal("a refused like reached the store")
	}
}

// removeLike (tune.go): takes a stored like off once, and is a no-op
// without one.
func TestRemoveLike_IsIdempotent(t *testing.T) {
	r := newLikeRig(t)
	user := uuid.New()
	ctx := context.Background()

	if err := r.svc.removeLike(ctx, r.post.ID, user); err != nil {
		t.Fatal(err)
	}
	if _, unreacts := r.hearts.calls(); unreacts != 0 || r.ledger.total() != 0 {
		t.Fatal("removing a like that is not there wrote or announced something")
	}

	r.tap(t, user)
	// The Redis membership key this used to ask is gone after 24 h; the
	// stored like is still found.
	r.redis.FastForward(25 * time.Hour)
	for i := 0; i < 2; i++ {
		if err := r.svc.removeLike(ctx, r.post.ID, user); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.counters(); got != (likeCounters{Stored: 0, Redis: "0", OutboxNet: 0, EngagementNet: 0}) {
		t.Fatalf("after removing the like twice: %+v", got)
	}
	if r.hearts.stored(r.post.ID, user) != "" {
		t.Fatal("the like is still stored")
	}
}

// The route's body is {"liked":…,"count":…} and nothing else (web
// usePostReaction.ts reads exactly these two fields).
func TestLikeToggleResult_WireShape(t *testing.T) {
	raw, err := json.Marshal(&LikeToggleResult{Liked: true, Count: 7})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"liked":true,"count":7}` {
		t.Fatalf("wire shape changed: %s", raw)
	}
	raw, _ = json.Marshal(&LikeToggleResult{})
	if string(raw) != `{"liked":false,"count":0}` {
		t.Fatalf("wire shape changed: %s", raw)
	}
}
