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

func TestDiscoverySlots(t *testing.T) {
	cases := []struct {
		limit int
		share float64
		want  int
	}{
		{20, 0.2, 4},
		{10, 0.2, 2},
		{3, 0.2, 1}, // at least one
		{2, 0.2, 1},
		{1, 0.2, 0}, // a page of one stays the viewer's network
		{20, 0, 0},  // off
		{20, 0.5, 10},
		{5, 0.5, 2}, // never more than half
	}
	for _, tc := range cases {
		if got := discoverySlots(tc.limit, tc.share); got != tc.want {
			t.Errorf("discoverySlots(%d, %v) = %d, want %d", tc.limit, tc.share, got, tc.want)
		}
	}
}

func TestLoadDiscoveryConfig(t *testing.T) {
	for raw, want := range map[string]float64{"": 0.2, "0": 0, "0.3": 0.3, "0.9": maxDiscoveryShare, "-1": 0, "nope": 0.2} {
		t.Setenv("FEED_DISCOVERY_SHARE", raw)
		if got := loadDiscoveryConfig().share; got != want {
			t.Errorf("FEED_DISCOVERY_SHARE=%q: share %v, want %v", raw, got, want)
		}
	}
}

func TestDiscoveryOnlyOnTheRankedUnnarrowedPage(t *testing.T) {
	if !discoveryAllowed("ranked", false, false) {
		t.Fatal("ranked For You must get discovery")
	}
	for _, c := range []struct {
		mode               string
		circle, followOnly bool
	}{{"chronological", false, false}, {"shadow", false, false}, {"ranked", true, false}, {"ranked", false, true}} {
		if discoveryAllowed(c.mode, c.circle, c.followOnly) {
			t.Errorf("discovery allowed for %+v", c)
		}
	}
	if !hasColdStart([]FeedItem{{}, {Source: sourceColdStart}}) || hasColdStart([]FeedItem{{Source: sourceTimeline}}) {
		t.Fatal("hasColdStart misreads the page")
	}
}

func TestRankDiscoveryOrdersByVelocityThenRecency(t *testing.T) {
	now := time.Now()
	a := FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now.Add(-time.Hour), CursorToken: "x"}
	b := FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now}
	c := FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now.Add(-2 * time.Hour)}
	drop := FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now}
	vel := map[uuid.UUID]float64{a.PostID: 3, c.PostID: 3}

	got := rankDiscovery([]FeedItem{b, c, drop, a, a}, vel, func(f FeedItem) bool { return f.PostID == drop.PostID })
	if len(got) != 3 || got[0].PostID != a.PostID || got[1].PostID != c.PostID || got[2].PostID != b.PostID {
		t.Fatalf("order = %v", got)
	}
	for _, g := range got {
		if g.Source != sourceColdStart || g.CursorToken != "" {
			t.Fatalf("a discovery item must be a recommendation with no cursor: %+v", g)
		}
	}
}

// The whole path: anonymous recent-public pool from post-service, minus the
// viewer, the people they follow, blocked authors, reels, what the page
// already holds and what was offered before; best velocity first; and a
// second page offers the next ones.
func TestDiscoveryForPicksOutOfNetworkPostsOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	viewer, followed, blocked := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()

	type post struct {
		ID, AuthorID, ContentType string
		CreatedAt                 time.Time
	}
	mk := func(author uuid.UUID, ct string, ageMin int) post {
		return post{uuid.NewString(), author.String(), ct, now.Add(-time.Duration(ageMin) * time.Minute)}
	}
	own := mk(viewer, "post", 1)
	fromFollowed := mk(followed, "post", 1)
	fromBlocked := mk(blocked, "post", 1)
	reel := mk(uuid.New(), "reel", 1)
	inPage := mk(uuid.New(), "post", 1)
	e1, e2, e3, e4 := mk(uuid.New(), "post", 5), mk(uuid.New(), "post", 4), mk(uuid.New(), "post", 3), mk(uuid.New(), "post", 2)
	pool := []post{own, fromFollowed, fromBlocked, reel, inPage, e1, e2, e3, e4}
	for _, p := range []struct {
		p post
		v float64
	}{{e1, 9}, {e2, 7}, {fromFollowed, 50}, {fromBlocked, 50}} {
		if _, err := mr.ZAdd("post:velocity:ranked", p.v, p.p.ID); err != nil {
			t.Fatal(err)
		}
	}

	var postCalls, graphCalls atomic.Int32
	postSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		postCalls.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/v1/posts/recent") || r.Header.Get("X-User-Id") != "" {
			t.Errorf("pool read %s as %q; want the anonymous recent-public page", r.URL, r.Header.Get("X-User-Id"))
		}
		var data []map[string]any
		for _, p := range pool {
			data = append(data, map[string]any{"id": p.ID, "author_id": p.AuthorID, "created_at": p.CreatedAt, "content_type": p.ContentType})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer postSvc.Close()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		graphCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{followed.String()}})
	}))
	defer graph.Close()

	s := &Service{
		rdb: rdb, postServiceURL: postSvc.URL, postClient: postSvc.Client(),
		graphURL: graph.URL, graphClient: graph.Client(),
	}
	page := []FeedItem{{PostID: uuid.MustParse(inPage.ID), AuthorID: uuid.MustParse(inPage.AuthorID)}}
	blockedSet := map[uuid.UUID]struct{}{blocked: {}}

	first := s.discoveryFor(context.Background(), viewer, page, 2, blockedSet, false)
	if ids := discoveredIDs(first); len(ids) != 2 || ids[0] != e1.ID || ids[1] != e2.ID {
		t.Fatalf("first page discovery = %v, want [e1 e2] (best velocity, out of network)", ids)
	}
	second := s.discoveryFor(context.Background(), viewer, page, 2, blockedSet, false)
	if ids := discoveredIDs(second); len(ids) != 2 || ids[0] != e4.ID || ids[1] != e3.ID {
		t.Fatalf("second page discovery = %v, want [e4 e3] (not offered again; newest first at equal velocity)", ids)
	}
	if third := s.discoveryFor(context.Background(), viewer, page, 2, blockedSet, false); len(third) != 0 {
		t.Fatalf("third page offered %v; everything eligible was already offered", discoveredIDs(third))
	}
	if postCalls.Load() != 1 || graphCalls.Load() != 1 {
		t.Fatalf("post-service read %d times, graph %d times; the pool and the following set are cached", postCalls.Load(), graphCalls.Load())
	}
	if ttl := mr.TTL(discoverySeenKey(viewer)); ttl <= 0 || ttl > discoverySeenTTL {
		t.Fatalf("offered set TTL = %v", ttl)
	}
}

// A graph-service failure means no discovery, never discovery that might
// include the people the viewer follows mislabelled, or a broken page.
func TestDiscoveryForGivesNothingWhenFollowingIsUnknown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	postSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": uuid.NewString(), "author_id": uuid.NewString(), "created_at": time.Now(), "content_type": "post"},
		}})
	}))
	defer postSvc.Close()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer graph.Close()
	s := &Service{rdb: rdb, postServiceURL: postSvc.URL, postClient: postSvc.Client(), graphURL: graph.URL, graphClient: graph.Client()}
	if got := s.discoveryFor(context.Background(), uuid.New(), nil, 3, nil, false); len(got) != 0 {
		t.Fatalf("discovery without a following set = %v", discoveredIDs(got))
	}
}

func discoveredIDs(items []FeedItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.PostID.String()
	}
	return out
}
