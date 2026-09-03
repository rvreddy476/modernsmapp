package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ── Account control: hide / unhide / purge ──────────────────────────────────
//
// See Architecture/shared/events/events.go ("Account control") and
// internal/purge. Hide is a row in post_hidden_authors; purge is one transaction
// that removes every post-service row keyed by the user and the
// post_hidden_authors row itself.
//
// post_hidden_authors is the account-level gate that internal/service/privacy_gate.go
// consults inside canViewPosts/graphCan, so a hidden author is denied on
// every read and write surface that already funnels through the gate — the
// same "one choke point" property that gate document describes for the
// graph-service privacy answer.

// SetUserHidden hides (true) or unhides (false) a user. Idempotent.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	if hidden {
		_, err := s.db.Exec(ctx, `
			INSERT INTO post_hidden_authors (user_id, reason) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason, hidden_at = NOW()`,
			userID, reason)
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM post_hidden_authors WHERE user_id = $1`, userID)
	return err
}

// IsHidden reports whether the given author is currently hidden (deactivated
// or in the 30-day deletion recovery window). Fails open to "not hidden" on
// a query error's zero value being returned along with the error — callers
// in the privacy gate already fail-closed via the graph answer, so a hidden
// lookup error should not additionally hide everyone.
func (s *Store) IsHidden(ctx context.Context, userID uuid.UUID) (bool, error) {
	var hidden bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM post_hidden_authors WHERE user_id = $1)`, userID,
	).Scan(&hidden)
	return hidden, err
}

// AnyHidden batch-checks a set of authors in one round trip. The result has
// an entry only for authors that ARE hidden; an absent id is not hidden.
func (s *Store) AnyHidden(ctx context.Context, authorIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(authorIDs))
	if len(authorIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM post_hidden_authors WHERE user_id = ANY($1)`, authorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// PurgeUser erases every post-service row keyed by the user in ONE
// transaction. Idempotent — a second call finds nothing to erase and
// succeeds.
//
// Ordering follows the actual FK shape (verified against database/setup.sql
// and database/migrations/*.sql, not assumed):
//
//   - polls, comments, post_engagement_counts and post_moderation_decisions
//     all carry a plain (no ON DELETE) or explicit ON DELETE RESTRICT FK to
//     posts(id), so they must be emptied before the posts row goes.
//   - video_metadata, crosspost_links, flick_series_items,
//     video_series_episodes, playlist_items, media_chapters, video_cards and
//     watch_progress(post_id) all carry ON DELETE CASCADE from posts(id) and
//     need no explicit statement here.
//   - video_series.trailer_post_id has neither CASCADE nor SET NULL, so it is
//     defensively nulled before the delete rather than left to fail the
//     transaction on an unrelated creator's row.
//   - post_media, reactions, saved_items, reel_hashtags, reel_crosspost,
//     slug_history, moderation_reviews, post_product_tags, post_reposts and
//     content_reports carry no FK to posts at all (plain UUID columns), so
//     they are cleaned up explicitly or they become orphaned rows.
//   - post_draft_media carries ON DELETE CASCADE from post_drafts(id).
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Materialize the affected id sets once so every later statement (and
	// the counter-decrement UPDATEs, which read AND write) sees the same
	// consistent snapshot within the transaction.
	setup := []string{
		`CREATE TEMP TABLE _purge_posts ON COMMIT DROP AS
			SELECT id FROM posts WHERE author_id = $1`,
		`CREATE TEMP TABLE _purge_comments ON COMMIT DROP AS
			SELECT id FROM comments
			WHERE author_id = $1 OR post_id IN (SELECT id FROM _purge_posts)`,
	}
	for _, stmt := range setup {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", firstWords(stmt), err)
		}
	}

	// Statements that take no parameters beyond userID via the temp tables
	// created above (userID is still bound as $1 for the ones that need it
	// directly).
	stmts := []string{
		// Orphan (don't cascade-fail) replies to comments about to be
		// deleted, whether or not the reply itself is also being deleted.
		`UPDATE comments SET parent_id = NULL
			WHERE parent_id IN (SELECT id FROM _purge_comments)
			  AND id NOT IN (SELECT id FROM _purge_comments)`,

		// Counters on surviving posts/comments shrink BEFORE the rows that
		// earned them go away — same rule graph-service's PurgeUser follows
		// for follower/following counts.
		`UPDATE post_engagement_counts pec
			SET comment_count = GREATEST(pec.comment_count - cnt.n, 0), updated_at = NOW()
			FROM (
				SELECT post_id, COUNT(*) AS n FROM comments
				WHERE id IN (SELECT id FROM _purge_comments)
				  AND is_deleted = FALSE
				  AND post_id NOT IN (SELECT id FROM _purge_posts)
				GROUP BY post_id
			) cnt
			WHERE pec.post_id = cnt.post_id`,
		`UPDATE post_engagement_counts pec
			SET like_count = GREATEST(pec.like_count - 1, 0), updated_at = NOW()
			FROM reactions r
			WHERE r.user_id = $1 AND r.target_type = 'post' AND r.target_id = pec.post_id
			  AND r.target_id NOT IN (SELECT id FROM _purge_posts)`,
		`UPDATE post_engagement_counts pec
			SET repost_count = GREATEST(pec.repost_count - 1, 0), updated_at = NOW()
			FROM post_reposts pr
			WHERE pr.user_id = $1 AND pr.status = 'active' AND pr.original_post_id = pec.post_id
			  AND pr.original_post_id NOT IN (SELECT id FROM _purge_posts)`,

		// Comment-scoped rows.
		`DELETE FROM comment_idempotency WHERE actor_id = $1 OR post_id IN (SELECT id FROM _purge_posts)`,
		`DELETE FROM comments WHERE id IN (SELECT id FROM _purge_comments)`,

		// Reactions: authored by the user anywhere, plus orphans left on
		// posts/comments that are being deleted regardless of who reacted.
		`DELETE FROM reactions
			WHERE user_id = $1
			   OR (target_type = 'post' AND target_id IN (SELECT id FROM _purge_posts))
			   OR (target_type = 'comment' AND target_id IN (SELECT id FROM _purge_comments))`,

		// Product tags the user created, plus tags on posts being deleted.
		`DELETE FROM post_product_tags WHERE creator_id = $1 OR post_id IN (SELECT id FROM _purge_posts)`,

		// Reel-scoped rows keyed by reel_id = post id (no FK on any of
		// these; see the doc comment above).
		`DELETE FROM reel_hashtags WHERE reel_id IN (SELECT id FROM _purge_posts)`,
		`DELETE FROM reel_crosspost WHERE source_reel_id IN (SELECT id FROM _purge_posts)`,
		`DELETE FROM slug_history WHERE reel_id IN (SELECT id FROM _purge_posts)`,
		`DELETE FROM moderation_reviews WHERE reel_id IN (SELECT id FROM _purge_posts)`,

		// Carousel/media references (no FK to posts).
		`DELETE FROM post_media WHERE post_id IN (SELECT id FROM _purge_posts)`,

		// Polls: poll_votes has no FK at all; poll_options FKs polls(post_id)
		// with no cascade, so it must go before polls.
		`DELETE FROM poll_votes WHERE user_id = $1 OR post_id IN (SELECT id FROM _purge_posts)`,
		`DELETE FROM poll_options WHERE post_id IN (SELECT id FROM _purge_posts)`,
		`DELETE FROM polls WHERE post_id IN (SELECT id FROM _purge_posts)`,

		// Watch history: post_id cascades from posts already; user_id is
		// explicit because the FK target (users(id) in the shared app DB)
		// belongs to a different service's purge, not this one.
		`DELETE FROM watch_progress WHERE user_id = $1 OR post_id IN (SELECT id FROM _purge_posts)`,

		// Saved items owned by the user, plus orphaned saves of posts being
		// deleted.
		`DELETE FROM saved_items
			WHERE user_id = $1
			   OR (target_type = 'post' AND target_id IN (SELECT id FROM _purge_posts))`,

		// Reports: filed by the user, plus reports targeting content being
		// deleted. sync_post_reports_count fires per row and is a no-op for
		// a post_engagement_counts row that is about to be (or already was)
		// removed.
		`DELETE FROM content_reports
			WHERE reporter_id = $1
			   OR (target_type = 'post' AND target_id IN (SELECT id FROM _purge_posts))
			   OR (target_type = 'comment' AND target_id IN (SELECT id FROM _purge_comments))`,

		// Dedup ledger entries about content being deleted.
		`DELETE FROM engagement_event_log
			WHERE target_id IN (SELECT id FROM _purge_posts)
			   OR target_id IN (SELECT id FROM _purge_comments)`,

		`DELETE FROM post_engagement_counts WHERE post_id IN (SELECT id FROM _purge_posts)`,

		// Reposts: by the user (any target), and of the user's own posts
		// (by anyone). The surviving-target decrement already ran above.
		`DELETE FROM post_reposts WHERE user_id = $1 OR original_post_id IN (SELECT id FROM _purge_posts)`,

		// Drafts.
		`DELETE FROM post_drafts WHERE author_id = $1`,
		`DELETE FROM reel_drafts WHERE author_id = $1`,

		// post_moderation_decisions is ON DELETE RESTRICT from posts(id) —
		// must be emptied before the posts row goes.
		`DELETE FROM post_moderation_decisions WHERE post_id IN (SELECT id FROM _purge_posts)`,

		// video_series.trailer_post_id has no ON DELETE action at all, so a
		// dangling reference to a post being deleted here (created by some
		// other author entirely) would otherwise fail the whole purge.
		`UPDATE video_series SET trailer_post_id = NULL
			WHERE trailer_post_id IN (SELECT id FROM _purge_posts)`,

		// The posts themselves. Everything above that referenced posts(id)
		// without CASCADE is now gone; everything with CASCADE (video
		// metadata, crosspost links, series episodes, playlist items,
		// chapters, cards, watch_progress by post_id) is removed by
		// PostgreSQL as part of this statement.
		`DELETE FROM posts WHERE author_id = $1`,

		// Purge implies hide is moot.
		`DELETE FROM post_hidden_authors WHERE user_id = $1`,
	}
	for _, stmt := range stmts {
		// Not every statement references the user directly (several act
		// purely through the _purge_posts/_purge_comments temp tables), and
		// pgx errors on a parameter that isn't referenced by the query, so
		// the argument is only passed when the statement actually uses $1.
		var execErr error
		if strings.Contains(stmt, "$1") {
			_, execErr = tx.Exec(ctx, stmt, userID)
		} else {
			_, execErr = tx.Exec(ctx, stmt)
		}
		if execErr != nil {
			return fmt.Errorf("purge: %s: %w", firstWords(stmt), execErr)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

// firstWords trims a multi-line SQL statement down to a short identifier for
// error messages, mirroring graph-service's internal/store/purge.go helper.
func firstWords(stmt string) string {
	var b []byte
	n := 0
	for i := 0; i < len(stmt); i++ {
		c := stmt[i]
		if c == ' ' || c == '\n' || c == '\t' {
			if len(b) > 0 && b[len(b)-1] != ' ' {
				b = append(b, ' ')
				n++
			}
			if n == 4 {
				break
			}
			continue
		}
		b = append(b, c)
	}
	return string(b)
}
