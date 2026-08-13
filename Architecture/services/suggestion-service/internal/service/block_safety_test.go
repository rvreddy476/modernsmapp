package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Module 3 M3-P0-8 / SR-7 — suggestions must never surface a blocked account.
//
// suggestion-service filtered NOTHING for blocks. The comment claimed
// otherwise ("Filter out existing friends, blocked users, and self") but the
// code built its exclusion set from friends and the viewer's own id only.
// There was no block lookup anywhere in the service.
//
// These tests exercise filterBlocked directly. It is the single egress point
// every return path now runs through, so testing it covers the cache hit, the
// candidate path, the popular-users fallback and the interstitial surface at
// once — and a new return path that forgets to call it is a code-level
// omission a reader can see, not a silent behavioural gap.

type fixedBlockLookup struct {
	blocked map[uuid.UUID]struct{}
	err     error
	calls   int
}

func (f *fixedBlockLookup) BlockedSet(context.Context, uuid.UUID) (map[uuid.UUID]struct{}, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.blocked, nil
}

func itemsFor(ids ...uuid.UUID) []SuggestionItem {
	out := make([]SuggestionItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, SuggestionItem{
			CandidateUserID: id.String(),
			DisplayName:     "Candidate",
		})
	}
	return out
}

func containsCandidate(resp *SuggestionsResponse, id uuid.UUID) bool {
	for _, item := range resp.Items {
		if item.CandidateUserID == id.String() {
			return true
		}
	}
	return false
}

func TestBlockedCandidatesAreRemovedAndOthersKept(t *testing.T) {
	viewer := uuid.New()
	blockedUser := uuid.New()
	safeUser := uuid.New()

	svc := &Service{blocks: &fixedBlockLookup{
		blocked: map[uuid.UUID]struct{}{blockedUser: {}},
	}}

	resp := svc.filterBlocked(context.Background(), viewer, &SuggestionsResponse{
		Items: itemsFor(blockedUser, safeUser),
	})

	if containsCandidate(resp, blockedUser) {
		t.Fatal("a blocked account was suggested to the viewer who blocked them")
	}
	if !containsCandidate(resp, safeUser) {
		t.Fatal("an unblocked candidate was dropped; the filter is over-broad")
	}
	if len(resp.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(resp.Items))
	}
}

// FAIL CLOSED. A lookup failure must not fall back to the unfiltered list —
// that would recommend blocked accounts for the length of the incident, with
// every response still 200.
func TestBlockLookupFailureReturnsEmptyNotUnfiltered(t *testing.T) {
	viewer := uuid.New()
	blockedUser := uuid.New()

	svc := &Service{blocks: &fixedBlockLookup{err: errors.New("graph-service unreachable")}}
	resp := svc.filterBlocked(context.Background(), viewer, &SuggestionsResponse{
		Items:      itemsFor(blockedUser, uuid.New(), uuid.New()),
		NextCursor: "abc",
	})

	if len(resp.Items) != 0 {
		t.Fatalf("got %d items while the block lookup was FAILING. An unfiltered "+
			"suggestion list recommends accounts the viewer blocked.", len(resp.Items))
	}
	if resp.NextCursor != "" {
		t.Error("a next cursor was returned for an empty page; paging would resume " +
			"into unfiltered results")
	}
}

// An unconfigured lookup is the same hazard: block safety simply is not running.
func TestUnconfiguredBlockLookupReturnsEmpty(t *testing.T) {
	svc := &Service{} // blocks is nil
	resp := svc.filterBlocked(context.Background(), uuid.New(), &SuggestionsResponse{
		Items: itemsFor(uuid.New(), uuid.New()),
	})
	if len(resp.Items) != 0 {
		t.Fatalf("got %d items with NO block lookup configured", len(resp.Items))
	}
}

// A candidate id that cannot be parsed cannot be checked against the block
// set. Showing it would mean showing an unverified account.
func TestUnparseableCandidateIDIsDropped(t *testing.T) {
	viewer := uuid.New()
	svc := &Service{blocks: &fixedBlockLookup{
		blocked: map[uuid.UUID]struct{}{uuid.New(): {}},
	}}

	resp := svc.filterBlocked(context.Background(), viewer, &SuggestionsResponse{
		Items: []SuggestionItem{
			{CandidateUserID: "not-a-uuid", DisplayName: "Malformed"},
			{CandidateUserID: uuid.New().String(), DisplayName: "Fine"},
		},
	})
	for _, item := range resp.Items {
		if item.CandidateUserID == "not-a-uuid" {
			t.Fatal("a candidate whose id could not be checked was shown anyway")
		}
	}
	if len(resp.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(resp.Items))
	}
}

// An empty response short-circuits: no reason to pay for a graph call.
func TestEmptyResponseSkipsTheLookup(t *testing.T) {
	lookup := &fixedBlockLookup{}
	svc := &Service{blocks: lookup}
	svc.filterBlocked(context.Background(), uuid.New(), &SuggestionsResponse{Items: nil})
	if lookup.calls != 0 {
		t.Errorf("the block lookup was called %d times for an empty response", lookup.calls)
	}
}

// Blocks are SYMMETRIC: graph-service's blocked-and-muted set contains both
// who the viewer blocked and who blocked the viewer (Module 2 M2-P0-3). Both
// directions must be removed — a viewer must not be recommended someone who
// blocked them either.
func TestBothBlockDirectionsAreFiltered(t *testing.T) {
	viewer := uuid.New()
	iBlockedThem := uuid.New()
	theyBlockedMe := uuid.New()

	svc := &Service{blocks: &fixedBlockLookup{blocked: map[uuid.UUID]struct{}{
		iBlockedThem:  {},
		theyBlockedMe: {},
	}}}

	resp := svc.filterBlocked(context.Background(), viewer, &SuggestionsResponse{
		Items: itemsFor(iBlockedThem, theyBlockedMe),
	})
	if len(resp.Items) != 0 {
		t.Fatalf("got %d items, want 0: both directions of a block must be "+
			"removed from suggestions", len(resp.Items))
	}
}
