package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Private accounts / comments audience — the account-level gate.
//
// These drive the real HTTP client against a fake graph-service `can`
// endpoint, the same way thread_audience_test.go drives the relationship
// lookup, so the wire contract (body shape, header, envelope) is what is
// under test — not a stub of our own client.

type canRequest struct {
	ViewerID  string   `json:"viewer_id"`
	Action    string   `json:"action"`
	TargetIDs []string `json:"target_ids"`
}

// fakeGraphCan answers /v1/internal/graph/can from a fixed allow set and
// records every request it saw.
func fakeGraphCan(t *testing.T, allow map[string]bool, calls *int32, seen *[]canRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/graph/can" || r.Method != http.MethodPost {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != "test-key" {
			t.Errorf("internal key header = %q", got)
		}
		var req canRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		atomic.AddInt32(calls, 1)
		if seen != nil {
			*seen = append(*seen, req)
		}
		if len(req.TargetIDs) > 100 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"BATCH_TOO_LARGE"}}`))
			return
		}
		data := map[string]bool{}
		for _, id := range req.TargetIDs {
			if id == req.ViewerID {
				data[id] = true
				continue
			}
			data[id] = allow[id]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func newGatedService(url string) *Service {
	return &Service{graphServiceURL: url, internalServiceKey: "test-key", httpClient: http.DefaultClient}
}

func TestCanViewPosts_PublicAllowedPrivateDenied(t *testing.T) {
	viewer, public, private := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{public.String(): true, private.String(): false}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)

	got := s.canViewPosts(context.Background(), &viewer, []uuid.UUID{public, private})
	if !got[public] {
		t.Fatalf("public author denied")
	}
	if got[private] {
		t.Fatalf("private author allowed to a stranger")
	}
}

// A viewer with no identity is asked about as the nil UUID — a stranger to
// every private account — rather than skipping the gate.
func TestCanViewPosts_AnonymousIsAStranger(t *testing.T) {
	private := uuid.New()
	var seen []canRequest
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{}, &calls, &seen)
	defer srv.Close()
	s := newGatedService(srv.URL)

	if s.canViewPosts(context.Background(), nil, []uuid.UUID{private})[private] {
		t.Fatalf("anonymous viewer saw a private author")
	}
	if len(seen) != 1 || seen[0].ViewerID != uuid.Nil.String() || seen[0].Action != "view_posts" {
		t.Fatalf("unexpected request: %+v", seen)
	}
}

// Every failure shape is a denial: a non-200, a transport error, and an
// author the graph did not answer for.
func TestCanViewPosts_FailsClosed(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()

	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		if newGatedService(srv.URL).canViewAuthor(context.Background(), &viewer, author) {
			t.Fatalf("500 from graph-service allowed the read")
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.Close()
		if newGatedService(srv.URL).canViewAuthor(context.Background(), &viewer, author) {
			t.Fatalf("unreachable graph-service allowed the read")
		}
	})
	t.Run("missing target in answer", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]bool{}})
		}))
		defer srv.Close()
		if newGatedService(srv.URL).canViewAuthor(context.Background(), &viewer, author) {
			t.Fatalf("an unanswered author was allowed")
		}
	})
}

// No graph configured keeps the dev-rig behaviour the follow check has
// always had: no policy, no gate.
func TestCanViewPosts_NoGraphConfiguredIsPermissive(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	s := &Service{}
	if !s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("empty GRAPH_SERVICE_URL denied; dev rigs would lose every post")
	}
}

// One page of 250 distinct authors costs three calls, never one oversized
// call the graph would reject with BATCH_TOO_LARGE.
func TestCanViewPosts_ChunksAtOneHundred(t *testing.T) {
	viewer := uuid.New()
	allow := map[string]bool{}
	authors := make([]uuid.UUID, 250)
	for i := range authors {
		authors[i] = uuid.New()
		allow[authors[i].String()] = true
	}
	var seen []canRequest
	var calls int32
	srv := fakeGraphCan(t, allow, &calls, &seen)
	defer srv.Close()

	got := newGatedService(srv.URL).canViewPosts(context.Background(), &viewer, authors)
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	for _, r := range seen {
		if len(r.TargetIDs) > 100 {
			t.Fatalf("batch of %d exceeds the graph limit", len(r.TargetIDs))
		}
	}
	for _, a := range authors {
		if !got[a] {
			t.Fatalf("author %s lost in chunking", a)
		}
	}
}

// Answers are reused for 3s per (viewer, action, author); a second
// hydration of the same page costs no round trip, and the cache expires.
func TestCanViewPosts_ShortCachePerViewerAndAuthor(t *testing.T) {
	viewer, other, author := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{author.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	now := time.Now()
	s.privacyNow = func() time.Time { return now }

	s.canViewAuthor(context.Background(), &viewer, author)
	s.canViewAuthor(context.Background(), &viewer, author)
	if calls != 1 {
		t.Fatalf("calls = %d after a cached repeat, want 1", calls)
	}
	// A different viewer is a different answer.
	s.canViewAuthor(context.Background(), &other, author)
	if calls != 2 {
		t.Fatalf("calls = %d, second viewer must not share the cache", calls)
	}
	// A different action is a different answer too.
	s.canComment(context.Background(), viewer, author)
	if calls != 3 {
		t.Fatalf("calls = %d, comment must not reuse the view answer", calls)
	}
	now = now.Add(privacyGateCacheTTL + time.Millisecond)
	s.canViewAuthor(context.Background(), &viewer, author)
	if calls != 4 {
		t.Fatalf("calls = %d, entry did not expire", calls)
	}
}

// Failures are not cached: the request after a blip asks again.
func TestCanViewPosts_ErrorsAreNotCached(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	var fail atomic.Bool
	fail.Store(true)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]bool{author.String(): true}})
	}))
	defer srv.Close()
	s := newGatedService(srv.URL)
	if s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("502 allowed")
	}
	fail.Store(false)
	if !s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("recovered graph still denied — the failure was cached")
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

// viewerMayViewPost: a PUBLIC post by a PRIVATE author is hidden from a
// stranger; the author still sees their own; a public author's public post
// stays visible. This is the direct-link (GetPost) gate.
func TestViewerMayViewPost_PrivateAccountHidesPublicPost(t *testing.T) {
	stranger, follower, author := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		var req canRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// graph-service resolves the follow edge itself; the fake mirrors it.
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]bool{author.String(): req.ViewerID == follower.String()}})
	}))
	defer srv.Close()
	s := newGatedService(srv.URL)
	post := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "public"}

	if s.viewerMayViewPost(context.Background(), post, &stranger) {
		t.Fatalf("stranger read a private account's post")
	}
	if s.viewerMayViewPost(context.Background(), post, nil) {
		t.Fatalf("anonymous viewer read a private account's post")
	}
	if !s.viewerMayViewPost(context.Background(), post, &follower) {
		t.Fatalf("follower denied")
	}
	before := calls
	if !s.viewerMayViewPost(context.Background(), post, &author) {
		t.Fatalf("author denied their own post")
	}
	if calls != before {
		t.Fatalf("self-view made a graph call")
	}
}

// ── Account control (auth-service deactivate/delete/purge lifecycle) ───────
//
// post_hidden_authors is post-service's mirror of "this account is deactivated
// or inside the 30-day deletion recovery window" (see
// internal/store/postgres/purge.go and internal/purge). denyHiddenAuthors
// is the seam canViewPosts uses to consult it; these tests exercise that
// seam with a fake rather than a live database, mirroring how canViewPosts
// itself is tested against an httptest fake graph-service.

type fakeHiddenAuthors struct {
	hidden map[uuid.UUID]bool
	err    error
	calls  int
	seen   []uuid.UUID
}

func (f *fakeHiddenAuthors) AnyHidden(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	f.calls++
	f.seen = append(f.seen, ids...)
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if f.hidden[id] {
			out[id] = true
		}
	}
	return out, nil
}

// A hidden author is denied even though the graph would allow the read —
// the account-lifecycle gate overrides the follow/privacy answer, not the
// other way around.
func TestCanViewPosts_HiddenAuthorDeniedDespiteGraphAllowing(t *testing.T) {
	viewer, hiddenAuthor, normalAuthor := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{hiddenAuthor.String(): true, normalAuthor.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	s.hiddenAuthors = &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{hiddenAuthor: true}}

	got := s.canViewPosts(context.Background(), &viewer, []uuid.UUID{hiddenAuthor, normalAuthor})
	if got[hiddenAuthor] {
		t.Fatalf("hidden author allowed even though graph said yes")
	}
	if !got[normalAuthor] {
		t.Fatalf("normal author denied")
	}
}

// Unhide (deletion cancelled / reactivated — post_hidden_authors row removed)
// restores exactly the graph's own answer; the gate has nothing further to
// say once the row is gone.
func TestCanViewPosts_UnhideRestoresGraphAnswer(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{author.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	fake := &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{author: true}}
	s.hiddenAuthors = fake

	if s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("hidden author allowed")
	}

	// Simulate user.reactivated / user.deletion_cancelled: the row is gone.
	delete(fake.hidden, author)
	if !s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("unhidden author still denied")
	}
}

// Self-view is never gated by hidden — the deactivated user themself is not
// asked about, matching graphCan's existing self-view short circuit.
func TestCanViewPosts_SelfViewSkipsHiddenLookup(t *testing.T) {
	viewer := uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	fake := &fakeHiddenAuthors{hidden: map[uuid.UUID]bool{viewer: true}}
	s.hiddenAuthors = fake

	if !s.canViewAuthor(context.Background(), &viewer, viewer) {
		t.Fatalf("self-view denied")
	}
	if fake.calls != 0 {
		t.Fatalf("hidden lookup ran for a self-view target, calls=%d", fake.calls)
	}
}

// A nil hiddenAuthors (dev rigs with no store wired, and every existing test
// in this file that constructs Service{} directly) must not change behavior
// — canViewPosts falls back to the graph's own answer.
func TestCanViewPosts_NilHiddenStoreIsANoOp(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{author.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL) // hiddenAuthors left nil
	if !s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("nil hidden store changed the graph's allow into a deny")
	}
}

// Fail-closed: a lookup error denies rather than silently falling back to
// the graph's answer, matching this file's stated "Failure shape: DENY".
func TestCanViewPosts_HiddenLookupErrorDenies(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{author.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	s.hiddenAuthors = &fakeHiddenAuthors{err: errors.New("db down")}

	if s.canViewAuthor(context.Background(), &viewer, author) {
		t.Fatalf("hidden-lookup error must deny, not fall back to the graph's allow")
	}
}

func TestCanComment_FriendsOnlyDeniesStranger(t *testing.T) {
	stranger, friend, author := uuid.New(), uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req canRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Action != "comment" {
			t.Errorf("action = %q", req.Action)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]bool{author.String(): req.ViewerID == friend.String()}})
	}))
	defer srv.Close()
	s := newGatedService(srv.URL)
	if s.canComment(context.Background(), stranger, author) {
		t.Fatalf("stranger may comment on a friends-only account")
	}
	if !s.canComment(context.Background(), friend, author) {
		t.Fatalf("friend denied")
	}
	if !s.canComment(context.Background(), author, author) {
		t.Fatalf("author denied on their own post")
	}
}
