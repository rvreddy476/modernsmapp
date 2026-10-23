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

// Members-only posts through feed hydration (2026-10-23).
//
// post-service's batch decides the redaction per viewer, so the feed must
// (1) tell it who the viewer is — the X-User-Id header and viewer_id in the
// body — and (2) pass tier_required_id / body_redacted through to the feed
// row so a client can draw the lock, and (3) never cache a membership
// answer. Every feed surface (home, reels, videos, watch) hydrates here.

const (
	membersGatedPostID = "0f6c2d8e-4b1a-4c3d-9e7f-1a2b3c4d5e01"
	membersOpenPostID  = "0f6c2d8e-4b1a-4c3d-9e7f-1a2b3c4d5e02"
	membersAuthorID    = soundTestAuthorID
	membersTierID      = "7d3e9a1b-2c4f-4e6a-8b0d-5f1e3a7c9b03"
)

type membersBatchSpy struct {
	mu        sync.Mutex
	calls     int
	headers   []string
	viewerIDs []string
}

func membersUpstream(t *testing.T, spy *membersBatchSpy) *httptest.Server {
	t.Helper()
	gated := `{"id":"` + membersGatedPostID + `","author_id":"` + membersAuthorID + `","text":"","visibility":"public",
		"content_type":"long_video","title":"Episode 1","tier_required_id":"` + membersTierID + `","body_redacted":true,"duration_ms":61000,
		"created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z"}`
	open := `{"id":"` + membersOpenPostID + `","author_id":"` + membersAuthorID + `","text":"open body","visibility":"public",
		"content_type":"post","created_at":"2026-10-23T10:00:00Z","updated_at":"2026-10-23T10:00:00Z"}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			var body struct {
				IDs      []string `json:"ids"`
				ViewerID string   `json:"viewer_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			spy.mu.Lock()
			spy.calls++
			spy.headers = append(spy.headers, r.Header.Get("X-User-Id"))
			spy.viewerIDs = append(spy.viewerIDs, body.ViewerID)
			spy.mu.Unlock()
			rows := map[string]json.RawMessage{}
			for _, id := range body.IDs {
				switch id {
				case membersGatedPostID:
					rows[id] = json.RawMessage(gated)
				case membersOpenPostID:
					rows[id] = json.RawMessage(open)
				}
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
}

func TestHydrationForwardsTheViewerAndCarriesTheMembersGate(t *testing.T) {
	viewer := uuid.MustParse("71c4e9a3-5d2b-4f6e-9b08-4a7d1c3e5f07")
	gatedID, openID := uuid.MustParse(membersGatedPostID), uuid.MustParse(membersOpenPostID)
	author := uuid.MustParse(membersAuthorID)
	spy := &membersBatchSpy{}
	up := membersUpstream(t, spy)
	defer up.Close()
	s, mr := soundService(t, up.URL)
	items := []FeedItem{
		{PostID: gatedID, AuthorID: author, ContentType: "long_video"},
		{PostID: openID, AuthorID: author, ContentType: "post"},
	}

	page, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	// (1) The viewer reaches post-service, on the header the batch reads.
	spy.mu.Lock()
	if spy.calls != 1 || spy.headers[0] != viewer.String() || spy.viewerIDs[0] != viewer.String() {
		t.Fatalf("batch calls=%d X-User-Id=%v viewer_id=%v; want the viewer %s", spy.calls, spy.headers, spy.viewerIDs, viewer)
	}
	spy.mu.Unlock()

	// (2) The feed row says what post-service said.
	rows := map[string]map[string]any{}
	for _, hp := range page {
		raw, _ := json.Marshal(hp)
		rows[hp.ID.String()] = asObject(t, raw)
	}
	gated, open := rows[membersGatedPostID], rows[membersOpenPostID]
	if gated == nil || open == nil {
		t.Fatalf("page lost a row: %v", rows)
	}
	if gated["tier_required_id"] != membersTierID || gated["body_redacted"] != true {
		t.Fatalf("gated feed row: tier_required_id=%v body_redacted=%v", gated["tier_required_id"], gated["body_redacted"])
	}
	if gated["duration_ms"] != float64(61000) {
		t.Fatalf("gated feed row lost duration_ms: %v", gated["duration_ms"])
	}
	if _, ok := open["duration_ms"]; ok {
		t.Fatalf("an open row grew duration_ms: %v", open)
	}
	if _, ok := open["tier_required_id"]; ok {
		t.Fatalf("an open row grew tier_required_id: %v", open)
	}
	if _, ok := open["body_redacted"]; ok {
		t.Fatalf("an open row grew body_redacted: %v", open)
	}

	// (3) The open row is cached; the gated one never is, so the next page
	// asks post-service again for it.
	waitForCachedRow(t, mr, hydrationCacheKey(viewer, openID))
	time.Sleep(50 * time.Millisecond)
	if mr.Exists(hydrationCacheKey(viewer, gatedID)) {
		t.Fatal("a members-only row was written to the hydration cache")
	}
	if _, err := s.HydratePosts(context.Background(), items, viewer); err != nil {
		t.Fatalf("hydrate again: %v", err)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.calls != 2 {
		t.Fatalf("batch calls = %d after the second page; the gated row must be re-asked", spy.calls)
	}
}

func TestHydratedCacheableRefusesMembershipAnswers(t *testing.T) {
	for _, tc := range []struct {
		hp   HydratedPost
		want bool
	}{
		{HydratedPost{}, true},
		{HydratedPost{TierRequiredID: membersTierID}, false},
		{HydratedPost{TierRequiredID: membersTierID, BodyRedacted: true}, false},
		{HydratedPost{BodyRedacted: true}, false},
	} {
		if got := hydratedCacheable(tc.hp); got != tc.want {
			t.Errorf("hydratedCacheable(%+v) = %v, want %v", tc.hp, got, tc.want)
		}
	}
}
