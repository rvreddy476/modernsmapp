package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// profile-service publishes `avatar_url` next to `avatar_media_id`. The
// hydration path's own publicProfile struct decoded only the id, so the URL
// was dropped on the floor and every feed row handed its client an identifier
// to resolve that had already been resolved one hop upstream.
//
// The id is kept alongside it: the shipped Android client reads
// `avatar_media_id`, so this is additive, never a swap.
func TestFeedCarriesTheAvatarURLProfileServiceSent(t *testing.T) {
	authorID := uuid.New()
	viewerID := uuid.New()
	avatarID := uuid.New()
	avatarURL := "/v1/media/" + avatarID.String() + "/serve/avatar"

	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			authorID.String(): map[string]any{
				"user_id": authorID, "display_name": "Author Name",
				"avatar_media_id": avatarID, "avatar_url": avatarURL,
			},
		})
	}))
	defer profileServer.Close()

	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer mediaServer.Close()

	svc := &Service{
		profileServiceURL: profileServer.URL,
		mediaServiceURL:   mediaServer.URL,
		profileClient:     profileServer.Client(),
		mediaClient:       mediaServer.Client(),
	}
	posts := []HydratedPost{{ID: uuid.New(), AuthorID: authorID}}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}

	if posts[0].Author.AvatarURL == nil {
		t.Fatalf("avatar_url was dropped decoding the profile; author=%+v", posts[0].Author)
	}
	if *posts[0].Author.AvatarURL != avatarURL {
		t.Errorf("avatar_url = %q, want %q", *posts[0].Author.AvatarURL, avatarURL)
	}
	// Additive: the id every shipped client already reads must survive.
	if posts[0].Author.AvatarMediaID == nil || *posts[0].Author.AvatarMediaID != avatarID {
		t.Errorf("avatar_media_id was lost: %v", posts[0].Author.AvatarMediaID)
	}

	raw, err := json.Marshal(posts[0].Author)
	if err != nil {
		t.Fatalf("marshal author: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal author: %v", err)
	}
	if out["avatar_url"] != avatarURL {
		t.Errorf("serialised avatar_url = %v", out["avatar_url"])
	}
	if out["avatar_media_id"] != avatarID.String() {
		t.Errorf("serialised avatar_media_id = %v", out["avatar_media_id"])
	}
}

// An author with no avatar — or one the profile photo gate redacted, which it
// does by clearing BOTH the id and the URL — must not widen the row with an
// empty key. Neither must the "Deleted account" placeholder, which is built
// from a zero Author and never touches the profile map.
func TestFeedOmitsAvatarURLWhenProfileSendsNone(t *testing.T) {
	authorID := uuid.New()
	missingID := uuid.New()
	viewerID := uuid.New()

	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			authorID.String(): map[string]any{
				"user_id": authorID, "display_name": "No Face",
			},
		})
	}))
	defer profileServer.Close()

	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer mediaServer.Close()

	svc := &Service{
		profileServiceURL: profileServer.URL,
		mediaServiceURL:   mediaServer.URL,
		profileClient:     profileServer.Client(),
		mediaClient:       mediaServer.Client(),
	}
	posts := []HydratedPost{
		{ID: uuid.New(), AuthorID: authorID},
		{ID: uuid.New(), AuthorID: missingID},
	}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}

	for i, want := range []string{"No Face", "Deleted account"} {
		if posts[i].Author.DisplayName != want {
			t.Fatalf("posts[%d] display_name = %q, want %q", i, posts[i].Author.DisplayName, want)
		}
		if posts[i].Author.AvatarURL != nil {
			t.Errorf("posts[%d] invented an avatar_url: %q", i, *posts[i].Author.AvatarURL)
		}
		raw, err := json.Marshal(posts[i].Author)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if v, ok := out["avatar_url"]; ok {
			t.Errorf("posts[%d] serialised an empty avatar_url: %v", i, v)
		}
	}
}
