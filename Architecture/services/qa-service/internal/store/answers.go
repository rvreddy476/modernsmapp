package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateAnswer(ctx context.Context, questionID, authorID uuid.UUID, body, bodyHTML string, isAnonymous bool) (*Answer, error) {
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
		questionID, authorID, body, bodyHTML, isAnonymous,
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
	refs, _ := s.GetAnswerReferences(ctx, answerID)
	a.References = refs
	return a, nil
}

func (s *Store) UpdateAnswer(ctx context.Context, answerID uuid.UUID, body, bodyHTML string) (*Answer, error) {
	_, err := s.db.Exec(ctx, `
		UPDATE answers SET body = $2, body_html = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, answerID, body, bodyHTML)
	if err != nil {
		return nil, fmt.Errorf("update answer: %w", err)
	}
	return s.GetAnswer(ctx, answerID)
}

func (s *Store) DeleteAnswer(ctx context.Context, answerID uuid.UUID) error {
	var questionID uuid.UUID
	var communityIDText string
	err := s.db.QueryRow(ctx, `
		SELECT a.question_id, COALESCE(q.community_id::text, '')
		FROM answers a
		JOIN questions q ON q.id = a.question_id
		WHERE a.id = $1`, answerID,
	).Scan(&questionID, &communityIDText)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE answers SET deleted_at = now() WHERE id = $1`, answerID)
	_, _ = tx.Exec(ctx, `UPDATE questions SET answer_count = GREATEST(answer_count - 1, 0), updated_at = now() WHERE id = $1`, questionID)
	if communityIDText != "" {
		if communityID, parseErr := uuid.Parse(communityIDText); parseErr == nil {
			if err := s.syncCommunityAnswerStatsTx(ctx, tx, communityID); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
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

func (s *Store) SelectBestAnswer(ctx context.Context, questionID, answerID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE answers SET is_best = false WHERE question_id = $1 AND is_best = true`, questionID)
	_, _ = tx.Exec(ctx, `UPDATE answers SET is_best = true WHERE id = $1`, answerID)
	_, _ = tx.Exec(ctx, `UPDATE questions SET best_answer_id = $2, is_answered = true, updated_at = now() WHERE id = $1`, questionID, answerID)

	var authorID uuid.UUID
	_ = tx.QueryRow(ctx, `SELECT author_id FROM answers WHERE id = $1`, answerID).Scan(&authorID)
	_, _ = tx.Exec(ctx, `
		INSERT INTO qa_profiles (user_id, best_answer_count) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET best_answer_count = qa_profiles.best_answer_count + 1, updated_at = now()`,
		authorID)

	return tx.Commit(ctx)
}

func (s *Store) UnselectBestAnswer(ctx context.Context, questionID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE answers SET is_best = false WHERE question_id = $1 AND is_best = true`, questionID)
	_, _ = tx.Exec(ctx, `UPDATE questions SET best_answer_id = NULL, is_answered = false, updated_at = now() WHERE id = $1`, questionID)
	return tx.Commit(ctx)
}

func (s *Store) AddAnswerMedia(ctx context.Context, answerID uuid.UUID, mediaIDs []string) error {
	for i, mid := range mediaIDs {
		parsed, err := uuid.Parse(mid)
		if err != nil {
			continue
		}
		_, _ = s.db.Exec(ctx, `INSERT INTO answer_media (answer_id, media_id, sort_order) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, answerID, parsed, i)
	}
	return nil
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

func (s *Store) ListAnswersByAuthor(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]Answer, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, question_id, author_id, body, body_html, vote_score,
		       upvote_count, downvote_count, is_best, is_accepted,
		       comment_count, reference_count, created_at, updated_at,
		       COALESCE(is_anonymous, false)
		FROM answers WHERE author_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, authorID, limit, offset)
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
		results = append(results, a)
	}
	return results, nil
}

func (s *Store) syncCommunityAnswerStatsTx(ctx context.Context, exec dbtx, communityID uuid.UUID) error {
	var answerCount int
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM answers a
		JOIN questions q ON q.id = a.question_id
		WHERE q.community_id = $1 AND q.deleted_at IS NULL AND a.deleted_at IS NULL`,
		communityID,
	).Scan(&answerCount); err != nil {
		return err
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO community_qa_settings (community_id)
		VALUES ($1)
		ON CONFLICT (community_id) DO NOTHING`,
		communityID,
	); err != nil {
		return err
	}

	if _, err := exec.Exec(ctx, `
		UPDATE community_qa_settings
		SET total_answers_count = $2,
		    updated_at = now()
		WHERE community_id = $1`,
		communityID, answerCount,
	); err != nil {
		return err
	}

	if _, err := exec.Exec(ctx, `
		UPDATE qa_communities
		SET qa_answer_count = $2,
		    last_qa_activity_at = now(),
		    updated_at = now()
		WHERE id = $1`,
		communityID, answerCount,
	); err != nil {
		return err
	}

	return s.refreshCommunityContributorCountTx(ctx, exec, communityID)
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
