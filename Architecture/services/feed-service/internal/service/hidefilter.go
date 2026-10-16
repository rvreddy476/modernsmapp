package service

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
)

// Account-control hide gate (auth-service's 30-day deletion flow).
//
// See Architecture/shared/events/events.go ("Account control") and
// internal/purge. internal/store/postgres/purge.go's hidden_authors table is
// a GLOBAL per-author suppression set: an author lands in it on
// user.deactivated / user.deletion_scheduled and comes back out on
// user.reactivated / user.deletion_cancelled (or is erased outright by
// user.purge_requested). It is deliberately wired into the SAME call sites
// as applyBlockFilter (feed.go) — every surface that already resolves
// block/mute state gets hidden-author exclusion applied right after it, so
// there is nowhere a hidden author's posts can leak back in that a blocked
// author could not.
//
// Like the block/mute lookup (M2-P0-3), this FAILS CLOSED: a lookup error is
// returned and the surface answers with an error, never a page that may
// carry a deactivated or deletion-scheduled account's posts. It used to log
// and serve the page filtered by block/mute alone, so a Postgres blip put a
// person who had just deactivated their account back in everyone's feed —
// the one outcome the account-control flow exists to prevent.

// applyHiddenAuthorFilter drops every item authored by a currently-hidden
// account. Extracted so every applyBlockFilter call site can pair with it
// through one call, the same way the timeline path and the cold-start
// fallback are made to provably run the same block filter. The pure step
// (filterHiddenAuthors) is what the tests exercise directly; this wrapper
// just resolves the hidden set from Postgres first.
func (s *Service) applyHiddenAuthorFilter(ctx context.Context, items []FeedItem) ([]FeedItem, error) {
	if len(items) == 0 || s.pgStore == nil {
		return items, nil
	}
	authors := uniqueAuthorIDs(items)
	if len(authors) == 0 {
		return items, nil
	}
	hidden, err := s.pgStore.GetHiddenAuthorIDs(ctx, authors)
	if err != nil {
		log.Printf("hidden-author lookup failed; refusing the page rather than serving it unfiltered: %v", err)
		return nil, fmt.Errorf("feed unavailable: hidden-author state could not be resolved: %w", err)
	}
	return filterHiddenAuthors(items, hidden), nil
}

func uniqueAuthorIDs(items []FeedItem) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(items))
	out := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		if _, ok := seen[it.AuthorID]; ok {
			continue
		}
		seen[it.AuthorID] = struct{}{}
		out = append(out, it.AuthorID)
	}
	return out
}

// filterHiddenAuthors is the pure step, mirroring applyBlockFilter.
func filterHiddenAuthors(items []FeedItem, hidden map[uuid.UUID]struct{}) []FeedItem {
	if len(hidden) == 0 || len(items) == 0 {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if _, isHidden := hidden[it.AuthorID]; isHidden {
			continue
		}
		out = append(out, it)
	}
	return out
}
