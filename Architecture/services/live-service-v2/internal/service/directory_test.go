package service

// The clients of the other services (directory.go), against httptest
// servers that answer in the shapes the real routes do: paths, the internal
// key, and what a failure looks like to the caller.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const dirKey = "dir-internal-key"

func keyed(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != dirKey {
			http.Error(w, `{"error":{"code":"UNAUTHORIZED"}}`, http.StatusUnauthorized)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPProfiles(t *testing.T) {
	asha, ben, ghost := uuid.New(), uuid.New(), uuid.New()
	var asked [][]string
	var sawUser string
	srv := keyed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/profiles/batch" {
			http.NotFound(w, r)
			return
		}
		sawUser = r.Header.Get("X-User-Id")
		var body struct {
			UserIDs []string `json:"user_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		asked = append(asked, body.UserIDs)
		// identity-profile answers a raw map, a user without a visible
		// profile simply missing from it.
		out := map[string]any{}
		for _, id := range body.UserIDs {
			switch id {
			case asha.String():
				out[id] = map[string]any{"user_id": id, "username": "asha", "display_name": "Asha Rao", "avatar_media_id": uuid.NewString(), "avatar_url": "/v1/media/a.webp", "is_private": false}
			case ben.String():
				out[id] = map[string]any{"user_id": id, "display_name": "Ben"} // no username, no avatar
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	c := NewHTTPProfiles(srv.URL+"/", dirKey)
	got, err := c.Profiles(ctx, []uuid.UUID{asha, ben, ghost})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]Profile{
		asha: {Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/a.webp"},
		ben:  {Name: "Ben"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profiles: %+v", got)
	}
	if sawUser != "" {
		t.Fatalf("the lookup carried a viewer (%q); it must not, the answer is cached for everyone", sawUser)
	}
	// More users than one batch: split, nothing lost.
	asked = nil
	many := make([]uuid.UUID, 107)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := c.Profiles(ctx, many); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || len(asked[0]) != 100 || len(asked[1]) != 7 {
		t.Fatalf("batches: %d", len(asked))
	}
	// Failures are errors, never an empty answer.
	if _, err := NewHTTPProfiles(srv.URL, "wrong-key").Profiles(ctx, []uuid.UUID{asha}); err == nil {
		t.Fatal("a 401 read as an empty answer")
	}
	if NewHTTPProfiles("  ", dirKey) != nil {
		t.Fatal("an empty base URL built a client")
	}
}

func TestHTTPCategories(t *testing.T) {
	body := `{"data":[{"id":"comedy","slug":"comedy","label":"Comedy","kind":"all"},{"id":"music","label":"Music"},{"id":"","slug":""},{"id":"kids","slug":"kids","label":"Kids","kind":"long"}]}`
	status := http.StatusOK
	srv := keyed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/posts/categories" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	c := NewHTTPCategories(srv.URL, dirKey)
	got, err := c.Categories(ctx)
	want := []Category{{Slug: "comedy", Label: "Comedy"}, {Slug: "music", Label: "Music"}, {Slug: "kids", Label: "Kids"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("categories: %+v %v", got, err)
	}
	// An empty or failed answer is an error: it must never replace a
	// taxonomy seen before with "nothing is valid".
	body = `{"data":[]}`
	if _, err := c.Categories(ctx); err == nil {
		t.Fatal("an empty taxonomy was accepted")
	}
	body, status = `{"error":{"code":"INTERNAL_ERROR"}}`, http.StatusInternalServerError
	if _, err := c.Categories(ctx); err == nil {
		t.Fatal("a 500 was accepted")
	}
	if NewHTTPCategories("", dirKey) != nil {
		t.Fatal("an empty base URL built a client")
	}
}

func TestHTTPFollowing(t *testing.T) {
	viewer := uuid.New()
	followed, both := uuid.New(), uuid.New()
	owners := []uuid.UUID{both}
	for i := 0; i < subscribedOwnersPage+3; i++ {
		owners = append(owners, uuid.New())
	}
	graphFail, postFail := false, false
	var postPages []string
	graph := keyed(t, func(w http.ResponseWriter, r *http.Request) {
		if graphFail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/v1/graph/"+viewer.String()+"/following-ids" || r.URL.Query().Get("limit") != "500" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []string{followed.String(), both.String()}, "count": 2}})
	})
	post := keyed(t, func(w http.ResponseWriter, r *http.Request) {
		if postFail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/internal/users/"+viewer.String()+"/subscribed-owner-ids" {
			http.NotFound(w, r)
			return
		}
		after := r.URL.Query().Get("after")
		postPages = append(postPages, after)
		start := 0
		if after != "" {
			for i, id := range owners {
				if id.String() == after {
					start = i + 1
				}
			}
		}
		end := start + subscribedOwnersPage
		if end > len(owners) {
			end = len(owners)
		}
		page := []string{}
		for _, id := range owners[start:end] {
			page = append(page, id.String())
		}
		next := ""
		if len(page) > 0 {
			next = page[len(page)-1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"owner_ids": page, "next_after": next, "has_more": len(page) == subscribedOwnersPage}})
	})
	c := NewHTTPFollowing(graph.URL, post.URL, dirKey)
	got, err := c.FollowedCreatorIDs(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	// Follows and subscriptions, each id once.
	if len(got) != 1+len(owners) {
		t.Fatalf("%d ids, want %d (the one in both lists once)", len(got), 1+len(owners))
	}
	seen := map[uuid.UUID]int{}
	for _, id := range got {
		seen[id]++
	}
	if seen[followed] != 1 || seen[both] != 1 || seen[owners[len(owners)-1]] != 1 {
		t.Fatalf("missing or repeated ids: followed=%d both=%d last owner=%d", seen[followed], seen[both], seen[owners[len(owners)-1]])
	}
	if len(postPages) != 2 || postPages[0] != "" || postPages[1] != owners[subscribedOwnersPage-1].String() {
		t.Fatalf("subscription pages asked: %d", len(postPages))
	}
	// Either half failing fails the whole list: half a list would hide
	// streams silently.
	graphFail = true
	if _, err := c.FollowedCreatorIDs(ctx, viewer); err == nil || !strings.Contains(err.Error(), "following-ids") {
		t.Fatalf("graph down: %v", err)
	}
	graphFail, postFail = false, true
	if _, err := c.FollowedCreatorIDs(ctx, viewer); err == nil || !strings.Contains(err.Error(), "subscribed-owner-ids") {
		t.Fatalf("post-service down: %v", err)
	}
	if NewHTTPFollowing(graph.URL, "", dirKey) != nil || NewHTTPFollowing("", post.URL, dirKey) != nil {
		t.Fatal("a Following source was built with only one of its two services")
	}
}

func TestHTTPGraphRelationshipsBatch(t *testing.T) {
	viewer := uuid.New()
	var sizes []int
	drop := ""
	srv := keyed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/graph/relationships/batch" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ViewerID != viewer.String() || len(body.TargetIDs) > relationshipBatchMax {
			http.Error(w, `{"error":{"code":"BATCH_TOO_LARGE"}}`, http.StatusBadRequest)
			return
		}
		sizes = append(sizes, len(body.TargetIDs))
		out := map[string]any{}
		for i, id := range body.TargetIDs {
			if id == drop {
				continue
			}
			out[id] = map[string]bool{"follows": i%2 == 0, "blocked": false, "blocked_by": i == 1}
		}
		_ = json.NewEncoder(w).Encode(out) // a raw map, no envelope
	})
	c := NewHTTPGraphClient(srv.URL, dirKey)
	creators := make([]uuid.UUID, relationshipBatchMax+5)
	for i := range creators {
		creators[i] = uuid.New()
	}
	got, err := c.Relationships(ctx, viewer, creators)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(creators) || fmt.Sprint(sizes) != fmt.Sprint([]int{relationshipBatchMax, 5}) {
		t.Fatalf("%d answers in batches %v", len(got), sizes)
	}
	if !got[creators[0]].Follows || got[creators[0]].Blocked || !got[creators[1]].Blocked {
		t.Fatalf("relationships: %+v %+v (blocked_by is a block too)", got[creators[0]], got[creators[1]])
	}
	// A creator missing from the answer cannot be ruled out as a block: the
	// whole call fails.
	drop = creators[3].String()
	if _, err := c.Relationships(ctx, viewer, creators[:10]); err == nil {
		t.Fatal("a missing entry was read as no block")
	}
	if _, err := NewHTTPGraphClient("", dirKey).Relationships(ctx, viewer, creators[:1]); err == nil {
		t.Fatal("an unconfigured client answered")
	}
}
