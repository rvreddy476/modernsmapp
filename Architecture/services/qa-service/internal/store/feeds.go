package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) GetTrendingQuestions(ctx context.Context, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, title, slug, status, vote_score, answer_count, view_count, is_answered, created_at,
		       COALESCE(is_anonymous, false)
		FROM questions
		WHERE deleted_at IS NULL AND status = 'open'
		  AND created_at > now() - interval '7 days'
		ORDER BY (vote_score + answer_count * 2 + view_count * 0.1) DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) GetUnansweredQuestions(ctx context.Context, topicID *uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	if topicID != nil {
		rows, err := s.db.Query(ctx, `
			SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
			       COALESCE(q.is_anonymous, false)
			FROM questions q JOIN question_topics qt ON q.id = qt.question_id
			WHERE qt.topic_id = $1 AND q.answer_count = 0 AND q.status = 'open' AND q.deleted_at IS NULL
			ORDER BY q.created_at DESC LIMIT $2 OFFSET $3`, *topicID, limit, offset)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanQuestionSummaries(rows)
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, title, slug, status, vote_score, answer_count, view_count, is_answered, created_at,
		       COALESCE(is_anonymous, false)
		FROM questions WHERE answer_count = 0 AND status = 'open' AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) GetFollowingFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q
		LEFT JOIN question_topics qt ON q.id = qt.question_id
		LEFT JOIN topic_follows tf ON qt.topic_id = tf.topic_id AND tf.user_id = $1
		LEFT JOIN contributor_follows cf ON q.author_id = cf.followed_id AND cf.follower_id = $1
		WHERE q.deleted_at IS NULL AND q.status != 'deleted'
		  AND (tf.user_id IS NOT NULL OR cf.follower_id IS NOT NULL)
		ORDER BY q.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) GetForYouFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	// V1: questions in user's expertise topics, falling back to trending
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q
		JOIN question_topics qt ON q.id = qt.question_id
		JOIN topics t ON qt.topic_id = t.id
		JOIN qa_profiles p ON p.user_id = $1
		WHERE q.deleted_at IS NULL AND q.status = 'open'
		  AND t.name = ANY(p.expertise_areas)
		ORDER BY q.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results, _ := scanQuestionSummaries(rows)
	if len(results) < limit {
		// fallback to trending
		extra, _ := s.GetTrendingQuestions(ctx, limit-len(results), 0)
		results = append(results, extra...)
	}
	return results, nil
}

func (s *Store) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	// Mix of followed + recent popular
	following, _ := s.GetFollowingFeed(ctx, userID, limit/2, 0)
	trending, _ := s.GetTrendingQuestions(ctx, limit/2, 0)
	seen := map[uuid.UUID]bool{}
	var results []QuestionSummary
	for _, q := range following {
		if !seen[q.ID] {
			results = append(results, q)
			seen[q.ID] = true
		}
	}
	for _, q := range trending {
		if !seen[q.ID] {
			results = append(results, q)
			seen[q.ID] = true
		}
	}
	if offset < len(results) {
		end := offset + limit
		if end > len(results) {
			end = len(results)
		}
		return results[offset:end], nil
	}
	return results, nil
}

func (s *Store) GetAnswerQueue(ctx context.Context, userID uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q
		JOIN question_topics qt ON q.id = qt.question_id
		JOIN topics t ON qt.topic_id = t.id
		JOIN qa_profiles p ON p.user_id = $1
		WHERE q.deleted_at IS NULL AND q.status = 'open' AND q.answer_count = 0
		  AND t.name = ANY(p.expertise_areas)
		ORDER BY q.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) GetLocalFeed(ctx context.Context, lat, lng float64, radiusKm int, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	if radiusKm <= 0 {
		radiusKm = 50
	}
	// Simple bounding box approximation
	latDelta := float64(radiusKm) / 111.0
	lngDelta := float64(radiusKm) / 111.0

	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q JOIN local_scopes ls ON q.id = ls.question_id
		WHERE q.deleted_at IS NULL AND q.status = 'open'
		  AND ls.latitude BETWEEN $1 - %f AND $1 + %f
		  AND ls.longitude BETWEEN $2 - %f AND $2 + %f
		ORDER BY q.created_at DESC LIMIT $3 OFFSET $4`, latDelta, latDelta, lngDelta, lngDelta),
		lat, lng, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

// GDPR cleanup
func (s *Store) AnonymizeUser(ctx context.Context, userID uuid.UUID) error {
	_, _ = s.db.Exec(ctx, `UPDATE qa_profiles SET display_name = 'Deleted User', bio = '', expertise_areas = '{}', updated_at = now() WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `UPDATE questions SET deleted_at = now(), status = 'deleted' WHERE author_id = $1 AND deleted_at IS NULL`, userID)
	_, _ = s.db.Exec(ctx, `UPDATE answers SET deleted_at = now() WHERE author_id = $1 AND deleted_at IS NULL`, userID)
	_, _ = s.db.Exec(ctx, `UPDATE answer_comments SET deleted_at = now() WHERE author_id = $1 AND deleted_at IS NULL`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM question_votes WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM answer_votes WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM answer_comment_votes WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM question_follows WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM topic_follows WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM contributor_follows WHERE follower_id = $1 OR followed_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM question_saves WHERE user_id = $1`, userID)
	_, _ = s.db.Exec(ctx, `DELETE FROM answer_saves WHERE user_id = $1`, userID)
	return nil
}
