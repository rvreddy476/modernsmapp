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

// Audit L6: the new-posts pill counts only what the viewer can open, and
// the check does not pay for a rendered page.
func TestOpenableForPill_CountsOnlyOpenablePosts(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	open, removed, processing := uuid.New(), uuid.New(), uuid.New()
	var renderCalls atomic.Int32
	row := func(id uuid.UUID, extra string) json.RawMessage {
		return json.RawMessage(`{"id":"` + id.String() + `","author_id":"` + author.String() + `","text":"x","visibility":"public",
			"content_type":"post","created_at":"2026-10-30T10:00:00Z","updated_at":"2026-10-30T10:00:00Z"` + extra + `}`)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			// `removed` was taken down: post-service's batch drops it.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]json.RawMessage{
				open.String():       row(open, ""),
				processing.String(): row(processing, `,"is_processing":true`),
			}})
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
		case "/v1/profiles/batch", "/v1/media/batch":
			renderCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	s, _ := soundService(t, up.URL)
	now := time.Now()
	items := []FeedItem{
		{PostID: open, AuthorID: author, CreatedAt: now, ContentType: "post"},
		{PostID: removed, AuthorID: author, CreatedAt: now.Add(time.Second), ContentType: "post"},
		{PostID: processing, AuthorID: author, CreatedAt: now.Add(2 * time.Second), ContentType: "post"},
	}
	got, err := s.openableForPill(context.Background(), viewer, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PostID != open {
		t.Fatalf("pill counted %d posts, want only the one the viewer can open", len(got))
	}
	if renderCalls.Load() != 0 {
		t.Fatalf("the pill's visibility check fetched render data %d times", renderCalls.Load())
	}
}

func TestOpenableForPill_HydrationFailureIsAnError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer up.Close()
	s, _ := soundService(t, up.URL)
	if _, err := s.openableForPill(context.Background(), uuid.New(), []FeedItem{{PostID: uuid.New(), AuthorID: uuid.New()}}); err == nil {
		t.Fatal("an unverifiable pill count was served")
	}
}
