package service

import (
	"testing"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

// Friends in common are fetched once per page, and only for candidates whose
// stored count says they have any (as the per-candidate lookup was skipped).
func TestWithMutualFriendsAsksOnlyForCandidatesWithAny(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	got := withMutualFriends([]store.SuggestionCandidate{
		{CandidateID: a, MutualFriendCount: 2},
		{CandidateID: b, MutualFriendCount: 0},
		{CandidateID: c, MutualFriendCount: 1},
	})
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Fatalf("withMutualFriends = %v, want [a c]", got)
	}
	if got := withMutualFriends(nil); len(got) != 0 {
		t.Fatalf("no candidates: %v", got)
	}
	if maxMutualFriendIDs != 3 {
		t.Fatalf("a suggestion names %d friends in common; the cards show 3", maxMutualFriendIDs)
	}
}
