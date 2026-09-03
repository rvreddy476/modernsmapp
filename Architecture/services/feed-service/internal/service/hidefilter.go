package service

import (
	"context"
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
// Unlike the block/mute lookup (a remote graph-service call that FAILS
// CLOSED — M2-P0-3), this is a local Postgres read against a store this
// service already owns. A lookup failure here logs and serves the feed
// filtered by block/mute alone rather than making the whole surface
// unavailable — consistent with how this service already treats its own
// Postgres reads (GetFeedMode, IsCeleb: default and continue on error).

// applyHiddenAuthorFilter drops every item authored by a currently-hidden
// account. Extracted so every applyBlockFilter call site can pair with it
// through one call, the same way the timeline path and the cold-start
// fallback are made to provably run the same block filter. The pure step
// (filterHiddenAuthors) is what the tests exercise directly; this wrapper
// just resolves the hidden set from Postgres first.
func (s *Service) applyHiddenAuthorFilter(ctx context.Context, items []FeedItem) []FeedItem {
	if len(items) == 0 || s.pgStore == nil {
		return items
	}
	authors := uniqueAuthorIDs(items)
	if len(authors) == 0 {
		return items
	}
	hidden, err := s.pgStore.GetHiddenAuthorIDs(ctx, authors)
	if err != nil {
		log.Printf("hidden-author lookup failed, feed served with block/mute filtering only: %v", err)
		return items
	}
	return filterHiddenAuthors(items, hidden)
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
