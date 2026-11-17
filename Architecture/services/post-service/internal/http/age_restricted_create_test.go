package http

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// lv-9 (long-video readiness audit): POST /v1/posts takes age_restricted, so
// the 18+ gate is stored with the post instead of by a follow-up PATCH.

// The route forwards the flag to the service: on a kind that is not a video
// it is 400 AGE_RESTRICTION_NOT_ALLOWED before anything is written (the rig
// has no database, so a request that got further would fail loudly). Were
// the field dropped by the handler, this would not be refused.
func TestCreatePostRouteForwardsAgeRestricted(t *testing.T) {
	r := newGateRouteRig(t)
	for _, kind := range []string{"post", "poll"} {
		poll := ""
		if kind == "poll" {
			poll = `"poll":{"question":"Next?","options":["a","b"]},`
		}
		body := `{"text":"x","visibility":"public","content_type":"` + kind + `",` + poll + `"age_restricted":true}`
		w := r.do(http.MethodPost, "/v1/posts", body)
		if w.Code != http.StatusBadRequest || codeOf(w) != "AGE_RESTRICTION_NOT_ALLOWED" {
			t.Fatalf("%s with age_restricted: %d %s; want 400 AGE_RESTRICTION_NOT_ALLOWED", kind, w.Code, w.Body.String())
		}
	}
}

// The web studio skips its fallback PATCH only when the create response
// echoes age_restricted: true, so both create body shapes (the plain post,
// and a long video's post + credits) must carry the field, never omitted.
func TestCreatePostResponseEchoesAgeRestricted(t *testing.T) {
	p := &postgres.Post{ID: uuid.New(), ContentType: "long_video", AgeRestricted: true}
	for name, body := range map[string]any{
		"post":              p,
		"post with credits": createPostResponse{Post: p, Credits: &[]service.CreditView{}},
	} {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"age_restricted":true`) {
			t.Fatalf("%s: create response lacks age_restricted:true: %s", name, raw)
		}
	}
	p.AgeRestricted = false
	raw, _ := json.Marshal(p)
	if !strings.Contains(string(raw), `"age_restricted":false`) {
		t.Fatalf("an open post must say age_restricted:false, not omit it: %s", raw)
	}
}

// Mass assignment stays impossible: the create body is a whitelist, and none
// of the server-owned or Creator-Hub-only columns can be named in it. Adding
// age_restricted must not have opened the door to its neighbours.
func TestCreatePostRequestNamesNoServerOwnedField(t *testing.T) {
	forbidden := map[string]bool{
		"id": true, "author_id": true, "review_status": true, "deleted_at": true,
		"active_restriction_count": true, "content_type_explicit": true,
		"distribution_rev": true, "hide_like_count": true, "default_comment_sort": true,
		"related_post_id": true, "notify_subscribers": true, "like_count": true,
		"view_count": true, "comment_count": true, "share_count": true,
		"created_at": true, "updated_at": true, "is_pinned": true,
	}
	typ := reflect.TypeOf(CreatePostRequest{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		seen[name] = true
		if forbidden[name] {
			t.Errorf("CreatePostRequest binds %q: a client could set a field only the server or the edit route may set", name)
		}
	}
	if !seen["age_restricted"] {
		t.Fatal("CreatePostRequest does not bind age_restricted (lv-9)")
	}
}
