package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// feed-33: post-service's batch refuses more than 100 ids (400). The pill
// reads up to 201 rows, so a viewer with more than 100 new posts got a 500
// from GET /v1/feed/delta on every poll. The batch call is now sent in
// groups of at most 100.

// cappedPostService is a post-service stub that enforces the real cap and
// records the size of every batch request. Ids in `gone` are left out, as
// post-service leaves out a deleted or private post.
type cappedPostService struct {
	mu     sync.Mutex
	sizes  []int
	gone   map[string]bool
	author uuid.UUID
	// failFrom, when > 0, answers 503 from that request on (1-based).
	failFrom int
}

func (c *cappedPostService) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			var req struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			c.mu.Lock()
			c.sizes = append(c.sizes, len(req.IDs))
			n := len(c.sizes)
			c.mu.Unlock()
			if len(req.IDs) > 100 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"INVALID_REQUEST","message":"Maximum 100 IDs per request"}}`))
				return
			}
			if c.failFrom > 0 && n >= c.failFrom {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			data := map[string]json.RawMessage{}
			for _, id := range req.IDs {
				if c.gone[id] {
					continue
				}
				data[id] = json.RawMessage(`{"id":"` + id + `","author_id":"` + c.author.String() + `","text":"x","visibility":"public",
					"content_type":"post","created_at":"2026-11-16T10:00:00Z","updated_at":"2026-11-16T10:00:00Z"}`)
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
		default:
			http.NotFound(w, r)
		}
	}
}

func (c *cappedPostService) requestSizes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.sizes...)
}

func pillItems(n int, author uuid.UUID) []FeedItem {
	now := time.Now()
	items := make([]FeedItem, n)
	for i := range items {
		items[i] = FeedItem{PostID: uuid.New(), AuthorID: author, CreatedAt: now.Add(-time.Duration(i) * time.Second), ContentType: "post"}
	}
	return items
}

// The finding's acceptance test: 150 fresh rows against a post-service that
// answers 400 above 100 ids. No error, and all 150 are counted.
func TestOpenableForPill_MoreThanAHundredFreshRows(t *testing.T) {
	post := &cappedPostService{author: uuid.New()}
	up := httptest.NewServer(post.handler(t))
	defer up.Close()
	s, _ := soundService(t, up.URL)

	items := pillItems(150, post.author)
	got, err := s.openableForPill(context.Background(), uuid.New(), items)
	if err != nil {
		t.Fatalf("150 fresh rows failed the pill (500 on /v1/feed/delta before the fix): %v", err)
	}
	if len(got) != 150 {
		t.Fatalf("pill counted %d of 150 openable posts", len(got))
	}
	sizes := post.requestSizes()
	if len(sizes) != 2 || sizes[0] != 100 || sizes[1] != 50 {
		t.Fatalf("batch requests carried %v ids, want [100 50]", sizes)
	}
}

// The delta's own ceiling: 201 rows (deltaMaxCount+1) is three requests,
// and a post post-service leaves out is still left out of the count.
func TestOpenableForPill_DeltaCeilingAndDroppedPosts(t *testing.T) {
	post := &cappedPostService{author: uuid.New(), gone: map[string]bool{}}
	up := httptest.NewServer(post.handler(t))
	defer up.Close()
	s, _ := soundService(t, up.URL)

	items := pillItems(deltaMaxCount+1, post.author)
	for _, i := range []int{3, 120, 200} { // one in each group
		post.gone[items[i].PostID.String()] = true
	}
	got, err := s.openableForPill(context.Background(), uuid.New(), items)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != deltaMaxCount+1-3 {
		t.Fatalf("pill counted %d, want %d (three posts are gone)", len(got), deltaMaxCount+1-3)
	}
	for _, it := range got {
		if post.gone[it.PostID.String()] {
			t.Fatal("a post post-service left out was counted")
		}
	}
	if sizes := post.requestSizes(); len(sizes) != 3 || sizes[0] != 100 || sizes[1] != 100 || sizes[2] != 1 {
		t.Fatalf("batch requests carried %v ids, want [100 100 1]", sizes)
	}
}

func TestPostBatch_GroupSizes(t *testing.T) {
	for n, want := range map[int][]int{0: nil, 1: {1}, 100: {100}, 101: {100, 1}, 250: {100, 100, 50}} {
		post := &cappedPostService{author: uuid.New()}
		up := httptest.NewServer(post.handler(t))
		s, _ := soundService(t, up.URL)
		ids := make([]string, n)
		for i := range ids {
			ids[i] = uuid.NewString()
		}
		got, err := s.postBatch(context.Background(), uuid.New(), ids)
		up.Close()
		if err != nil {
			t.Fatalf("%d ids: %v", n, err)
		}
		if len(got) != n {
			t.Fatalf("%d ids: %d posts came back", n, len(got))
		}
		sizes := post.requestSizes()
		if len(sizes) != len(want) {
			t.Fatalf("%d ids: requests %v, want %v", n, sizes, want)
		}
		for i := range want {
			if sizes[i] != want[i] {
				t.Fatalf("%d ids: requests %v, want %v", n, sizes, want)
			}
		}
	}
}

// A failed group fails the call. A partial answer would read as "the posts
// of the failed group are not visible", and every caller filters on that:
// the pill would undercount, a page would silently lose rows.
func TestPostBatch_AFailedGroupFailsTheWholeCall(t *testing.T) {
	post := &cappedPostService{author: uuid.New(), failFrom: 2}
	up := httptest.NewServer(post.handler(t))
	defer up.Close()
	s, _ := soundService(t, up.URL)
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	got, err := s.postBatch(context.Background(), uuid.New(), ids)
	if err == nil {
		t.Fatalf("the second group failed (503) and %d posts were returned as the whole answer", len(got))
	}
	if _, err := s.openableForPill(context.Background(), uuid.New(), pillItems(150, post.author)); err == nil {
		t.Fatal("a pill count was served from a partial batch")
	}
}
