package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 2 M2-P0-2 — the search-eligibility projection choke point.
//
// Codex acceptance: "Every post-service path that changes review/search
// eligibility publishes the transition transactionally with the canonical
// row change (outbox or an equivalent atomic mechanism). No direct
// status-changing path may bypass it."
//
// Rather than asking every call site to remember two operations, this
// file provides ONE primitive — BumpSearchRevAndEmitTx — that does the
// revision bump, reads back the canonical row, and writes the outbox
// event inside the caller's transaction. Status-changing statements are
// then rewritten to run through it.

// ErrPostRowMissing means the post disappeared mid-transition.
var ErrPostRowMissing = errors.New("post row not found")

// BumpSearchRevAndEmitTx increments posts.search_rev, reads the resulting
// canonical eligibility state, and enqueues PostSearchEligibilityChanged
// on the outbox — all inside tx, so the row change and the projection
// event commit together or not at all.
//
// It deliberately re-reads visibility/review_status/deleted_at from the
// row rather than trusting the caller's idea of the new state: the row is
// the canonical source, and a caller that computed the value separately
// could drift from what actually persisted.
func BumpSearchRevAndEmitTx(ctx context.Context, tx pgx.Tx, postID uuid.UUID) error {
	_, err := BumpSearchRevAndEmitTxRev(ctx, tx, postID)
	return err
}

// BumpSearchRevAndEmitTxRev is BumpSearchRevAndEmitTx that also returns the
// revision it stamped. The soft-delete / restore paths put that same
// revision on PostDeleted / PostRestored so a consumer that raises its own
// barrier (search) lands on the canonical number, not one above it.
func BumpSearchRevAndEmitTxRev(ctx context.Context, tx pgx.Tx, postID uuid.UUID) (int64, error) {
	var (
		authorID    uuid.UUID
		visibility  string
		review      string
		text        string
		contentType string
		createdAt   time.Time
		deletedAt   *time.Time
		publishAt   *time.Time
		rev         int64
	)
	err := tx.QueryRow(ctx, `
		UPDATE posts
		SET search_rev = search_rev + 1
		WHERE id = $1
		RETURNING author_id, visibility, review_status, text, content_type,
		          created_at, deleted_at, publish_at, search_rev`, postID).
		Scan(&authorID, &visibility, &review, &text, &contentType,
			&createdAt, &deletedAt, &publishAt, &rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrPostRowMissing
	}
	if err != nil {
		return 0, fmt.Errorf("bump search_rev: %w", err)
	}

	payload := events.PostSearchEligibilityChangedPayload{
		PostID:       postID.String(),
		AuthorID:     authorID.String(),
		Visibility:   visibility,
		ReviewStatus: review,
		Deleted:      deletedAt != nil,
		// A scheduled post (publish_at set, migration 042) is not public yet
		// whatever its visibility and review say; the consumer treats the
		// flag as ineligible. Its PostCreated arrives at publish time with a
		// higher revision.
		Scheduled:   publishAt != nil,
		SearchRev:   rev,
		ContentType: contentType,
		CreatedAt:   createdAt,
		ChangedAt:   time.Now().UTC(),
	}
	// Only carry the body when the post is actually eligible. There is no
	// reason to put non-public or unapproved text on the bus, and it keeps
	// removal events small.
	if publishAt == nil && events.SearchEligible(visibility, review, deletedAt != nil) {
		payload.Text = text
	}

	if err := InsertOutboxEventTx(ctx, tx, events.PostSearchEligibilityChanged, "post", postID, payload); err != nil {
		return 0, err
	}
	return rev, nil
}

// WithSearchEligibilityTx runs mutate inside a transaction and, if it
// reports that the row changed, bumps the revision and emits the
// transition atomically.
//
// mutate returns (changed, err): `false` means the guarded UPDATE matched
// nothing (e.g. a compare-and-set that lost), in which case no event is
// emitted — nothing changed, so the projection is already correct.
func (s *Store) WithSearchEligibilityTx(ctx context.Context, postID uuid.UUID,
	mutate func(ctx context.Context, tx pgx.Tx) (bool, error)) (bool, error) {

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	changed, err := mutate(ctx, tx)
	if err != nil {
		return false, err
	}
	if !changed {
		// Nothing moved; commit (mutate may have done read work) and skip
		// the event.
		return false, tx.Commit(ctx)
	}

	if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// EligibilityRow is the canonical projection state for one post, used by
// the reconciler and by the initial index build.
type EligibilityRow struct {
	PostID      uuid.UUID
	AuthorID    uuid.UUID
	Visibility  string
	Review      string
	Deleted     bool
	Scheduled   bool
	SearchRev   int64
	Text        string
	ContentType string
	CreatedAt   time.Time
}

// Eligible reports whether this row belongs in the public search index.
func (r EligibilityRow) Eligible() bool {
	return !r.Scheduled && events.SearchEligible(r.Visibility, r.Review, r.Deleted)
}

// ScanEligibility pages the canonical posts table for reconciliation
// (M2-P0-2 acceptance 6: "rollout rebuilds or reconciles the post and
// hashtag indices from canonical data"). Keyset paginated on id so a full
// sweep is resumable and does not hold a long transaction.
func (s *Store) ScanEligibility(ctx context.Context, afterID uuid.UUID, limit int) ([]EligibilityRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, visibility, review_status,
		       (deleted_at IS NOT NULL), (publish_at IS NOT NULL), search_rev, text, content_type, created_at
		FROM posts
		WHERE id > $1
		ORDER BY id ASC
		LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EligibilityRow
	for rows.Next() {
		var r EligibilityRow
		if err := rows.Scan(&r.PostID, &r.AuthorID, &r.Visibility, &r.Review,
			&r.Deleted, &r.Scheduled, &r.SearchRev, &r.Text, &r.ContentType, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
