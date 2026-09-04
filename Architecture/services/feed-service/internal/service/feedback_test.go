package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/feed-service/internal/ranking"
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
	// authors is feed_author_feedback, keyed (viewer, author).
	authors map[[2]uuid.UUID]postgres.AuthorFeedback
	err     error
	calls   int
}

func newFakeFeedbackStore() *fakeFeedbackStore {
	return &fakeFeedbackStore{
		rows:    map[[2]uuid.UUID]postgres.PostFeedback{},
		hidden:  map[uuid.UUID]struct{}{},
		authors: map[[2]uuid.UUID]postgres.AuthorFeedback{},
	}
}

func (f *fakeFeedbackStore) UpsertAuthorFeedback(_ context.Context, r *postgres.AuthorFeedback) error {
	if f.err != nil {
		return f.err
	}
	f.authors[[2]uuid.UUID{r.UserID, r.AuthorID}] = *r
	return nil
}

func (f *fakeFeedbackStore) MutedAuthorIDs(_ context.Context, viewerID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]struct{}{}
	for k, r := range f.authors {
		if k[0] == viewerID && r.Signal == FeedbackNotInterested {
			out[k[1]] = struct{}{}
		}
	}
	return out, nil
}

func (f *fakeFeedbackStore) ListMutedAuthors(_ context.Context, viewerID uuid.UUID) ([]postgres.MutedAuthor, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []postgres.MutedAuthor
	for k, r := range f.authors {
		if k[0] == viewerID && r.Signal == FeedbackNotInterested {
			out = append(out, postgres.MutedAuthor{AuthorID: k[1], CreatedAt: r.CreatedAt})
		}
	}
	return out, nil
}

// mute is a test helper: the viewer has "Don't recommend" on the author.
func (f *fakeFeedbackStore) mute(viewer, author uuid.UUID) {
	f.authors[[2]uuid.UUID{viewer, author}] = postgres.AuthorFeedback{UserID: viewer, AuthorID: author, Signal: FeedbackNotInterested}
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

func (f *fakeFeedbackStore) AuthorFeedbackNet(_ context.Context, viewerID, authorID uuid.UUID) (int, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
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
	a, ok := f.authors[[2]uuid.UUID{viewerID, authorID}]
	return net, ok && a.Signal == FeedbackNotInterested, nil
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

// "Don't recommend this account" — author-level feedback
// (POST /v1/feed/feedback {author_id}):
//
//   - one row per (viewer, author), latest wins; interested clears;
//   - not_interested drops EVERY post by the author at the hydration tail,
//     on the cached and uncached paths alike, and keeps everyone else's;
//   - the author is the maximum ranker penalty while muted;
//   - a failed author lookup FAILS CLOSED, like the post-level one.

func TestRecordAuthorFeedback_Validation(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	cases := []struct {
		name   string
		author uuid.UUID
		signal string
		want   error
	}{
		{"own account", viewer, FeedbackNotInterested, ErrOwnAuthorFeedback},
		{"own account, interested", viewer, FeedbackInterested, ErrOwnAuthorFeedback},
		{"unknown signal", author, "meh", ErrInvalidFeedbackSignal},
		{"empty signal", author, "", ErrInvalidFeedbackSignal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeFeedbackStore()
			s := &Service{feedback: store}
			if _, err := s.RecordAuthorFeedback(context.Background(), viewer, tc.author, tc.signal); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if len(store.authors) != 0 {
				t.Fatal("nothing may be stored for a rejected answer")
			}
		})
	}
}

func TestRecordAuthorFeedback_MuteThenInterestedClears(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	store := newFakeFeedbackStore()
	s := &Service{feedback: store}

	f, err := s.RecordAuthorFeedback(context.Background(), viewer, author, FeedbackNotInterested)
	if err != nil {
		t.Fatalf("mute: %v", err)
	}
	if f.UserID != viewer || f.AuthorID != author || f.Signal != FeedbackNotInterested {
		t.Fatalf("response echoes the stored row, got %+v", f)
	}
	muted, err := s.feedback.MutedAuthorIDs(context.Background(), viewer)
	if err != nil || len(muted) != 1 {
		t.Fatalf("author must be muted after not_interested: %v %v", muted, err)
	}
	listed, err := s.ListMutedAuthors(context.Background(), viewer)
	if err != nil || len(listed) != 1 || listed[0].AuthorID != author {
		t.Fatalf("listing shows the mute: %+v %v", listed, err)
	}

	// Ranker: an active mute is the maximum author penalty even with a
	// positive post-level history.
	store.rows[[2]uuid.UUID{viewer, uuid.New()}] = postgres.PostFeedback{AuthorID: author, Signal: FeedbackInterested}
	net, isMuted, err := store.AuthorFeedbackNet(context.Background(), viewer, author)
	if err != nil || !isMuted || net != 1 {
		t.Fatalf("net/muted = %d/%v (%v), want 1/true", net, isMuted, err)
	}
	if got := ranking.AuthorPenalty(ranking.NetWithMute(float64(net), isMuted)); got != 0.5 {
		t.Fatalf("a muted author must carry the maximum penalty, got %v", got)
	}

	// Same answer twice is still one row.
	if _, err := s.RecordAuthorFeedback(context.Background(), viewer, author, FeedbackNotInterested); err != nil {
		t.Fatalf("re-mute: %v", err)
	}
	if len(store.authors) != 1 {
		t.Fatalf("two answers on one author must be ONE row, got %d", len(store.authors))
	}

	// Interested clears the mute.
	if _, err := s.RecordAuthorFeedback(context.Background(), viewer, author, FeedbackInterested); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if muted, _ := s.feedback.MutedAuthorIDs(context.Background(), viewer); len(muted) != 0 {
		t.Fatalf("interested must clear the mute, still muted: %v", muted)
	}
	listed, err = s.ListMutedAuthors(context.Background(), viewer)
	if err != nil || listed == nil || len(listed) != 0 {
		t.Fatalf("listing after clear is an empty list, never nil: %#v %v", listed, err)
	}
	if got := ranking.AuthorPenalty(ranking.NetWithMute(1, false)); got != 0 {
		t.Fatalf("no penalty once cleared, got %v", got)
	}
}

func TestApplyFeedbackFilter_DropsEveryPostByMutedAuthor(t *testing.T) {
	viewer, muted, other := uuid.New(), uuid.New(), uuid.New()
	m1 := HydratedPost{ID: uuid.New(), AuthorID: muted}
	m2 := HydratedPost{ID: uuid.New(), AuthorID: muted}
	keep1 := HydratedPost{ID: uuid.New(), AuthorID: other}
	keep2 := HydratedPost{ID: uuid.New(), AuthorID: viewer}
	// other's post, but it reached this surface because the muted account
	// reposted it: the muted account must not surface through a repost.
	reposted := HydratedPost{ID: uuid.New(), AuthorID: other, IsRepost: true, RepostedBy: &muted}
	// The muted author's post reposted by someone the viewer is fine with is
	// still the muted author's post.
	mutedViaOther := HydratedPost{ID: uuid.New(), AuthorID: muted, IsRepost: true, RepostedBy: &other}

	store := newFakeFeedbackStore()
	store.mute(viewer, muted)
	s := &Service{feedback: store}

	out, err := s.applyFeedbackFilter(context.Background(), viewer, []HydratedPost{m1, keep1, m2, reposted, keep2, mutedViaOther})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 2 || out[0].ID != keep1.ID || out[1].ID != keep2.ID {
		t.Fatalf("want [keep1, keep2], got %+v", out)
	}
}

func TestApplyFeedbackFilter_UnmutedAuthorPassesThrough(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	post := HydratedPost{ID: uuid.New(), AuthorID: author}
	store := newFakeFeedbackStore()
	store.authors[[2]uuid.UUID{viewer, author}] = postgres.AuthorFeedback{Signal: FeedbackInterested}
	// Another viewer's mute of the same author must not leak.
	store.mute(uuid.New(), author)
	s := &Service{feedback: store}

	out, err := s.applyFeedbackFilter(context.Background(), viewer, []HydratedPost{post})
	if err != nil || len(out) != 1 {
		t.Fatalf("an interested / other-viewer row must not drop the post: %v %+v", err, out)
	}
}

func TestHydratePosts_MutedAuthorGoneUntilInterested(t *testing.T) {
	viewer, muted, other := uuid.New(), uuid.New(), uuid.New()
	a := HydratedPost{ID: uuid.New(), AuthorID: muted}
	b := HydratedPost{ID: uuid.New(), AuthorID: other}
	c := HydratedPost{ID: uuid.New(), AuthorID: muted}
	store := newFakeFeedbackStore()
	s, done := feedbackService(t, store, a, b, c)
	defer done()
	items := []FeedItem{{PostID: a.ID, AuthorID: a.AuthorID}, {PostID: b.ID, AuthorID: b.AuthorID}, {PostID: c.ID, AuthorID: c.AuthorID}}

	if _, err := s.RecordAuthorFeedback(context.Background(), viewer, muted, FeedbackNotInterested); err != nil {
		t.Fatalf("mute: %v", err)
	}
	out, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if len(out) != 1 || out[0].ID != b.ID {
		t.Fatalf("every post by the muted author must be gone, got %+v", out)
	}

	if _, err := s.RecordAuthorFeedback(context.Background(), viewer, muted, FeedbackInterested); err != nil {
		t.Fatalf("clear: %v", err)
	}
	out, err = s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatalf("hydrate after clear: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("posts come back once the mute is cleared, got %d", len(out))
	}
}

// authorLookupFailingStore fails ONLY the author-level lookup, so the
// post-level step succeeds first and the author step is what fails closed.
type authorLookupFailingStore struct {
	*fakeFeedbackStore
}

func (a authorLookupFailingStore) MutedAuthorIDs(context.Context, uuid.UUID) (map[uuid.UUID]struct{}, error) {
	return nil, errors.New("postgres down")
}

func TestApplyFeedbackFilter_AuthorLookupFailureFailsClosed(t *testing.T) {
	s := &Service{feedback: authorLookupFailingStore{newFakeFeedbackStore()}}
	out, err := s.applyFeedbackFilter(context.Background(), uuid.New(), []HydratedPost{{ID: uuid.New(), AuthorID: uuid.New()}})
	if err == nil {
		t.Fatal("a failed author-mute lookup must be an ERROR, never an unfiltered page")
	}
	if out != nil {
		t.Fatalf("no rows may be returned alongside the error, got %d", len(out))
	}
}
