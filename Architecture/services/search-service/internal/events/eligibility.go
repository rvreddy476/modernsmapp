package events

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
)

// Module 2 M2-P0-2 — applying a search-eligibility transition.
//
//	public + approved → idempotent upsert of the current document
//	anything else     → idempotent removal
//	deleted           → idempotent removal
//
// Ordering: Kafka orders only within a partition, and DLQ replay reorders
// by construction, so a late "approved" can arrive after a "rejected".
//
// The revision comparison used to happen HERE, between a GET and a write.
// That was a time-of-check/time-of-use race: two handlers could both read
// the old revision and the later write would win regardless of which
// revision it carried. The comparison now happens inside OpenSearch, in
// ApplyPostProjection, which holds the document lock while it decides.
// This function no longer reads a revision at all — it describes the
// intended state and lets the store decide whether it wins.

// applySearchEligibility projects one transition onto the public index.
func (c *Consumer) applySearchEligibility(ctx context.Context, p events.PostSearchEligibilityChangedPayload) error {
	if p.PostID == "" {
		return nil // malformed; nothing addressable
	}

	eligible := events.SearchEligible(p.Visibility, p.ReviewStatus, p.Deleted)

	// A zero/absent revision cannot be ordered. Fail closed: remove, and
	// let OpenSearch stamp storedRev+1 so the removal raises the barrier
	// instead of being replayable.
	if p.SearchRev <= 0 {
		slog.Warn("search: eligibility event without a usable revision; removing",
			"post_id", p.PostID)
		if err := c.store.ApplyPostProjection(ctx, search.PostProjection{
			PostID:   p.PostID,
			AutoRev:  true,
			Removed:  true,
			AuthorID: p.AuthorID,
		}); err != nil {
			return fmt.Errorf("eligibility(no-rev): remove %s: %w", p.PostID, err)
		}
		return nil
	}

	if !eligible {
		if err := c.store.ApplyPostProjection(ctx, search.PostProjection{
			PostID:   p.PostID,
			Rev:      p.SearchRev,
			Removed:  true,
			AuthorID: p.AuthorID,
		}); err != nil {
			return fmt.Errorf("eligibility: remove %s: %w", p.PostID, err)
		}
		// Removal latency is the safety-critical half of the SLO: it is
		// how long unsafe or newly-private content stayed searchable.
		observeEligibilityApply(p.ChangedAt, "remove")
		slog.Info("search: post removed from public index",
			"post_id", p.PostID, "visibility", p.Visibility,
			"review_status", p.ReviewStatus, "deleted", p.Deleted, "rev", p.SearchRev)
		return nil
	}

	// M2-P0-7 / re-review P0-2: never re-index content for an erased
	// account. The per-post fence tombstone already outranks this event
	// for posts that were indexed at erasure time; the fence check covers
	// posts that were not; and the recheck inside this helper covers an
	// erasure that lands while this very write is in flight.
	// The projection script replaces the whole _source, so the author's
	// current account_visibility must ride along or a re-approval would
	// reset a private author's post to public.
	authorPrivate, err := c.authorIsPrivate(ctx, p.AuthorID)
	if err != nil {
		return err
	}
	if err := c.store.IndexPostUnlessAuthorErased(ctx, search.PostProjection{
		PostID: p.PostID,
		Rev:    p.SearchRev,
		Doc: search.PostDoc{
			PostID:          p.PostID,
			AuthorID:        p.AuthorID,
			AuthorIsPrivate: authorPrivate,
			Text:            p.Text,
			Hashtags:        extractHashtags(p.Text),
			Visibility:      p.Visibility,
			ReviewStatus:    p.ReviewStatus,
			SearchRev:       p.SearchRev,
			PostType:        p.ContentType,
			CreatedAt:       p.CreatedAt,
		},
	}); err != nil {
		return fmt.Errorf("eligibility: index %s: %w", p.PostID, err)
	}

	// NOTE ON HASHTAGS (M2-P0-4 in the review): this deliberately does NOT
	// bump a hashtag counter. The hashtags_v1 counters were increment-only
	// — nothing decremented them on rejection, takedown, visibility
	// downgrade, edit, or deletion — so a tag from a once-approved post
	// stayed in autocomplete forever after the post was removed. Every
	// viewer-facing hashtag surface now derives from a live aggregation
	// over posts_v1 instead, which is reversible by construction: the
	// contribution disappears the moment the post document does.

	observeEligibilityApply(p.ChangedAt, "index")
	slog.Info("search: post indexed by eligibility transition",
		"post_id", p.PostID, "rev", p.SearchRev)
	return nil
}
