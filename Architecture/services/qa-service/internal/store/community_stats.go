package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ── Community Q&A counters, recounted off the write path (audit M7) ─────────
//
// Every answer used to recount its whole community (COUNT(*) of answers, a
// UNION over every question and answer author) and update the community row
// inside the answer's transaction, so answers in one community ran one at a
// time and slowed down as it grew. Now a write only stamps
// qa_communities.qa_stats_stale_at (migration 003), and only when it is not
// already stamped, so a busy community's answers take no community row lock
// at all; RunCommunityStatsRefresher recounts stamped communities every
// interval. The counts (qa_question_count, qa_answer_count,
// qa_contributor_count and the community_qa_settings totals) lag by at most
// one interval and are exact after it, including after deletes and hides
// that never decremented them before.

// markCommunityStatsStaleTx stamps communityID for the background recount.
func markCommunityStatsStaleTx(ctx context.Context, exec dbtx, communityID uuid.UUID) error {
	_, err := exec.Exec(ctx, `
		UPDATE qa_communities SET qa_stats_stale_at = now()
		WHERE id = $1 AND qa_stats_stale_at IS NULL`, communityID)
	return err
}

// CommunityStatsInterval is how often RunCommunityStatsRefresher recounts.
const CommunityStatsInterval = 30 * time.Second

// communityStatsBatch bounds the communities recounted per pass.
const communityStatsBatch = 100

// RunCommunityStatsRefresher recounts stamped communities every interval
// until ctx ends. Safe on every replica: a community is claimed by clearing
// its stamp, which only one replica can do.
func (s *Store) RunCommunityStatsRefresher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = CommunityStatsInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.RefreshStaleCommunityStats(ctx, communityStatsBatch); err != nil {
				slog.Warn("qa community stats refresh failed", "error", err, "refreshed", n)
			}
		}
	}
}

// RefreshStaleCommunityStats recounts up to limit stamped communities and
// reports how many it recounted.
func (s *Store) RefreshStaleCommunityStats(ctx context.Context, limit int) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, qa_stats_stale_at FROM qa_communities
		WHERE qa_stats_stale_at IS NOT NULL
		ORDER BY qa_stats_stale_at
		LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type stale struct {
		id uuid.UUID
		at time.Time
	}
	var todo []stale
	for rows.Next() {
		var st stale
		if err := rows.Scan(&st.id, &st.at); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	done := 0
	for _, st := range todo {
		ok, err := s.refreshCommunityStats(ctx, st.id, st.at)
		if err != nil {
			return done, err
		}
		if ok {
			done++
		}
	}
	return done, nil
}

// refreshCommunityStats recounts one community. It first clears the stamp
// it read (claiming the recount; false when another replica already did),
// then counts: a write that lands after the clear stamps the community
// again, so nothing it changes is missed. A failed recount re-stamps.
func (s *Store) refreshCommunityStats(ctx context.Context, communityID uuid.UUID, staleAt time.Time) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE qa_communities
		SET qa_stats_stale_at = NULL,
		    last_qa_activity_at = GREATEST(COALESCE(last_qa_activity_at, $2), $2)
		WHERE id = $1 AND qa_stats_stale_at = $2`, communityID, staleAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := s.recountCommunityStats(ctx, communityID); err != nil {
		if _, restampErr := s.db.Exec(ctx, `
			UPDATE qa_communities SET qa_stats_stale_at = COALESCE(qa_stats_stale_at, now())
			WHERE id = $1`, communityID); restampErr != nil {
			slog.Warn("qa community stats: re-stamp after a failed recount failed", "community_id", communityID, "error", restampErr)
		}
		return false, fmt.Errorf("recount community %s: %w", communityID, err)
	}
	return true, nil
}

// recountCommunityStats counts outside any transaction (no row is locked
// while it scans) and then writes the totals in one short transaction.
func (s *Store) recountCommunityStats(ctx context.Context, communityID uuid.UUID) error {
	var questions, answers, contributors int
	if err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM questions q
			 WHERE q.community_id = $1 AND q.deleted_at IS NULL AND q.status <> 'deleted'),
			(SELECT COUNT(*) FROM answers a JOIN questions q ON q.id = a.question_id
			 WHERE q.community_id = $1 AND q.deleted_at IS NULL AND a.deleted_at IS NULL),
			(SELECT COUNT(*) FROM (
				SELECT q.author_id FROM questions q
				WHERE q.community_id = $1 AND q.deleted_at IS NULL AND q.status <> 'deleted'
				UNION
				SELECT a.author_id FROM answers a JOIN questions q ON q.id = a.question_id
				WHERE q.community_id = $1 AND q.deleted_at IS NULL AND a.deleted_at IS NULL
			) c)`, communityID,
	).Scan(&questions, &answers, &contributors); err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE qa_communities
		SET qa_question_count = $2, qa_answer_count = $3, qa_contributor_count = $4, updated_at = now()
		WHERE id = $1`, communityID, questions, answers, contributors); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO community_qa_settings (community_id, total_questions_count, total_answers_count, unique_contributors_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (community_id) DO UPDATE SET
			total_questions_count = EXCLUDED.total_questions_count,
			total_answers_count = EXCLUDED.total_answers_count,
			unique_contributors_count = EXCLUDED.unique_contributors_count,
			updated_at = now()`, communityID, questions, answers, contributors); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// questionTopicIDsTx reads a question's topic ids and closes the rows before
// returning, so the caller may write on the same transaction (a pgx
// connection cannot run a statement while a result set is still open: "conn
// busy"; it broke every answer and every view in a community).
func questionTopicIDsTx(ctx context.Context, exec dbtx, questionID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := exec.Query(ctx, `SELECT topic_id FROM question_topics WHERE question_id = $1`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
