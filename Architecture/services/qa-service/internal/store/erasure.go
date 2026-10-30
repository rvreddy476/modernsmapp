package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Erased is what one AnonymizeUser removed: the questions and answers it
// deleted, whose media claims the caller releases afterwards.
type Erased struct {
	QuestionIDs []uuid.UUID
	AnswerIDs   []uuid.UUID
}

// ErasedQuestionTitle replaces the title of an erased user's question.
const ErasedQuestionTitle = "[deleted]"

// AnonymizeUser erases a user's Q&A data for account deletion (audit M8).
//
// It runs as ONE transaction and returns its error, so the consumer commits
// the deletion event only after the erasure is durable and retries it
// otherwise (it used to ignore every error and always succeed). It is
// idempotent: a second run finds nothing left to change and succeeds.
//
// What it does, following the platform's erasure policy (post-service
// PurgeUser: the user's words are gone, counters on surviving content shrink
// before the rows that earned them go):
//   - the user's questions, answers and comments are tombstoned: soft
//     deleted (if not already) and their title, body, body_html and slug
//     blanked, including content the user had deleted earlier. Rows stay so
//     other users' answers, votes and moderation records keep their keys;
//     their tags, translations, local scopes and edit history are deleted;
//   - answer_count / comment_count on other users' questions and answers
//     drop by the live answers and comments erased, and a question whose
//     accepted answer is erased is no longer marked answered;
//   - the user's votes are removed after vote_score / upvote_count /
//     downvote_count on their targets are reversed (they used to stay
//     inflated); follows likewise reverse follow_count / follower_count;
//   - drafts, answer requests (sent or received), reports the user filed,
//     reputation history, badges, verification, community memberships,
//     saves, follows and the Q&A profile are deleted;
//   - communities whose counts changed are stamped for the stats recount.
func (s *Store) AnonymizeUser(ctx context.Context, userID uuid.UUID) (Erased, error) {
	var out Erased
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("erase qa user: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The user's content, captured once with its live state, so every later
	// statement sees the same set.
	setup := []string{
		`CREATE TEMP TABLE _qa_erase_questions ON COMMIT DROP AS
			SELECT id, community_id, deleted_at IS NULL AS live FROM questions WHERE author_id = $1`,
		`CREATE TEMP TABLE _qa_erase_answers ON COMMIT DROP AS
			SELECT a.id, a.question_id, a.deleted_at IS NULL AS live, a.is_best
			FROM answers a WHERE a.author_id = $1`,
		`CREATE TEMP TABLE _qa_erase_comments ON COMMIT DROP AS
			SELECT c.id, c.answer_id, c.deleted_at IS NULL AS live
			FROM answer_comments c WHERE c.author_id = $1`,
	}
	for _, stmt := range setup {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return out, fmt.Errorf("erase qa user: %s: %w", firstLine(stmt), err)
		}
	}

	steps := []string{
		// Lock the user's rows before changing anything about them.
		`SELECT 1 FROM questions WHERE id IN (SELECT id FROM _qa_erase_questions) FOR UPDATE`,

		// Counters on surviving content shrink before the rows that earned
		// them go.
		`UPDATE questions q
			SET vote_score = q.vote_score - CASE v.vote_type WHEN 'up' THEN 1 ELSE -1 END,
			    upvote_count = GREATEST(q.upvote_count - CASE v.vote_type WHEN 'up' THEN 1 ELSE 0 END, 0),
			    downvote_count = GREATEST(q.downvote_count - CASE v.vote_type WHEN 'down' THEN 1 ELSE 0 END, 0)
			FROM question_votes v
			WHERE v.user_id = $1 AND v.question_id = q.id`,
		`UPDATE answers a
			SET vote_score = a.vote_score - CASE v.vote_type WHEN 'up' THEN 1 ELSE -1 END,
			    upvote_count = GREATEST(a.upvote_count - CASE v.vote_type WHEN 'up' THEN 1 ELSE 0 END, 0),
			    downvote_count = GREATEST(a.downvote_count - CASE v.vote_type WHEN 'down' THEN 1 ELSE 0 END, 0)
			FROM answer_votes v
			WHERE v.user_id = $1 AND v.answer_id = a.id`,
		`UPDATE answer_comments c
			SET vote_score = c.vote_score - CASE v.vote_type WHEN 'up' THEN 1 ELSE -1 END
			FROM answer_comment_votes v
			WHERE v.user_id = $1 AND v.comment_id = c.id`,
		`UPDATE questions q SET follow_count = GREATEST(q.follow_count - 1, 0)
			FROM question_follows f WHERE f.user_id = $1 AND f.question_id = q.id`,
		`UPDATE topics t SET follower_count = GREATEST(t.follower_count - 1, 0)
			FROM topic_follows f WHERE f.user_id = $1 AND f.topic_id = t.id`,
		`UPDATE questions q SET answer_count = GREATEST(q.answer_count - n.cnt, 0), updated_at = now()
			FROM (SELECT question_id, COUNT(*) AS cnt FROM _qa_erase_answers WHERE live GROUP BY question_id) n
			WHERE q.id = n.question_id`,
		`UPDATE questions q SET best_answer_id = NULL, is_answered = false, updated_at = now()
			WHERE q.best_answer_id IN (SELECT id FROM _qa_erase_answers)`,
		`UPDATE answers a SET comment_count = GREATEST(a.comment_count - n.cnt, 0)
			FROM (SELECT answer_id, COUNT(*) AS cnt FROM _qa_erase_comments WHERE live GROUP BY answer_id) n
			WHERE a.id = n.answer_id`,

		// Votes, follows, saves.
		`DELETE FROM question_votes WHERE user_id = $1`,
		`DELETE FROM answer_votes WHERE user_id = $1`,
		`DELETE FROM answer_comment_votes WHERE user_id = $1`,
		`DELETE FROM question_follows WHERE user_id = $1`,
		`DELETE FROM topic_follows WHERE user_id = $1`,
		`DELETE FROM contributor_follows WHERE follower_id = $1 OR followed_id = $1`,
		`DELETE FROM question_saves WHERE user_id = $1`,
		`DELETE FROM answer_saves WHERE user_id = $1`,

		// Text the user wrote that hangs off their content.
		`DELETE FROM question_revisions WHERE editor_id = $1 OR question_id IN (SELECT id FROM _qa_erase_questions)`,
		`DELETE FROM answer_revisions WHERE editor_id = $1 OR answer_id IN (SELECT id FROM _qa_erase_answers)`,
		`DELETE FROM question_translations WHERE question_id IN (SELECT id FROM _qa_erase_questions)`,
		`DELETE FROM answer_translations WHERE answer_id IN (SELECT id FROM _qa_erase_answers)`,
		`DELETE FROM question_tags WHERE question_id IN (SELECT id FROM _qa_erase_questions)`,
		`DELETE FROM local_scopes WHERE question_id IN (SELECT id FROM _qa_erase_questions)`,
		`DELETE FROM question_drafts WHERE author_id = $1`,
		`DELETE FROM answer_drafts WHERE author_id = $1`,
		`DELETE FROM answer_requests WHERE requester_id = $1 OR requested_user_id = $1`,
		`DELETE FROM moderation_reports WHERE reporter_id = $1`,

		// The tombstones.
		`UPDATE answer_comments SET body = '', deleted_at = COALESCE(deleted_at, now()), updated_at = now()
			WHERE id IN (SELECT id FROM _qa_erase_comments)`,

		// Reputation, badges, verification, memberships, profile.
		`DELETE FROM reputation_events WHERE user_id = $1`,
		`DELETE FROM contributor_badges WHERE user_id = $1`,
		`DELETE FROM verification_profiles WHERE user_id = $1`,
		`DELETE FROM qa_community_members WHERE user_id = $1`,
		`DELETE FROM qa_profiles WHERE user_id = $1`,
	}
	for _, stmt := range steps {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{userID}
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			return out, fmt.Errorf("erase qa user: %s: %w", firstLine(stmt), err)
		}
	}

	// Tombstone questions and answers last, collecting the ones this run
	// took down (their media is released by the caller).
	out.QuestionIDs, err = collectIDs(ctx, tx, `
		UPDATE questions q
		SET title = '`+ErasedQuestionTitle+`', body = '', body_html = '',
		    slug = 'deleted-' || left(replace(q.id::text, '-', ''), `+fmt.Sprint(slugIDDigits)+`),
		    status = 'deleted', deleted_at = COALESCE(q.deleted_at, now()), updated_at = now()
		FROM _qa_erase_questions e
		WHERE q.id = e.id
		RETURNING q.id, e.live`)
	if err != nil {
		return out, fmt.Errorf("erase qa user: questions: %w", err)
	}
	out.AnswerIDs, err = collectIDs(ctx, tx, `
		UPDATE answers a
		SET body = '', body_html = '', deleted_at = COALESCE(a.deleted_at, now()), updated_at = now()
		FROM _qa_erase_answers e
		WHERE a.id = e.id
		RETURNING a.id, e.live`)
	if err != nil {
		return out, fmt.Errorf("erase qa user: answers: %w", err)
	}

	// Community counters are recounted in the background (community_stats.go).
	if _, err := tx.Exec(ctx, `
		UPDATE qa_communities SET qa_stats_stale_at = now()
		WHERE qa_stats_stale_at IS NULL AND id IN (
			SELECT community_id FROM _qa_erase_questions WHERE community_id IS NOT NULL
			UNION
			SELECT q.community_id FROM questions q JOIN _qa_erase_answers e ON e.question_id = q.id
			WHERE q.community_id IS NOT NULL)`); err != nil {
		return out, fmt.Errorf("erase qa user: stamp communities: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Erased{}, fmt.Errorf("erase qa user: commit: %w", err)
	}
	return out, nil
}

// collectIDs runs an UPDATE ... RETURNING id, was_live and returns the ids
// that were live before it.
func collectIDs(ctx context.Context, tx pgx.Tx, sql string) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var live bool
		if err := rows.Scan(&id, &live); err != nil {
			return nil, err
		}
		if live {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// firstLine is the start of a statement, for error context (never values).
func firstLine(stmt string) string {
	for i, r := range stmt {
		if r == '\n' {
			return stmt[:i]
		}
	}
	return stmt
}
