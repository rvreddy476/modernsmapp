package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// An unlisted or private post is the author's alone in every listing: fan-out
// writes only the author's own rows, and the hydration tail drops any row a
// non-author still holds (a pre-existing row, the follow backfill, a cached
// row that predates an edit).

func TestFanoutPost_UnlistedAndPrivateReachOnlyTheAuthor(t *testing.T) {
	for _, vis := range []string{"unlisted", "private", "staged", "PRIVATE"} {
		t.Run(vis, func(t *testing.T) {
			author, follower, connection, subscriber, channel := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			s, tl := fanoutStubs(t, []uuid.UUID{follower}, []uuid.UUID{connection}, []uuid.UUID{subscriber}, false)

			if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "long_video", vis, channel); err != nil {
				t.Fatal(err)
			}
			if got := tl.rows(author); got != 1 {
				t.Fatalf("author got %d own home rows, want 1", got)
			}
			for who, id := range map[string]uuid.UUID{"follower": follower, "connection": connection, "subscriber": subscriber} {
				if got := tl.rows(id); got != 0 {
					t.Errorf("%s got %d rows of a %s post, want 0", who, got, vis)
				}
			}
		})
	}
}

func TestFanoutPost_PublicAndFollowersStillReachFollowers(t *testing.T) {
	for _, vis := range []string{"public", "followers", ""} {
		t.Run(vis, func(t *testing.T) {
			author, follower := uuid.New(), uuid.New()
			s, tl := fanoutStubs(t, []uuid.UUID{follower}, nil, nil, false)

			if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "post", vis, uuid.Nil); err != nil {
				t.Fatal(err)
			}
			if got := tl.rows(follower); got != 1 {
				t.Fatalf("follower got %d rows of a %q post, want 1", got, vis)
			}
		})
	}
}

func TestListingVisibilityFilter_OnlyTheAuthorSeesUnlistedOrPrivate(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	page := func() []HydratedPost {
		return []HydratedPost{
			{ID: uuid.New(), AuthorID: author, Visibility: "public"},
			{ID: uuid.New(), AuthorID: author, Visibility: "followers"},
			{ID: uuid.New(), AuthorID: author, Visibility: ""},
			{ID: uuid.New(), AuthorID: author, Visibility: "unlisted"},
			{ID: uuid.New(), AuthorID: author, Visibility: "private"},
			{ID: uuid.New(), AuthorID: author, Visibility: "circle"},
			{ID: uuid.New(), AuthorID: author, Visibility: "something-new"},
		}
	}

	got := applyListingVisibilityFilter(viewer, page())
	if len(got) != 3 {
		t.Fatalf("non-author kept %d posts, want 3 (public, followers, legacy empty)", len(got))
	}
	for _, p := range got {
		if !listableVisibility(p.Visibility) {
			t.Errorf("non-author was served a %q post", p.Visibility)
		}
	}

	if got := applyListingVisibilityFilter(author, page()); len(got) != 7 {
		t.Fatalf("author kept %d of their own posts, want all 7", len(got))
	}
}
