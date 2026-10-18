package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// PATCH /v1/posts/:id (2026-10-18): link_url, is_branded, ai_generated and
// the friends audience, under the create rules.
func TestBuildPostEditPatch_DetailsScreenFields(t *testing.T) {
	s := &Service{}
	current := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Text: "x"}
	str := func(v string) *string { return &v }
	yes := true

	patch, err := s.buildPostEditPatch(context.Background(), current, PostEditInput{
		LinkURL: str(" https://example.com/a "), IsBranded: &yes, AIGenerated: &yes, Visibility: str("Friends"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if patch.LinkURL == nil || *patch.LinkURL != "https://example.com/a" || patch.ClearLinkURL {
		t.Fatalf("link = %v clear %v", patch.LinkURL, patch.ClearLinkURL)
	}
	if patch.IsBranded == nil || !*patch.IsBranded || patch.AIGenerated == nil || !*patch.AIGenerated {
		t.Fatalf("switches = %v %v", patch.IsBranded, patch.AIGenerated)
	}
	if patch.Visibility == nil || *patch.Visibility != "friends" {
		t.Fatalf("visibility = %v", patch.Visibility)
	}

	patch, err = s.buildPostEditPatch(context.Background(), current, PostEditInput{LinkURL: str("")})
	if err != nil || !patch.ClearLinkURL || patch.LinkURL != nil {
		t.Fatalf(`"" must clear the link: %+v %v`, patch, err)
	}
	if patch, _ := s.buildPostEditPatch(context.Background(), current, PostEditInput{}); patch.ClearLinkURL || patch.LinkURL != nil {
		t.Fatal("an absent link_url touched the column")
	}

	for _, bad := range []string{"javascript:alert(1)", "http://example.com", "data:text/html,x"} {
		_, err := s.buildPostEditPatch(context.Background(), current, PostEditInput{LinkURL: str(bad)})
		if !errors.Is(err, ErrInvalidLinkURL) {
			t.Fatalf("%q: %v", bad, err)
		}
		if status, code := PostEditErrorStatus(err); status != http.StatusUnprocessableEntity || code != "INVALID_LINK_URL" {
			t.Fatalf("%q maps to %d %s", bad, status, code)
		}
	}
}
