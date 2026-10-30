package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/qa-service/internal/qarender"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateAnswer stores a new answer. body_html is always rendered from body
// (qarender, audit H3); the client's HTML argument is ignored.
func (s *Store) CreateAnswer(ctx context.Context, questionID, authorID uuid.UUID, body, _ string, isAnonymous bool) (*Answer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	a := &Answer{}
	err = tx.QueryRow(ctx, `
		INSERT INTO answers (question_id, author_id, body, body_html, is_anonymous)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, question_id, author_id, body, body_html, vote_score, upvote_count, downvote_count,
		          is_best, is_accepted, comment_count, reference_count, created_at, updated_at, COALESCE(is_anonymous, false)`,
		questionID, authorID, body, qarender.BodyHTML(body), isAnonymous,
	).Scan(&a.ID, &a.QuestionID, &a.AuthorID, &a.Body, &a.BodyHTML, &a.VoteScore,
		&a.UpvoteCount, &a.DownvoteCount, &a.IsBest, &a.IsAccepted,
		&a.CommentCount, &a.ReferenceCount, &a.CreatedAt, &a.UpdatedAt, &a.IsAnonymous)
	if err != nil {
		return nil, fmt.Errorf("insert answer: %w", err)
	}

	_, _ = tx.Exec(ctx, `UPDATE questions SET answer_count = answer_count + 1, updated_at = now() WHERE id = $1`, questionID)
	_, _ = tx.Exec(ctx, `
		INSERT INTO qa_profiles (user_id, answer_count) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET answer_count = qa_profiles.answer_count + 1, updated_at = now()`,
		authorID)

	var communityIDText string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(community_id::text, '') FROM questions WHERE id = $1`, questionID).Scan(&communityIDText); err == nil && communityIDText != "" {
		communityID, parseErr := uuid.Parse(communityIDText)
		if parseErr == nil {
			if err := s.syncCommunityAnswerStatsTx(ctx, tx, communityID); err != nil {
				return nil, err
			}
			if err := s.bumpCommunityTopicAnswerCountTx(ctx, tx, communityID, questionID); err != nil {
				return nil, err
			}
		}
	}

	// qa.answer.created commits with the row and names the question's author
	// as the recipient (audit L2, M10).
	var questionAuthorID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT author_id FROM questions WHERE id = $1`, questionID).Scan(&questionAuthorID); err != nil {
		return nil, fmt.Errorf("load question author: %w", err)
	}
	if err := enqueueAnswerCreated(ctx, tx, a, questionAuthorID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) GetAnswer(ctx context.Context, answerID uuid.UUID) (*Answer, error) {
	a := &Answer{}
	err := s.db.QueryRow(ctx, `
		SELECT id, question_id, author_id, body, body_html, vote_score, upvote_count, downvote_count,
		       is_best, is_accepted, comment_count, reference_count, created_at, updated_at, deleted_at,
		       COALESCE(is_anonymous, false)
		FROM answers WHERE id = $1 AND deleted_at IS NULL`, answerID,
	).Scan(&a.ID, &a.QuestionID, &a.AuthorID, &a.Body, &a.BodyHTML, &a.VoteScore,
		&a.UpvoteCount, &a.DownvoteCount, &a.IsBest, &a.IsAccepted,
		&a.CommentCount, &a.ReferenceCount, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt, &a.IsAnonymous)
	if err != nil {
		return nil, fmt.Errorf("get answer: %w", err)
	}
	a.BodyHTML = qarender.BodyHTML(a.Body) // never the stored column (H3)
	refs, _ := s.GetAnswerReferences(ctx, answerID)
	a.References = refs
	return a, nil
}

// UpdateAnswer rewrites an answer's body (audit L3, H3, L2): the replaced
// body is kept as an answer_revisions row in the same transaction, body_html
// is rendered from the body (the client's HTML argument is ignored), and
// qa.answer.updated commits with the edit. An edit that changes nothing
// writes nothing.
func (s *Store) UpdateAnswer(ctx context.Context, answerID uuid.UUID, body, _ string) (*Answer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldBody string
	var questionID, authorID uuid.UUID
	var anonymous bool
	if err := tx.QueryRow(ctx, `
		SELECT body, question_id, author_id, COALESCE(is_anonymous, false)
		FROM answers WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, answerID,
	).Scan(&oldBody, &questionID, &authorID, &anonymous); err != nil {
		return nil, fmt.Errorf("update answer: %w", err)
	}
	if body != oldBody {
		var revisionID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO answer_revisions (answer_id, editor_id, body)
			VALUES ($1, $2, $3)
			RETURNING id`, answerID, authorID, oldBody,
		).Scan(&revisionID); err != nil {
			return nil, fmt.Errorf("record answer revision: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE answers SET body = $2, body_html = $3, updated_at = now()
			WHERE id = $1`, answerID, body, qarender.BodyHTML(body)); err != nil {
			return nil, fmt.Errorf("update answer: %w", err)
		}
		if err := enqueueAnswerUpdated(ctx, tx, answerID, questionID, authorID, revisionID, anonymous); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetAnswer(ctx, answerID)
}

func (s *Store) DeleteAnswer(ctx context.Context, answerID, actorID uuid.UUID) (bool, error) {
	var questionID, authorID uuid.UUID
	var deleted bool
	err := s.db.QueryRow(ctx, `SELECT question_id, author_id, deleted_at IS NOT NULL FROM answers WHERE id = $1`, answerID).
		Scan(&questionID, &authorID, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, refuse(ErrNotFound, "answer not found")
	}
	if err != nil {
		return false, err
	}
	if authorID != actorID {
		return false, refuse(ErrForbidden, "only the answer author can delete it")
	}
	if deleted {
		return false, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The question row first, like every accept change (best_answer.go).
	var communityID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT community_id FROM questions WHERE id = $1 FOR UPDATE`, questionID).Scan(&communityID); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `UPDATE answers SET deleted_at = now() WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL`, answerID, actorID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // a concurrent delete won; it moved the counters
	}
	if _, err := tx.Exec(ctx, `UPDATE questions SET answer_count = GREATEST(answer_count - 1, 0), updated_at = now() WHERE id = $1`, questionID); err != nil {
		return false, err
	}
	if err := releaseBestAnswerTx(ctx, tx, questionID, answerID); err != nil {
		return false, err
	}
	if communityID != nil {
		if err := s.syncCommunityAnswerStatsTx(ctx, tx, *communityID); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

func (s *Store) ListAnswersByQuestion(ctx context.Context, questionID uuid.UUID, sortBy string, limit, offset int) ([]Answer, error) {
	if limit <= 0 {
		limit = 20
	}
	orderBy := "created_at ASC"
	switch sortBy {
	case "votes":
		orderBy = "vote_score DESC, created_at ASC"
	case "newest":
		orderBy = "created_at DESC"
	case "oldest":
		orderBy = "created_at ASC"
	}
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT id, question_id, author_id, body, body_html, vote_score, upvote_count, downvote_count,
		       is_best, is_accepted, comment_count, reference_count, created_at, updated_at,
		       COALESCE(is_anonymous, false)
		FROM answers WHERE question_id = $1 AND deleted_at IS NULL
		ORDER BY is_best DESC, %s LIMIT $2 OFFSET $3`, orderBy), questionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnswers(rows)
}

func (s *Store) AddAnswerReferences(ctx context.Context, answerID uuid.UUID, refs []AnswerReference) error {
	for i, ref := range refs {
		_, _ = s.db.Exec(ctx, `
			INSERT INTO answer_references (answer_id, url, title, description, sort_order)
			VALUES ($1, $2, $3, $4, $5)`, answerID, ref.URL, ref.Title, ref.Description, i)
	}
	_, _ = s.db.Exec(ctx, `UPDATE answers SET reference_count = (SELECT count(*) FROM answer_references WHERE answer_id = $1) WHERE id = $1`, answerID)
	return nil
}

func (s *Store) GetAnswerReferences(ctx context.Context, answerID uuid.UUID) ([]AnswerReference, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, answer_id, url, title, description, sort_order
		FROM answer_references WHERE answer_id = $1 ORDER BY sort_order`, answerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AnswerReference
	for rows.Next() {
		var r AnswerReference
		if err := rows.Scan(&r.ID, &r.AnswerID, &r.URL, &r.Title, &r.Description, &r.SortOrder); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// ListAnswersByAuthor is a profile's answer list. The author sees every live
// answer of theirs on a live question. Anyone else never sees an anonymous
// answer (audit H4), nor one on a question they could not see (H5).
func (s *Store) ListAnswersByAuthor(ctx context.Context, authorID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]Answer, error) {
	if limit <= 0 {
		limit = 20
	}
	self := viewerID != nil && *viewerID == authorID
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.question_id, a.author_id, a.body, a.body_html, a.vote_score,
		       a.upvote_count, a.downvote_count, a.is_best, a.is_accepted,
		       a.comment_count, a.reference_count, a.created_at, a.updated_at,
		       COALESCE(a.is_anonymous, false)
		FROM answers a JOIN questions q ON q.id = a.question_id
		WHERE a.author_id = $1 AND a.deleted_at IS NULL AND q.deleted_at IS NULL
		  AND ($5 OR (NOT COALESCE(a.is_anonymous, false) AND `+questionVisibleSQL("q", "$4")+`))
		ORDER BY a.created_at DESC LIMIT $2 OFFSET $3`, authorID, limit, offset, viewerID, self)
	if err != nil {
		return nil, fmt.Errorf("list answers by author: %w", err)
	}
	defer rows.Close()
	return scanAnswers(rows)
}

func scanAnswers(rows pgx.Rows) ([]Answer, error) {
	var results []Answer
	for rows.Next() {
		var a Answer
		if err := rows.Scan(&a.ID, &a.QuestionID, &a.AuthorID, &a.Body, &a.BodyHTML, &a.VoteScore,
			&a.UpvoteCount, &a.DownvoteCount, &a.IsBest, &a.IsAccepted,
			&a.CommentCount, &a.ReferenceCount, &a.CreatedAt, &a.UpdatedAt, &a.IsAnonymous); err != nil {
			return nil, err
		}
		a.BodyHTML = qarender.BodyHTML(a.Body) // never the stored column (H3)
		results = append(results, a)
	}
	return results, nil
}

// syncCommunityAnswerStatsTx records that a community's answer and
// contributor counts changed. It no longer recounts inside the caller's
// transaction (audit M7): it stamps the community for the background
// recount (community_stats.go), which takes no lock when the community is
// already stamped.
func (s *Store) syncCommunityAnswerStatsTx(ctx context.Context, exec dbtx, communityID uuid.UUID) error {
	return markCommunityStatsStaleTx(ctx, exec, communityID)
}

func (s *Store) bumpCommunityTopicAnswerCountTx(ctx context.Context, exec dbtx, communityID, questionID uuid.UUID) error {
	rows, err := exec.Query(ctx, `SELECT topic_id FROM question_topics WHERE question_id = $1`, questionID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var topicID uuid.UUID
		if err := rows.Scan(&topicID); err != nil {
			return err
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO community_topic_affinity (community_id, topic_id, answer_count, affinity_score)
			VALUES ($1, $2, 1, 0.5)
			ON CONFLICT (community_id, topic_id) DO UPDATE SET
				answer_count = community_topic_affinity.answer_count + 1,
				affinity_score = (
					community_topic_affinity.question_count +
					(community_topic_affinity.answer_count + 1) * 0.5 +
					community_topic_affinity.view_count * 0.05
				),
				updated_at = now()`,
			communityID, topicID,
		); err != nil {
			return err
		}
	}
	return rows.Err()
}
