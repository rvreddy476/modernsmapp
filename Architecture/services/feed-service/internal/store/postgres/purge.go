package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Account control: hide / unhide / purge ──────────────────────────────────
//
// See Architecture/shared/events/events.go ("Account control") and
// internal/purge. Hide is a row in hidden_authors (migrations/004) — a
// GLOBAL per-author suppression, not scoped to a viewer, consulted by every
// feed surface's block-filter tail (internal/service/feed.go,
// applyHiddenAuthorFilter) so a deactivated/deletion-scheduled account's
// posts stop reaching anyone, not just the requester. Purge is one
// transaction that removes every row keyed by the user and the
// hidden_authors row itself.

// SetUserHidden hides (true) or unhides (false) an author across every
// viewer's feed. Idempotent. Implements purge.Hider.
func (s *MetaStore) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	if hidden {
		_, err := s.db.Exec(ctx, `
			INSERT INTO hidden_authors (author_id, reason) VALUES ($1, $2)
			ON CONFLICT (author_id) DO UPDATE SET reason = EXCLUDED.reason, hidden_at = NOW()`,
			userID, reason)
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM hidden_authors WHERE author_id = $1`, userID)
	return err
}

// GetHiddenAuthorIDs returns the subset of authorIDs currently hidden.
// Empty input never touches the database. Used by the feed hide-gate
// (internal/service/feed.go, applyHiddenAuthorFilter) to exclude a
// deactivated/deletion-scheduled author's posts from every surface that
// already runs the block/mute filter.
func (s *MetaStore) GetHiddenAuthorIDs(ctx context.Context, authorIDs []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	out := make(map[uuid.UUID]struct{})
	if len(authorIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT author_id FROM hidden_authors WHERE author_id = ANY($1)`, authorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// PurgeUser erases every feed-service Postgres row keyed by the user in ONE
// transaction: their own signal/preference rows (user_id / viewer_id) and
// their celeb-authors / hidden-authors rows (author_id). Idempotent — a
// second call (auth re-emits user.purge_requested every 24h until acked)
// deletes nothing and still succeeds. Implements purge.PGStore.
//
// Deliberately does NOT touch:
//   - feed_distribution: keyed by post_id, not the author — feed-service
//     holds no author↔post index in Postgres outside Scylla, so there is no
//     way to find "this user's posts" here without a cross-service call.
//   - event_dedup: a delivery-dedup ledger for events already processed,
//     not user data.
func (s *MetaStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	stmts := []string{
		`DELETE FROM user_interactions WHERE viewer_id = $1 OR author_id = $1`,
		`DELETE FROM viewer_media_prefs WHERE user_id = $1`,
		`DELETE FROM post_impressions WHERE user_id = $1`,
		`DELETE FROM user_preferences WHERE user_id = $1`,
		`DELETE FROM feed_hides WHERE user_id = $1`,
		`DELETE FROM feed_mutes WHERE user_id = $1`,
		`DELETE FROM celeb_authors WHERE author_id = $1`,
		`DELETE FROM hidden_authors WHERE author_id = $1`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", stmt, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}
