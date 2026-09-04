package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
)

// Post "more" sheet — Interested / Not interested (POST /v1/feed/feedback).
//
//   - the answer is one row per (viewer, post), latest wins;
//   - not_interested drops the post at the hydration tail;
//   - a failed lookup FAILS CLOSED, like block/mute (blocksafety_test.go).

// fakeFeedbackStore is feed_feedback as a map, keyed (viewer, post) exactly
// as the table's primary key is.
type fakeFeedbackStore struct {
	rows   map[[2]uuid.UUID]postgres.PostFeedback
	hidden map[uuid.UUID]struct{}
	err    error
	calls  int
}

func newFakeFeedbackStore() *fakeFeedbackStore {
	return &fakeFeedbackStore{rows: map[[2]uuid.UUID]postgres.PostFeedback{}, hidden: map[uuid.UUID]struct{}{}}
}

func (f *fakeFeedbackStore) UpsertFeedback(_ context.Context, r *postgres.PostFeedback) error {
	if f.err != nil {
		return f.err
	}
	f.rows[[2]uuid.UUID{r.UserID, r.PostID}] = *r
	return nil
}

func (f *fakeFeedbackStore) ExcludedPostIDs(_ context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]struct{}{}
	for _, id := range ids {
		if r, ok := f.rows[[2]uuid.UUID{viewerID, id}]; ok && r.Signal == FeedbackNotInterested {
			out[id] = struct{}{}
		}
		if _, ok := f.hidden[id]; ok {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

func (f *fakeFeedbackStore) AuthorFeedbackNet(_ context.Context, viewerID, authorID uuid.UUID) (int, error) {
	net := 0
	for k, r := range f.rows {
		if k[0] != viewerID || r.AuthorID != authorID {
			continue
		}
		if r.Signal == FeedbackInterested {
			net++
		} else {
			net--
		}
	}
	return net, nil
}

// upstreamStub stands in for every service HydratePosts talks to, keyed by
// path: post-service's batch (the given posts), trust-safety's keyword
// filters (none), graph-service's can (everything allowed), profile and
// media batches (empty — the render step tolerates a missing author).
func upstreamStub(t *testing.T, posts ...HydratedPost) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			var req struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]HydratedPost{}
			for _, p := range posts {
				for _, id := range req.IDs {
					if id == p.ID.String() {
						data[id] = p
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/internal/keyword-filters":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keywords": []string{}}})
		case "/v1/internal/graph/can":
			var req canReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]bool{}
			for _, id := range req.TargetIDs {
				data[id] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/profiles/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/v1/media/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func feedbackService(t *testing.T, store feedbackStore, posts ...HydratedPost) (*Service, func()) {
	t.Helper()
	up := upstreamStub(t, posts...)
	client := func(name string) *http.Client { return httpclient.NewWithBreaker(2e9, "test->"+name) }
	s := &Service{
		postServiceURL:    up.URL,
		postClient:        client("post"),
		trustSafetyURL:    up.URL,
		trustClient:       client("trust"),
		graphURL:          up.URL,
		graphClient:       client("graph"),
		profileServiceURL: up.URL,
		profileClient:     client("profile"),
		mediaServiceURL:   up.URL,
		mediaClient:       client("media"),
		feedback:          store,
	}
	return s, up.Close
}

func TestRecordFeedback_UpsertLatestWins(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	post := HydratedPost{ID: uuid.New(), AuthorID: author, Category: "comedy"}
	store := newFakeFeedbackStore()
	s, done := feedbackService(t, store, post)
	defer done()

	first, err := s.RecordFeedback(context.Background(), viewer, post.ID, FeedbackNotInterested)
	if err != nil {
		t.Fatalf("first answer: %v", err)
	}
	if first.AuthorID != author || first.Category != "comedy" {
		t.Fatalf("author/category must be copied from the post: %+v", first)
	}
	second, err := s.RecordFeedback(context.Background(), viewer, post.ID, FeedbackInterested)
	if err != nil {
		t.Fatalf("second answer: %v", err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("two answers on one post must be ONE row, got %d", len(store.rows))
	}
	if got := store.rows[[2]uuid.UUID{viewer, post.ID}].Signal; got != FeedbackInterested {
		t.Fatalf("latest answer must win, stored %q", got)
	}
	if second.Signal != FeedbackInterested {
		t.Fatalf("response echoes the stored signal, got %q", second.Signal)
	}
}

func TestRecordFeedback_RejectsUnknownSignalAndUnknownPost(t *testing.T) {
	viewer := uuid.New()
	store := newFakeFeedbackStore()
	s, done := feedbackService(t, store) // stub knows no posts
	defer done()

	if _, err := s.RecordFeedback(context.Background(), viewer, uuid.New(), "meh"); !errors.Is(err, ErrInvalidFeedbackSignal) {
		t.Fatalf("want ErrInvalidFeedbackSignal, got %v", err)
	}
	if _, err := s.RecordFeedback(context.Background(), viewer, uuid.New(), FeedbackNotInterested); !errors.Is(err, ErrFeedbackPostNotFound) {
		t.Fatalf("a post the viewer cannot see is not found, got %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatal("nothing may be stored for a rejected answer")
	}
}

func TestApplyFeedbackFilter_DropsNotInterestedAndHidden(t *testing.T) {
	viewer := uuid.New()
	keep, dropped, hidden := HydratedPost{ID: uuid.New()}, HydratedPost{ID: uuid.New()}, HydratedPost{ID: uuid.New()}
	interested := HydratedPost{ID: uuid.New()}

	store := newFakeFeedbackStore()
	store.rows[[2]uuid.UUID{viewer, dropped.ID}] = postgres.PostFeedback{Signal: FeedbackNotInterested}
	store.rows[[2]uuid.UUID{viewer, interested.ID}] = postgres.PostFeedback{Signal: FeedbackInterested}
	store.hidden[hidden.ID] = struct{}{}
	s := &Service{feedback: store}

	out, err := s.applyFeedbackFilter(context.Background(), viewer, []HydratedPost{keep, dropped, hidden, interested})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 2 || out[0].ID != keep.ID || out[1].ID != interested.ID {
		t.Fatalf("want [keep, interested], got %+v", out)
	}
}

// The hydration cache is exactly the case this filter exists for: the row
// was cached BEFORE the viewer answered. A cached row must still be dropped.
func TestHydratePosts_NotInterestedDroppedOnCacheOnlyPath(t *testing.T) {
	viewer := uuid.New()
	dropped := HydratedPost{ID: uuid.New(), AuthorID: uuid.New()}
	keep := HydratedPost{ID: uuid.New(), AuthorID: uuid.New()}
	store := newFakeFeedbackStore()
	store.rows[[2]uuid.UUID{viewer, dropped.ID}] = postgres.PostFeedback{Signal: FeedbackNotInterested}

	// rdb nil ⇒ no hydration cache, so both rows come from the stub; the
	// filter runs on the merged rows on both paths regardless.
	s, done := feedbackService(t, store, dropped, keep)
	defer done()

	items := []FeedItem{{PostID: dropped.ID, AuthorID: dropped.AuthorID}, {PostID: keep.ID, AuthorID: keep.AuthorID}}
	out, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if len(out) != 1 || out[0].ID != keep.ID {
		t.Fatalf("not_interested post must be gone from the page, got %+v", out)
	}
	if out[0].Reason != ReasonFollowing || out[0].ReasonText != "From someone you follow" {
		t.Fatalf("a timeline row is 'following', got %q / %q", out[0].Reason, out[0].ReasonText)
	}
}

func TestHydratePosts_FeedbackLookupFailureFailsClosed(t *testing.T) {
	viewer := uuid.New()
	post := HydratedPost{ID: uuid.New(), AuthorID: uuid.New()}
	store := newFakeFeedbackStore()
	store.err = errors.New("postgres down")
	s, done := feedbackService(t, store, post)
	defer done()

	out, err := s.HydratePosts(context.Background(), []FeedItem{{PostID: post.ID, AuthorID: post.AuthorID}}, viewer)
	if err == nil {
		t.Fatalf("hydration must fail when feedback state cannot be resolved; got %d rows", len(out))
	}
}

func TestApplyFeedbackFilter_FailsClosed(t *testing.T) {
	store := newFakeFeedbackStore()
	store.err = errors.New("postgres down")
	s := &Service{feedback: store}

	out, err := s.applyFeedbackFilter(context.Background(), uuid.New(), []HydratedPost{{ID: uuid.New()}})
	if err == nil {
		t.Fatal("a failed feedback lookup must be an ERROR, never an unfiltered page")
	}
	if out != nil {
		t.Fatalf("no rows may be returned alongside the error, got %d", len(out))
	}
}

func TestApplyFeedbackFilter_NoStoreIsPassThrough(t *testing.T) {
	s := &Service{}
	in := []HydratedPost{{ID: uuid.New()}}
	out, err := s.applyFeedbackFilter(context.Background(), uuid.New(), in)
	if err != nil || len(out) != 1 {
		t.Fatalf("unconfigured store passes through: %v %d", err, len(out))
	}
}
