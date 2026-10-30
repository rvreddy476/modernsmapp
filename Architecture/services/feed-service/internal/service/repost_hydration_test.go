package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Audit L2, end to end through HydratePosts: a repost of a graph-muted
// original author is dropped on the fresh path and on the cached path.
func TestHydratePosts_DropsRepostOfMutedOriginalAuthor(t *testing.T) {
	viewer, reposter, muted := uuid.New(), uuid.New(), uuid.New()
	post := uuid.New()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]json.RawMessage{
				post.String(): json.RawMessage(`{"id":"` + post.String() + `","author_id":"` + muted.String() + `","text":"x","visibility":"public",
					"content_type":"post","created_at":"2026-10-30T10:00:00Z","updated_at":"2026-10-30T10:00:00Z"}`),
			}})
		case "/v1/internal/graph/blocked-and-muted":
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{muted.String()}})
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
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		}
	}))
	defer up.Close()
	s, mr := soundService(t, up.URL)
	items := []FeedItem{{PostID: post, AuthorID: reposter, ContentType: "repost"}}

	got, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("fresh path: a repost of a muted original author was served")
	}
	waitForCachedRow(t, mr, hydrationCacheKey(viewer, post))
	got, err = s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("cached path: a repost of a muted original author was served")
	}
}
