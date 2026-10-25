package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// A Service built without a counter store (service.New(pg, nil, …), the
// integration rigs that run without Scylla) answers the direct read's two
// Scylla questions with "nothing stored" instead of a nil-pointer panic:
// TestTitleReviewServiceIT_HoldClearBlock (GetPost) and
// TestRestrictionIT_RoutesNeverClearAHold (the restore read-back) both
// crashed there.

func TestLikeCountsWithoutCounterStoreReadZero(t *testing.T) {
	s := &Service{}
	c, err := s.likeCountsForPost(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if c == nil || c.Likes != 0 || c.Comments != 0 || c.Shares != 0 || c.Saves != 0 {
		t.Fatalf("counts = %+v, want zeros", c)
	}
}

func TestViewerReactionWithoutCounterStoreIsNone(t *testing.T) {
	s := &Service{}
	r, err := s.viewerStoredReaction(context.Background(), uuid.New(), uuid.New())
	if err != nil || r != "" {
		t.Fatalf("reaction = %q, err = %v; want none", r, err)
	}
}
