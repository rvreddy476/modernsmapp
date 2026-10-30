package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// POST /v1/posts/batch is reachable by any signed-in user, so it must apply
// the same per-post rule as the direct read: a followers-only post goes to
// followers only, and everything outside public/unlisted/followers is the
// author's alone.

func TestBatchVisibilityAllows(t *testing.T) {
	cases := []struct {
		visibility string
		follows    bool
		want       bool
	}{
		{"public", false, true},
		{"", false, true},
		{"unlisted", false, true},
		{"followers", true, true},
		{"followers", false, false},
		{"FOLLOWERS", false, false},
		{"private", true, false},
		{"circle", true, false},
		{"trusted", true, false},
		{"staged", true, false},
		{"something-new", true, false},
	}
	for _, tc := range cases {
		if got := batchVisibilityAllows(tc.visibility, tc.follows); got != tc.want {
			t.Errorf("batchVisibilityAllows(%q, follows=%v) = %v, want %v", tc.visibility, tc.follows, got, tc.want)
		}
	}
}

func TestFollowedAuthorsOfFollowersPosts_AsksOnlyForFollowersPostsAndFailsClosed(t *testing.T) {
	viewer, followed, notFollowed, broken, publicOnly := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var mu sync.Mutex // lookups run concurrently
	asked := map[string]int{}
	// POST /v1/graph/relationships/batch (graph-service's raw map shape). A
	// target the graph cannot answer for is left out of the map, which the
	// caller must read as unresolved.
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/graph/relationships/batch" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := map[string]any{}
		mu.Lock()
		for _, other := range req.TargetIDs {
			asked[other]++
			switch other {
			case followed.String():
				out[other] = map[string]any{"follows": true}
			case notFollowed.String():
				out[other] = map[string]any{"follows": false}
			}
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(graph.Close)

	s := &Service{graphServiceURL: graph.URL, httpClient: graph.Client()}
	posts := []postgres.Post{
		{ID: uuid.New(), AuthorID: followed, Visibility: "followers"},
		{ID: uuid.New(), AuthorID: followed, Visibility: "followers"}, // same author: one lookup
		{ID: uuid.New(), AuthorID: notFollowed, Visibility: "followers"},
		{ID: uuid.New(), AuthorID: broken, Visibility: "followers"},
		{ID: uuid.New(), AuthorID: publicOnly, Visibility: "public"},
		{ID: uuid.New(), AuthorID: viewer, Visibility: "followers"}, // own post: never asked
	}

	got := s.followedAuthorsOfFollowersPosts(context.Background(), posts, &viewer)

	if !got[followed] {
		t.Error("a followed author's followers-only posts were refused")
	}
	if got[notFollowed] {
		t.Error("a non-followed author's followers-only posts were allowed")
	}
	if got[broken] {
		t.Error("a failed graph lookup allowed followers-only posts; it must fail closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if asked[followed.String()] != 1 {
		t.Errorf("followed author looked up %d times, want 1", asked[followed.String()])
	}
	if asked[publicOnly.String()] != 0 || asked[viewer.String()] != 0 {
		t.Error("looked up an author with no followers-only post on the page, or the viewer themself")
	}

	if anon := s.followedAuthorsOfFollowersPosts(context.Background(), posts, nil); len(anon) != 0 {
		t.Errorf("anonymous viewer follows %d authors, want 0", len(anon))
	}
}
