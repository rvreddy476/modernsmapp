package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Audit MEDIUM 8 (2026-10-30): the sound page reads its view counts in one
// batched analytics call, not one call per reel.
func TestSoundPageViewCountsAreOneBatchedCall(t *testing.T) {
	author := uuid.New()
	posts := make([]postgres.Post, 0, 50)
	for i := 0; i < 50; i++ {
		posts = append(posts, postgres.Post{ID: uuid.New(), AuthorID: author, ContentType: "flick", Visibility: "public", ReviewStatus: "approved"})
	}
	var calls atomic.Int32
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			ContentIDs []string `json:"content_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		asked.Add(int32(len(req.ContentIDs)))
		data := map[string]any{}
		for i, id := range req.ContentIDs {
			data[id] = map[string]any{"views_display": 100 + i}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	s := newViewCountTestService(srv.URL)
	s.mediaStates = fakeMediaStates{}
	s.soundCounts = func(context.Context, []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error) {
		return map[uuid.UUID]*scylla.Counts{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	details, err := s.hydrateSoundPosts(ctx, &author, posts)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != len(posts) {
		t.Fatalf("hydrated %d of %d rows", len(details), len(posts))
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("analytics was called %d times for one page, want 1", n)
	}
	if n := asked.Load(); n != int32(len(posts)) {
		t.Fatalf("the call asked for %d ids, want %d", n, len(posts))
	}
	for _, d := range details {
		if d.ViewCount < 100 {
			t.Fatalf("post %s: view count %d, want the batched answer", d.Post.ID, d.ViewCount)
		}
	}
}
