package purge

import (
	"context"

	"github.com/google/uuid"
)

// TimelineStore is the Scylla slice: the author's own author_timeline_by_author
// rows (scylla.TimelineStore.DeleteTimelineEntriesByAuthor).
//
// Limitation (documented, not fixed here): this deletes ONLY the canonical
// author-timeline copy. It does NOT remove the fanned-out copies of the
// user's posts sitting in every follower's home_timeline_by_user partition
// — Scylla's fan-out-on-write model writes one row per follower at post
// time, and there is no reverse index of "which followers hold this
// author's posts" to drive a full scrub. Those stale rows are relied on to
// age out of relevance via the hydration tail (post-service's tombstone /
// 404 on the purged author's posts), not erased. See feed-service's PR
// report for the full writeup.
type TimelineStore interface {
	DeleteTimelineEntriesByAuthor(ctx context.Context, authorID uuid.UUID) error
}

// PGStore is the Postgres slice.
type PGStore interface {
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// StoreEraser runs the idempotent Scylla delete first, then the Postgres
// transaction. Satisfies the Eraser interface.
type StoreEraser struct {
	timeline TimelineStore
	pg       PGStore
}

// NewEraser builds the composite eraser; either store may be nil.
func NewEraser(timeline TimelineStore, pg PGStore) *StoreEraser {
	return &StoreEraser{timeline: timeline, pg: pg}
}

// PurgeUser implements the feed-service erase.
func (e *StoreEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if e.timeline != nil {
		if err := e.timeline.DeleteTimelineEntriesByAuthor(ctx, userID); err != nil {
			return err
		}
	}
	if e.pg != nil {
		return e.pg.PurgeUser(ctx, userID)
	}
	return nil
}
