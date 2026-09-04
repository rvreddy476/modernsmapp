package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Continue watching (Tube "You" page, 2026-09-05): each progress row carries
// its post; a row whose post the batch read no longer returns is dropped,
// and the most-recent-first order of the rows is kept.
func TestAttachContinueWatchingPosts(t *testing.T) {
	a, gone, b := uuid.New(), uuid.New(), uuid.New()
	rows := []postgres.WatchProgress{{PostID: a, PositionMs: 10}, {PostID: gone, PositionMs: 20}, {PostID: b, PositionMs: 30}}
	posts := map[uuid.UUID]*PostDetail{
		a: {Post: &postgres.Post{ID: a, Title: "A"}},
		b: {Post: &postgres.Post{ID: b, Title: "B"}},
	}
	got := attachContinueWatchingPosts(rows, posts)
	if len(got) != 2 || got[0].PostID != a || got[1].PostID != b {
		t.Fatalf("got %+v want rows a,b in order", got)
	}
	if got[0].Post == nil || got[0].Post.Title != "A" || got[0].PositionMs != 10 {
		t.Fatalf("row a did not keep its post and progress: %+v", got[0])
	}
	if attachContinueWatchingPosts(nil, posts) == nil {
		t.Fatal("empty input must be an empty slice, not nil (JSON [] not null)")
	}
}
