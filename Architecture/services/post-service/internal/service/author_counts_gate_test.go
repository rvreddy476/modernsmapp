package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Security sweep #24: GET /v1/posts/by-author/:authorId/counts had no viewer
// gate while the listing beside it hides a private account's grid from
// strangers and a blocked viewer. The counts now take the same decision.
//
// The store is nil here: a refused viewer must be answered before the
// database is touched, so reaching it would panic.
func TestAuthorCountsAreGatedLikeTheGrid(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	var calls int32
	// The graph denies view_posts for this author (private, or a block).
	srv := fakeGraphCan(t, map[string]bool{author.String(): false}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)

	counts, err := s.GetAuthorCountsForViewer(context.Background(), author, &viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("a refused viewer read counts: %v", counts)
	}
	if calls != 1 {
		t.Fatalf("graph asked %d times, want once", calls)
	}

	// The anonymous stranger is refused the same way.
	counts, err = s.GetAuthorCountsForViewer(context.Background(), author, nil)
	if err != nil || len(counts) != 0 {
		t.Fatalf("signed-out viewer: counts=%v err=%v", counts, err)
	}
}

// An unanswered graph is a denial here too (fail closed), exactly as for
// the listing.
func TestAuthorCountsFailClosedOnGraphOutage(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	s := newGatedService("http://127.0.0.1:1")
	counts, err := s.GetAuthorCountsForViewer(context.Background(), author, &viewer)
	if err != nil || len(counts) != 0 {
		t.Fatalf("graph down: counts=%v err=%v", counts, err)
	}
}
