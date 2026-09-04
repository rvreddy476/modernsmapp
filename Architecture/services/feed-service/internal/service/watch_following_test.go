package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// GET /v1/feed/watch?following_only=true — the watch "Following" tab keeps
// only long videos by authors the viewer FOLLOWS (graph-service, the same
// set and the same fail-closed rule as the reels Following tab). Both the
// plain page and the category page go through videoTimelineWindow, which
// narrows with applyFollowingFilter; these tests pin that helper.

// followingGraph is a graph-service stub whose /v1/graph/following/{id}
// answers with the given follow set. calls counts round trips.
func followingGraph(t *testing.T, following []uuid.UUID, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if following == nil {
			following = []uuid.UUID{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": following})
	}))
}

func TestWatchFollowingFilter_KeepsOnlyFollowedAuthors(t *testing.T) {
	followed := uuid.New()
	stranger := uuid.New()
	self := uuid.New()
	var calls int32
	graph := followingGraph(t, []uuid.UUID{followed}, &calls)
	defer graph.Close()

	s := newFeedServiceWithGraph(graph.URL)
	candidates := []FeedItem{
		feedItemBy(stranger),
		feedItemBy(followed),
		feedItemBy(self),
		feedItemBy(followed),
	}
	out, err := s.applyFollowingFilter(context.Background(), self, candidates, "watch")
	if err != nil {
		t.Fatalf("following filter errored with a healthy graph: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected the 2 long videos by the followed author, got %d", len(out))
	}
	for _, c := range out {
		if c.AuthorID != followed {
			t.Fatalf("a video by %s survived a following-only filter", c.AuthorID)
		}
	}
	if calls != 1 {
		t.Fatalf("expected exactly one graph round trip, got %d", calls)
	}
}

func TestWatchFollowingFilter_NoFollowsIsAnEmptyPage(t *testing.T) {
	var calls int32
	graph := followingGraph(t, nil, &calls)
	defer graph.Close()

	s := newFeedServiceWithGraph(graph.URL)
	candidates := []FeedItem{feedItemBy(uuid.New()), feedItemBy(uuid.New())}
	out, err := s.applyFollowingFilter(context.Background(), uuid.New(), candidates, "watch")
	if err != nil {
		t.Fatalf("an empty follow set is not an error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("a viewer who follows nobody must get an empty Following tab, not %d videos", len(out))
	}
}

// An unresolved follow graph must be an error — never a page of strangers
// labelled "Following", and never the old subscribed-owner-ids fallback.
func TestWatchFollowingFilter_GraphErrorFailsClosed(t *testing.T) {
	for _, status := range []int{
		http.StatusNotFound,            // the route is not there
		http.StatusUnauthorized,        // internal-key gate rejected us
		http.StatusInternalServerError, // graph-service is unhealthy
	} {
		graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{}`))
		}))
		s := newFeedServiceWithGraph(graph.URL)
		out, err := s.applyFollowingFilter(context.Background(), uuid.New(), []FeedItem{feedItemBy(uuid.New())}, "watch")
		graph.Close()
		if err == nil {
			t.Errorf("status %d returned no error — the Following tab would serve strangers", status)
		}
		if len(out) != 0 {
			t.Errorf("status %d returned %d candidates alongside the error", status, len(out))
		}
	}

	s := newFeedServiceWithGraph("http://127.0.0.1:1")
	if _, err := s.applyFollowingFilter(context.Background(), uuid.New(), []FeedItem{feedItemBy(uuid.New())}, "watch"); err == nil {
		t.Fatal("an unreachable graph-service must be an error")
	}
}

// No candidates → no graph round trip: the empty timeline is empty
// regardless of who the viewer follows.
func TestWatchFollowingFilter_EmptyWindowSkipsGraph(t *testing.T) {
	var calls int32
	graph := followingGraph(t, []uuid.UUID{uuid.New()}, &calls)
	defer graph.Close()

	s := newFeedServiceWithGraph(graph.URL)
	out, err := s.applyFollowingFilter(context.Background(), uuid.New(), nil, "watch")
	if err != nil {
		t.Fatalf("empty window errored: %v", err)
	}
	if len(out) != 0 || calls != 0 {
		t.Fatalf("empty window: got %d items and %d graph calls, want 0 and 0", len(out), calls)
	}
}
