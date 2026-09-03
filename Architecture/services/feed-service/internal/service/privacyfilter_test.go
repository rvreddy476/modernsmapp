package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Private accounts on the feed tail. Same policy family as
// blocksafety_test.go: a filter that cannot be resolved is an ERROR, never
// an unfiltered page, and a graph answer is decoded strictly.

type canReq struct {
	ViewerID  string   `json:"viewer_id"`
	Action    string   `json:"action"`
	TargetIDs []string `json:"target_ids"`
}

func fakeCanServer(t *testing.T, allow map[string]bool, calls *int32, seen *[]canReq) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/graph/can" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != "test-internal" {
			t.Errorf("internal key = %q", got)
		}
		var req canReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		atomic.AddInt32(calls, 1)
		if seen != nil {
			*seen = append(*seen, req)
		}
		if len(req.TargetIDs) > 100 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data := map[string]bool{}
		for _, id := range req.TargetIDs {
			data[id] = allow[id]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func postBy(author uuid.UUID) HydratedPost {
	return HydratedPost{ID: uuid.New(), AuthorID: author}
}

func TestAuthorPrivacyFilter_DropsPrivateAuthorsKeepsPublicAndSelf(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	viewer, public, private := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	var seen []canReq
	srv := fakeCanServer(t, map[string]bool{public.String(): true}, &calls, &seen)
	defer srv.Close()
	s := newFeedServiceWithGraph(srv.URL)

	in := []HydratedPost{postBy(public), postBy(private), postBy(viewer), postBy(public)}
	out, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, in)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d posts, want 3 (private author dropped)", len(out))
	}
	for _, p := range out {
		if p.AuthorID == private {
			t.Fatalf("private author's post survived")
		}
	}
	// One call, distinct authors only, the viewer's own id never sent.
	if calls != 1 || len(seen[0].TargetIDs) != 2 || seen[0].Action != "view_posts" || seen[0].ViewerID != viewer.String() {
		t.Fatalf("request = %+v (calls=%d)", seen, calls)
	}
}

// A repost is judged by the ORIGINAL author, not the reposter.
func TestAuthorPrivacyFilter_RepostOfPrivateAuthorIsDropped(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	viewer, reposter, private := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeCanServer(t, map[string]bool{reposter.String(): true}, &calls, nil)
	defer srv.Close()
	s := newFeedServiceWithGraph(srv.URL)

	rp := postBy(private)
	rp.IsRepost = true
	rp.RepostedBy = &reposter
	out, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, []HydratedPost{rp})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("repost of a private account's post was served")
	}
}

func TestAuthorPrivacyFilter_FailsClosed(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	for name, handler := range map[string]http.HandlerFunc{
		"non-200":   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"malformed": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("nope")) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			s := newFeedServiceWithGraph(srv.URL)
			if _, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, []HydratedPost{postBy(author)}); err == nil {
				t.Fatalf("%s returned no error — the feed would serve unfiltered", name)
			}
		})
	}
	t.Run("unanswered author is dropped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]bool{}})
		}))
		defer srv.Close()
		s := newFeedServiceWithGraph(srv.URL)
		out, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, []HydratedPost{postBy(author)})
		if err != nil {
			t.Fatalf("filter: %v", err)
		}
		if len(out) != 0 {
			t.Fatalf("an author the graph did not answer for was served")
		}
	})
}

func TestAuthorPrivacyFilter_ChunksAtGraphLimit(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	viewer := uuid.New()
	allow := map[string]bool{}
	posts := make([]HydratedPost, 0, 230)
	for i := 0; i < 230; i++ {
		a := uuid.New()
		allow[a.String()] = true
		posts = append(posts, postBy(a))
	}
	var calls int32
	srv := fakeCanServer(t, allow, &calls, nil)
	defer srv.Close()
	s := newFeedServiceWithGraph(srv.URL)
	out, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, posts)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if calls != 3 || len(out) != 230 {
		t.Fatalf("calls=%d out=%d", calls, len(out))
	}
}

func TestAuthorPrivacyFilter_CachesPerViewerAndAuthorForThreeSeconds(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	viewer, other, author := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeCanServer(t, map[string]bool{author.String(): true}, &calls, nil)
	defer srv.Close()
	s := newFeedServiceWithGraph(srv.URL)
	now := time.Now()
	s.apNow = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, []HydratedPost{postBy(author)}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d after cached repeats, want 1", calls)
	}
	if _, err := s.applyAuthorPrivacyFilter(context.Background(), other, []HydratedPost{postBy(author)}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d; a different viewer must not share the answer", calls)
	}
	now = now.Add(authorPrivacyCacheTTL + time.Millisecond)
	if _, err := s.applyAuthorPrivacyFilter(context.Background(), viewer, []HydratedPost{postBy(author)}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d; entry did not expire", calls)
	}
}
