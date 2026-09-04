package service

import (
	"testing"

	"github.com/google/uuid"
)

// GET /v1/feed/reels?following_only=true — the reels "Following" tab keeps
// only reels by authors the viewer follows (the social follow graph, the
// same meaning as the home feed's following_only).

func TestReelsFollowingFilter_KeepsOnlyFollowedAuthors(t *testing.T) {
	followed := uuid.New()
	stranger := uuid.New()
	self := uuid.New()
	candidates := []FeedItem{
		feedItemBy(stranger),
		feedItemBy(followed),
		feedItemBy(self),
		feedItemBy(followed),
	}

	out := filterByAuthorSet(candidates, []uuid.UUID{followed})
	if len(out) != 2 {
		t.Fatalf("expected the 2 reels by the followed author, got %d", len(out))
	}
	for _, c := range out {
		if c.AuthorID != followed {
			t.Fatalf("a reel by %s survived a following-only filter", c.AuthorID)
		}
	}
}

func TestReelsFollowingFilter_NoFollowsIsAnEmptyTab(t *testing.T) {
	candidates := []FeedItem{feedItemBy(uuid.New()), feedItemBy(uuid.New())}
	if out := filterByAuthorSet(candidates, nil); len(out) != 0 {
		t.Fatalf("a viewer who follows nobody must get an empty Following tab, not %d reels", len(out))
	}
}
