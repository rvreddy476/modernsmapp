package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// GET /v1/feed/watch?subscribed_only=true, the Tube "Subscriptions" tab,
// keeps only long videos by channel OWNERS the viewer subscribes to
// (post-service, cached in Redis). Same fail-closed rule as the Following
// tab, plus the cache the tab is served from. These tests pin
// applySubscribedFilter and subscribedOwners (subscriptions.go).

// subscribedOwnersStub is a post-service stub serving
// /internal/users/{id}/subscribed-owner-ids in the shared envelope. pages
// is the answer per `after` cursor ("" first); calls counts round trips.
type ownerPage struct {
	owners    []uuid.UUID
	nextAfter string
	hasMore   bool
}

func subscribedOwnersStub(t *testing.T, pages map[string]ownerPage, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if !strings.Contains(r.URL.Path, "/subscribed-owner-ids") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		page, ok := pages[r.URL.Query().Get("after")]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		out := make([]string, 0, len(page.owners))
		for _, id := range page.owners {
			out = append(out, id.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"owner_ids":  out,
			"next_after": page.nextAfter,
			"has_more":   page.hasMore,
		}})
	}))
}

func singlePage(owners ...uuid.UUID) map[string]ownerPage {
	return map[string]ownerPage{"": {owners: owners}}
}

// newSubscriptionService is a Service with a post-service stub and,
// optionally, a miniredis cache.
func newSubscriptionService(t *testing.T, postURL string, withRedis bool) (*Service, *miniredis.Miniredis) {
	t.Helper()
	s := &Service{
		postServiceURL: postURL,
		postClient:     &http.Client{Timeout: 2 * time.Second},
	}
	var mr *miniredis.Miniredis
	if withRedis {
		mr = miniredis.RunT(t)
		s.rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	}
	return s, mr
}

func TestWatchSubscribedFilter_KeepsOnlySubscribedOwners(t *testing.T) {
	subscribed := uuid.New()
	stranger := uuid.New()
	self := uuid.New()
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(subscribed), &calls)
	defer ps.Close()

	s, _ := newSubscriptionService(t, ps.URL, false)
	candidates := []FeedItem{
		feedItemBy(stranger),
		feedItemBy(subscribed),
		feedItemBy(self),
		feedItemBy(subscribed),
	}
	out, err := s.applySubscribedFilter(context.Background(), self, candidates, "watch")
	if err != nil {
		t.Fatalf("subscribed filter errored with a healthy post-service: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected the 2 long videos by the subscribed owner, got %d", len(out))
	}
	for _, c := range out {
		if c.AuthorID != subscribed {
			t.Fatalf("a video by %s survived a subscribed-only filter", c.AuthorID)
		}
	}
	if calls != 1 {
		t.Fatalf("expected exactly one post-service round trip, got %d", calls)
	}
}

// Timeline order survives: the tab is chronological and the filter must
// not reorder what the keyset window produced.
func TestWatchSubscribedFilter_PreservesTimelineOrder(t *testing.T) {
	owner := uuid.New()
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(owner), &calls)
	defer ps.Close()

	s, _ := newSubscriptionService(t, ps.URL, false)
	now := time.Now()
	newest := FeedItem{PostID: uuid.New(), AuthorID: owner, CreatedAt: now, ContentType: "long_video"}
	older := FeedItem{PostID: uuid.New(), AuthorID: owner, CreatedAt: now.Add(-time.Hour), ContentType: "long_video"}
	out, err := s.applySubscribedFilter(context.Background(), uuid.New(), []FeedItem{newest, feedItemBy(uuid.New()), older}, "watch")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].PostID != newest.PostID || out[1].PostID != older.PostID {
		t.Fatalf("subscribed filter reordered the window: %+v", out)
	}
}

func TestWatchSubscribedFilter_NoSubscriptionsIsAnEmptyPage(t *testing.T) {
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(), &calls)
	defer ps.Close()

	s, _ := newSubscriptionService(t, ps.URL, false)
	candidates := []FeedItem{feedItemBy(uuid.New()), feedItemBy(uuid.New())}
	out, err := s.applySubscribedFilter(context.Background(), uuid.New(), candidates, "watch")
	if err != nil {
		t.Fatalf("an empty subscription list is not an error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("a viewer with no subscriptions must get an empty Subscriptions tab, not %d videos", len(out))
	}
}

// An unresolved subscription list must be an error, never the viewer's
// whole timeline under a heading that promises subscribed channels.
func TestWatchSubscribedFilter_PostServiceErrorFailsClosed(t *testing.T) {
	for _, status := range []int{
		http.StatusNotFound,            // the route is not there yet
		http.StatusUnauthorized,        // internal-key gate rejected us
		http.StatusInternalServerError, // post-service is unhealthy
	} {
		ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{}`))
		}))
		s, _ := newSubscriptionService(t, ps.URL, false)
		out, err := s.applySubscribedFilter(context.Background(), uuid.New(), []FeedItem{feedItemBy(uuid.New())}, "watch")
		ps.Close()
		if err == nil {
			t.Errorf("status %d returned no error; the Subscriptions tab would serve strangers", status)
		}
		if len(out) != 0 {
			t.Errorf("status %d returned %d candidates alongside the error", status, len(out))
		}
	}

	s, _ := newSubscriptionService(t, "http://127.0.0.1:1", false)
	if _, err := s.applySubscribedFilter(context.Background(), uuid.New(), []FeedItem{feedItemBy(uuid.New())}, "watch"); err == nil {
		t.Fatal("an unreachable post-service must be an error")
	}
}

// A malformed body decodes to no owners, which is indistinguishable from
// "no subscriptions". It must be an error instead.
func TestWatchSubscribedFilter_MalformedBodyFailsClosed(t *testing.T) {
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": "nope"`))
	}))
	defer ps.Close()
	s, _ := newSubscriptionService(t, ps.URL, false)
	if _, err := s.applySubscribedFilter(context.Background(), uuid.New(), []FeedItem{feedItemBy(uuid.New())}, "watch"); err == nil {
		t.Fatal("a malformed subscribed-owners body must be an error")
	}
}

// No candidates → no post-service round trip.
func TestWatchSubscribedFilter_EmptyWindowSkipsPostService(t *testing.T) {
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(uuid.New()), &calls)
	defer ps.Close()

	s, _ := newSubscriptionService(t, ps.URL, false)
	out, err := s.applySubscribedFilter(context.Background(), uuid.New(), nil, "watch")
	if err != nil {
		t.Fatalf("empty window errored: %v", err)
	}
	if len(out) != 0 || calls != 0 {
		t.Fatalf("empty window: got %d items and %d post-service calls, want 0 and 0", len(out), calls)
	}
}

// The second request is served from Redis: one post-service round trip,
// the set under user:subscribed_owners:{viewer} with a TTL, and the same
// answer both times.
func TestSubscribedOwners_SecondCallServedFromCache(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(owner), &calls)
	defer ps.Close()

	s, mr := newSubscriptionService(t, ps.URL, true)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		owners, err := s.subscribedOwners(ctx, viewer)
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if len(owners) != 1 || owners[0] != owner {
			t.Fatalf("call %d: got %v, want [%s]", i+1, owners, owner)
		}
	}
	if calls != 1 {
		t.Fatalf("expected one post-service round trip across two calls, got %d", calls)
	}
	key := subscribedOwnersKey(viewer)
	if !mr.Exists(key) {
		t.Fatalf("%s was not written", key)
	}
	if ttl := mr.TTL(key); ttl <= 0 || ttl > subscribedOwnersTTL {
		t.Fatalf("%s TTL = %v, want (0, %v]", key, ttl, subscribedOwnersTTL)
	}
	if members, _ := mr.SMembers(key); len(members) != 1 || members[0] != owner.String() {
		t.Fatalf("%s holds %v, want [%s]", key, members, owner)
	}
}

// An empty answer is cached too, through the looked marker: without it a
// viewer with no subscriptions would hit post-service on every page.
func TestSubscribedOwners_EmptyAnswerIsCached(t *testing.T) {
	viewer := uuid.New()
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(), &calls)
	defer ps.Close()

	s, mr := newSubscriptionService(t, ps.URL, true)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		owners, err := s.subscribedOwners(ctx, viewer)
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if len(owners) != 0 {
			t.Fatalf("call %d: got %v, want none", i+1, owners)
		}
	}
	if calls != 1 {
		t.Fatalf("an empty answer must be remembered; post-service was called %d times", calls)
	}
	if !mr.Exists(subscribedLookedKey(viewer)) {
		t.Fatal("the looked marker was not written for an empty answer")
	}
}

// A failed fetch caches nothing: the next request tries post-service
// again rather than serving the failure as "no subscriptions".
func TestSubscribedOwners_FailureIsNotCached(t *testing.T) {
	viewer := uuid.New()
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ps.Close()
	s, mr := newSubscriptionService(t, ps.URL, true)
	if _, err := s.subscribedOwners(context.Background(), viewer); err == nil {
		t.Fatal("a 500 must be an error")
	}
	if mr.Exists(subscribedOwnersKey(viewer)) || mr.Exists(subscribedLookedKey(viewer)) {
		t.Fatal("a failed fetch must leave no cache entry behind")
	}
}

// InvalidateSubscribedOwners (the consumer's hook) drops both keys so the
// next call refetches.
func TestSubscribedOwners_InvalidateForcesRefetch(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	var calls int32
	ps := subscribedOwnersStub(t, singlePage(owner), &calls)
	defer ps.Close()

	s, mr := newSubscriptionService(t, ps.URL, true)
	ctx := context.Background()
	if _, err := s.subscribedOwners(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	if err := s.InvalidateSubscribedOwners(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	if mr.Exists(subscribedOwnersKey(viewer)) || mr.Exists(subscribedLookedKey(viewer)) {
		t.Fatal("invalidate left a key behind")
	}
	if _, err := s.subscribedOwners(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected a refetch after invalidation, got %d post-service calls", calls)
	}
}

// The route is keyset paged and the loop follows has_more / next_after
// until the last page, so a viewer with many subscriptions is never
// silently truncated to the first page.
func TestFetchSubscribedOwners_FollowsPages(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	ps := subscribedOwnersStub(t, map[string]ownerPage{
		"":         {owners: []uuid.UUID{a, b}, nextAfter: b.String(), hasMore: true},
		b.String(): {owners: []uuid.UUID{c}, nextAfter: c.String(), hasMore: false},
	}, &calls)
	defer ps.Close()

	s, _ := newSubscriptionService(t, ps.URL, false)
	owners, err := s.fetchSubscribedOwners(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 3 || owners[0] != a || owners[1] != b || owners[2] != c {
		t.Fatalf("got %v, want [%s %s %s]", owners, a, b, c)
	}
	if calls != 2 {
		t.Fatalf("expected 2 pages, got %d calls", calls)
	}
}
