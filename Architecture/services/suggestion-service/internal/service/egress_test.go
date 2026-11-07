package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

// SG-M2 / SG-L3: every surface returns through filterEgress, which drops
// hidden (deactivated / deletion-scheduled) accounts and the viewer's own
// cooldowns on top of blocks, failing closed.

type fakeEgress struct {
	hidden      map[uuid.UUID]struct{}
	cooldowns   map[uuid.UUID]store.CooldownEntry
	hiddenErr   error
	cooldownErr error
	hiddenAsked []uuid.UUID
}

func (f *fakeEgress) HiddenAmong(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	f.hiddenAsked = append(f.hiddenAsked, ids...)
	if f.hiddenErr != nil {
		return nil, f.hiddenErr
	}
	return f.hidden, nil
}

func (f *fakeEgress) GetActiveCooldowns(context.Context, uuid.UUID) (map[uuid.UUID]store.CooldownEntry, error) {
	if f.cooldownErr != nil {
		return nil, f.cooldownErr
	}
	return f.cooldowns, nil
}

func egressService(e egressReader, blocked ...uuid.UUID) *Service {
	set := map[uuid.UUID]struct{}{}
	for _, id := range blocked {
		set[id] = struct{}{}
	}
	svc := &Service{blocks: &fixedBlockLookup{blocked: set}}
	if e != nil {
		svc.egress = e
	}
	return svc
}

func TestEgressDropsHiddenCooldownBlockedAndSelf(t *testing.T) {
	viewer := uuid.New()
	hidden, cooled, blocked, safe := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	e := &fakeEgress{
		hidden:    map[uuid.UUID]struct{}{hidden: {}},
		cooldowns: map[uuid.UUID]store.CooldownEntry{cooled: {CandidateID: cooled, CooldownType: "hide"}},
	}
	resp := egressService(e, blocked).filterEgress(context.Background(), viewer, &SuggestionsResponse{
		Items: itemsFor(hidden, cooled, blocked, viewer, safe),
	})
	if containsCandidate(resp, hidden) {
		t.Fatal("a deactivated account was suggested")
	}
	if containsCandidate(resp, cooled) {
		t.Fatal("an account under the viewer's own hide cooldown was suggested")
	}
	if containsCandidate(resp, blocked) {
		t.Fatal("a blocked account was suggested")
	}
	if containsCandidate(resp, viewer) {
		t.Fatal("the viewer was suggested to themselves")
	}
	if !containsCandidate(resp, safe) || len(resp.Items) != 1 {
		t.Fatalf("got %d items, want only the safe candidate", len(resp.Items))
	}
}

func TestEgressFailsClosed(t *testing.T) {
	for name, e := range map[string]*fakeEgress{
		"hidden lookup fails":   {hiddenErr: errors.New("db down")},
		"cooldown lookup fails": {cooldownErr: errors.New("db down")},
		"not configured":        nil,
	} {
		t.Run(name, func(t *testing.T) {
			var reader egressReader
			if e != nil {
				reader = e
			}
			resp := egressService(reader).filterEgress(context.Background(), uuid.New(), &SuggestionsResponse{
				Items: itemsFor(uuid.New(), uuid.New()), NextCursor: "abc",
			})
			if len(resp.Items) != 0 || resp.NextCursor != "" {
				t.Fatalf("got %d items (cursor %q) while a safety check could not run", len(resp.Items), resp.NextCursor)
			}
		})
	}
}

// Blocks are still checked first, and an empty response asks nothing.
func TestEgressKeepsBlockSafetyAndSkipsEmpty(t *testing.T) {
	e := &fakeEgress{}
	svc := &Service{egress: e} // no block lookup: block safety is not running
	resp := svc.filterEgress(context.Background(), uuid.New(), &SuggestionsResponse{Items: itemsFor(uuid.New())})
	if len(resp.Items) != 0 {
		t.Fatal("egress returned items with no block lookup configured")
	}
	svc.filterEgress(context.Background(), uuid.New(), &SuggestionsResponse{})
	if len(e.hiddenAsked) != 0 {
		t.Fatal("the hidden lookup ran for an empty response")
	}
}
