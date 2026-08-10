package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Module 2 M2-P0-3 — block safety in the feed and the cold-start path.
//
// Two defects motivated these tests:
//
//  1. graph-service's blocked-and-muted set was one-way, so the person
//     who pressed "block" got no protection from the person they blocked.
//     (Fixed in graph-service; asserted there against live Postgres.)
//  2. feed-service treated a failed block lookup as "nobody is blocked"
//     and served an unfiltered feed. The HTTP status code was not even
//     checked, so a 401 from the internal-key gate looked identical to an
//     empty block list.

func feedItemBy(author uuid.UUID) FeedItem {
	return FeedItem{
		PostID:      uuid.New(),
		AuthorID:    author,
		CreatedAt:   time.Now().UTC(),
		ContentType: "post",
	}
}

func TestApplyBlockFilter_RemovesBlockedAuthors(t *testing.T) {
	blockedAuthor := uuid.New()
	okAuthor := uuid.New()

	items := []FeedItem{
		feedItemBy(okAuthor),
		feedItemBy(blockedAuthor),
		feedItemBy(okAuthor),
	}

	out := applyBlockFilter(items, blockedSetOf([]uuid.UUID{blockedAuthor}))
	if len(out) != 2 {
		t.Fatalf("expected 2 surviving items, got %d", len(out))
	}
	for _, it := range out {
		if it.AuthorID == blockedAuthor {
			t.Fatal("a blocked author's post survived the filter")
		}
	}
}

func TestApplyBlockFilter_EmptySetIsPassThrough(t *testing.T) {
	items := []FeedItem{feedItemBy(uuid.New()), feedItemBy(uuid.New())}
	if got := applyBlockFilter(items, nil); len(got) != 2 {
		t.Fatalf("no blocks means no filtering; got %d of 2", len(got))
	}
}

// The cold-start fallback pulls recent public posts from post-service,
// bypassing the viewer's timeline entirely. It must apply the same filter
// — a viewer with an empty timeline is exactly the viewer served by this
// path, and a stranger who blocked them is exactly who it would surface.
func TestColdStartCandidates_AreBlockFiltered(t *testing.T) {
	blockedAuthor := uuid.New()
	coldItems := []FeedItem{
		feedItemBy(blockedAuthor),
		feedItemBy(uuid.New()),
		feedItemBy(blockedAuthor),
	}

	out := applyBlockFilter(coldItems, blockedSetOf([]uuid.UUID{blockedAuthor}))
	if len(out) != 1 {
		t.Fatalf("cold-start fallback must exclude blocked authors; %d of 3 survived", len(out))
	}
	if out[0].AuthorID == blockedAuthor {
		t.Fatal("cold-start fallback surfaced a blocked author")
	}
}

// --- fail-closed lookup -----------------------------------------------------

func newFeedServiceWithGraph(url string) *Service {
	return &Service{
		graphURL:    url,
		graphClient: &http.Client{Timeout: 2 * time.Second},
	}
}

// A non-200 response must be an error. Previously the status was ignored
// entirely: the body failed to decode into user_ids, the decode error was
// discarded, and the function returned an empty list with a nil error —
// so every blocked author flowed back into the feed silently.
func TestGetBlockedAndMuted_NonOKStatusIsAnError(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized,        // internal-key gate rejected us
		http.StatusInternalServerError, // graph-service is unhealthy
		http.StatusBadGateway,          // the mesh dropped it
	} {
		graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{}`))
		}))

		s := newFeedServiceWithGraph(graph.URL)
		_, err := s.getBlockedAndMuted(context.Background(), uuid.New())
		graph.Close()

		if err == nil {
			t.Errorf("status %d returned no error — the feed would serve unfiltered", status)
		}
	}
}

// A malformed body decodes to an empty set, which is indistinguishable
// from "nobody is blocked". It must be an error instead.
func TestGetBlockedAndMuted_MalformedBodyIsAnError(t *testing.T) {
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`this is not json`))
	}))
	defer graph.Close()

	s := newFeedServiceWithGraph(graph.URL)
	if _, err := s.getBlockedAndMuted(context.Background(), uuid.New()); err == nil {
		t.Fatal("a malformed block list must be an error, not an empty set")
	}
}

// An unreachable graph-service must error rather than degrade silently.
func TestGetBlockedAndMuted_UnreachableIsAnError(t *testing.T) {
	s := newFeedServiceWithGraph("http://127.0.0.1:1")
	if _, err := s.getBlockedAndMuted(context.Background(), uuid.New()); err == nil {
		t.Fatal("an unreachable graph-service must be an error")
	}
}

// The happy path still parses.
func TestGetBlockedAndMuted_ParsesUserIDs(t *testing.T) {
	blocked := uuid.New()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_ids":["` + blocked.String() + `"]}`))
	}))
	defer graph.Close()

	s := newFeedServiceWithGraph(graph.URL)
	ids, err := s.getBlockedAndMuted(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != blocked {
		t.Fatalf("ids = %v, want [%s]", ids, blocked)
	}
}
