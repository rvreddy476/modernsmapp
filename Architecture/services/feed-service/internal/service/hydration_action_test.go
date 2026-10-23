package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The Action button through feed hydration (2026-10-23): post-service's
// batch decides the shape per viewer; the feed row carries it untouched, and
// a row with one is never cached, so a blocked link leaves the next page.

func TestHydrationCarriesTheActionAndDoesNotCacheIt(t *testing.T) {
	viewer := uuid.MustParse("71c4e9a3-5d2b-4f6e-9b08-4a7d1c3e5f07")
	withID := uuid.MustParse("0f6c2d8e-4b1a-4c3d-9e7f-1a2b3c4d5e11")
	plainID := uuid.MustParse("0f6c2d8e-4b1a-4c3d-9e7f-1a2b3c4d5e12")
	linkID := "5a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	author := uuid.MustParse(membersAuthorID)
	action := `{"id":"` + linkID + `","label":"apply_now","label_text":"Apply now","domain":"epfindia.gov.in","href":"/l/` + linkID + `"}`
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			calls++
			rows := map[string]json.RawMessage{
				withID.String(): json.RawMessage(`{"id":"` + withID.String() + `","author_id":"` + membersAuthorID + `","text":"hiring","visibility":"public",
					"content_type":"post","created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z","action":` + action + `}`),
				plainID.String(): json.RawMessage(`{"id":"` + plainID.String() + `","author_id":"` + membersAuthorID + `","text":"plain","visibility":"public",
					"content_type":"post","created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z"}`),
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
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
			_ = json.NewEncoder(w).Encode(map[string]any{
				membersAuthorID: map[string]any{"user_id": membersAuthorID, "display_name": "Ravi", "username": "ravi"},
			})
		case "/v1/media/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	s, mr := soundService(t, up.URL)
	items := []FeedItem{{PostID: withID, AuthorID: author, ContentType: "post"}, {PostID: plainID, AuthorID: author, ContentType: "post"}}
	page, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	for _, hp := range page {
		raw, _ := json.Marshal(hp)
		rows[hp.ID.String()] = asObject(t, raw)
	}
	got, _ := json.Marshal(rows[withID.String()]["action"])
	var want, have map[string]any
	_ = json.Unmarshal([]byte(action), &want)
	_ = json.Unmarshal(got, &have)
	if len(have) != len(want) || have["href"] != "/l/"+linkID || have["domain"] != "epfindia.gov.in" || have["label_text"] != "Apply now" {
		t.Fatalf("feed row action = %s, want %s", got, action)
	}
	if _, ok := rows[plainID.String()]["action"]; ok {
		t.Fatalf("a row without a button grew one: %v", rows[plainID.String()])
	}
	waitForCachedRow(t, mr, hydrationCacheKey(viewer, plainID))
	time.Sleep(50 * time.Millisecond)
	if mr.Exists(hydrationCacheKey(viewer, withID)) {
		t.Fatal("a row with an Action button was cached")
	}
	if _, err := s.HydratePosts(context.Background(), items, viewer); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("batch calls = %d; the row with a button must be asked again", calls)
	}
	if hydratedCacheable(HydratedPost{Action: json.RawMessage(`{}`)}) || !hydratedCacheable(HydratedPost{}) {
		t.Fatal("hydratedCacheable ignores the action")
	}
}
