package service

import (
	"testing"

	"github.com/google/uuid"
)

// Soft delete hides a post from feed hydration (2026-09-04).
//
// Two gates, both pinned here without Redis or post-service:
//
//   - the cached path: a per-viewer cache row for a tombstoned post is
//     discarded (dropTombstoned);
//   - the fresh path: a timeline item whose post is absent from
//     post-service's batch answer (the batch filters deleted_at IS NULL)
//     is dropped by mergeHydratedItems — fail closed, never a placeholder.

func TestDropTombstoned_RemovesDeletedPostsFromCachedPage(t *testing.T) {
	live, deleted, viewer := uuid.New(), uuid.New(), uuid.New()
	cached := map[uuid.UUID]HydratedPost{
		live:    {ID: live, AuthorID: viewer, Text: "still here"},
		deleted: {ID: deleted, AuthorID: viewer, Text: "gone"},
	}
	got := dropTombstoned(cached, map[uuid.UUID]bool{deleted: true})
	if _, ok := got[deleted]; ok {
		t.Fatal("a tombstoned post survived the cached path")
	}
	if _, ok := got[live]; !ok {
		t.Fatal("a live post was dropped alongside the deleted one")
	}
	// No tombstones: the map is returned untouched.
	if same := dropTombstoned(cached, nil); len(same) != 2 {
		t.Fatalf("no tombstones should keep every row, got %d", len(same))
	}
}

func TestMergeHydratedItems_DropsPostsMissingFromBatch(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	live, deleted := uuid.New(), uuid.New()
	items := []FeedItem{
		{PostID: deleted, AuthorID: author, ContentType: "flick", Source: "following"},
		{PostID: live, AuthorID: author, ContentType: "flick", Source: "following"},
	}
	// post-service answered for the live post only: the deleted one has
	// deleted_at set and the batch endpoint filters it out.
	batch := map[string]HydratedPost{
		live.String(): {ID: live, AuthorID: author, ContentType: "flick"},
	}
	out := (&Service{}).mergeHydratedItems(items, batch, nil, viewer)
	if len(out) != 1 || out[0].ID != live {
		t.Fatalf("merged = %+v, want only the live post: a deleted post must never be rendered from its timeline row", out)
	}
}
