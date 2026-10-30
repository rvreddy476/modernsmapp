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

// Audit M3: one request resolves the block set once however many windows
// it reads, and the follow / connection lists are cached briefly.

type graphCounter struct {
	blocked, following, connections atomic.Int64
	fail                            atomic.Bool
}

func (g *graphCounter) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "blocked-and-muted"):
			g.blocked.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{}})
		case strings.Contains(r.URL.Path, "/following/"):
			g.following.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{uuid.New().String()}})
		case strings.Contains(r.URL.Path, "/connections/"):
			g.connections.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{uuid.New().String()}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolveBlockedSet_OncePerRequest(t *testing.T) {
	g := &graphCounter{}
	srv := g.server(t)
	s := &Service{graphURL: srv.URL, graphClient: srv.Client()}
	viewer := uuid.New()

	ctx := WithRequestMemo(context.Background())
	for i := 0; i < 15; i++ { // a reels category page's window budget
		if _, err := s.resolveBlockedSet(ctx, viewer); err != nil {
			t.Fatal(err)
		}
	}
	if n := g.blocked.Load(); n != 1 {
		t.Fatalf("15 windows of one request resolved the block set %d times, want 1", n)
	}
	// A new request resolves afresh: the memo never outlives its request.
	if _, err := s.resolveBlockedSet(WithRequestMemo(context.Background()), viewer); err != nil {
		t.Fatal(err)
	}
	if n := g.blocked.Load(); n != 2 {
		t.Fatalf("a second request reused the first one's block set (%d resolutions)", n)
	}
}

func TestResolveBlockedSet_FailureIsNotMemoised(t *testing.T) {
	g := &graphCounter{}
	srv := g.server(t)
	s := &Service{graphURL: srv.URL, graphClient: srv.Client()}
	ctx := WithRequestMemo(context.Background())
	g.fail.Store(true)
	if _, err := s.resolveBlockedSet(ctx, uuid.New()); err == nil {
		t.Fatal("a graph failure resolved a block set")
	}
	viewer := uuid.New()
	if _, err := s.resolveBlockedSet(ctx, viewer); err == nil {
		t.Fatal("block set resolved while graph is down")
	}
	g.fail.Store(false)
	if _, err := s.resolveBlockedSet(ctx, viewer); err != nil {
		t.Fatalf("a failure was memoised: %v", err)
	}
}

func TestViewerFollowingAndConnections_AreCachedBriefly(t *testing.T) {
	g := &graphCounter{}
	srv := g.server(t)
	mr := miniredis.RunT(t)
	s := &Service{graphURL: srv.URL, graphClient: srv.Client(), rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	viewer := uuid.New()
	for i := 0; i < 3; i++ { // three separate requests
		if _, err := s.viewerFollowing(context.Background(), viewer); err != nil {
			t.Fatal(err)
		}
		if _, err := s.viewerConnections(context.Background(), viewer); err != nil {
			t.Fatal(err)
		}
	}
	if g.following.Load() != 1 || g.connections.Load() != 1 {
		t.Fatalf("following fetched %d times, connections %d; want 1 each", g.following.Load(), g.connections.Load())
	}
	// "Briefly": nothing invalidates the connection list early, so it may
	// live a minute at most.
	if ttl := mr.TTL(ConnectionsKey(viewer)); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("connections cached for %v, want (0, 1m]", ttl)
	}
	if ttl := mr.TTL(FollowingKey(viewer)); ttl <= 0 || ttl > followingCacheTTL {
		t.Fatalf("following cached for %v, want (0, %v]", ttl, followingCacheTTL)
	}
	mr.FastForward(61 * time.Second)
	if _, err := s.viewerConnections(context.Background(), viewer); err != nil {
		t.Fatal(err)
	}
	if g.connections.Load() != 2 {
		t.Fatal("an expired connections cache was still served")
	}
}

func TestViewerFollowing_ErrorIsNotCached(t *testing.T) {
	g := &graphCounter{}
	srv := g.server(t)
	mr := miniredis.RunT(t)
	s := &Service{graphURL: srv.URL, graphClient: srv.Client(), rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	viewer := uuid.New()
	g.fail.Store(true)
	if _, err := s.viewerFollowing(context.Background(), viewer); err == nil {
		t.Fatal("following resolved while graph is down")
	}
	if mr.Exists(FollowingKey(viewer)) {
		t.Fatal("a failed fetch was cached as an empty following list")
	}
}
