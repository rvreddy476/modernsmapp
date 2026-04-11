package store

import (
	"context"

	"github.com/google/uuid"
)

func (s *Store) FollowQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO question_follows (user_id, question_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, questionID)
	if err == nil {
		_, _ = s.db.Exec(ctx, `UPDATE questions SET follow_count = (SELECT count(*) FROM question_follows WHERE question_id = $1) WHERE id = $1`, questionID)
	}
	return err
}

func (s *Store) UnfollowQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM question_follows WHERE user_id = $1 AND question_id = $2`, userID, questionID)
	if err == nil {
		_, _ = s.db.Exec(ctx, `UPDATE questions SET follow_count = (SELECT count(*) FROM question_follows WHERE question_id = $1) WHERE id = $1`, questionID)
	}
	return err
}

func (s *Store) IsFollowingQuestion(ctx context.Context, userID, questionID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM question_follows WHERE user_id = $1 AND question_id = $2)`, userID, questionID).Scan(&exists)
	return exists, err
}

func (s *Store) GetQuestionFollowCount(ctx context.Context, questionID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM question_follows WHERE question_id = $1`, questionID).Scan(&count)
	return count, err
}

func (s *Store) FollowTopic(ctx context.Context, userID, topicID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `INSERT INTO topic_follows (user_id, topic_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, topicID)
	if err == nil {
		_, _ = s.db.Exec(ctx, `UPDATE topics SET follower_count = (SELECT count(*) FROM topic_follows WHERE topic_id = $1) WHERE id = $1`, topicID)
	}
	return err
}

func (s *Store) UnfollowTopic(ctx context.Context, userID, topicID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM topic_follows WHERE user_id = $1 AND topic_id = $2`, userID, topicID)
	if err == nil {
		_, _ = s.db.Exec(ctx, `UPDATE topics SET follower_count = (SELECT count(*) FROM topic_follows WHERE topic_id = $1) WHERE id = $1`, topicID)
	}
	return err
}

func (s *Store) IsFollowingTopic(ctx context.Context, userID, topicID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM topic_follows WHERE user_id = $1 AND topic_id = $2)`, userID, topicID).Scan(&exists)
	return exists, err
}

func (s *Store) GetFollowedTopics(ctx context.Context, userID uuid.UUID) ([]Topic, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.icon_url, t.parent_topic_id,
		       t.question_count, t.follower_count, t.is_featured, t.created_at
		FROM topics t JOIN topic_follows tf ON t.id = tf.topic_id
		WHERE tf.user_id = $1 ORDER BY t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTopics(rows)
}

func (s *Store) FollowContributor(ctx context.Context, followerID, followedID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `INSERT INTO contributor_follows (follower_id, followed_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, followerID, followedID)
	return err
}

func (s *Store) UnfollowContributor(ctx context.Context, followerID, followedID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM contributor_follows WHERE follower_id = $1 AND followed_id = $2`, followerID, followedID)
	return err
}

func (s *Store) GetFollowedContributors(ctx context.Context, userID uuid.UUID, limit, offset int) ([]QAProfile, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.user_id, p.display_name, p.bio, p.expertise_areas, p.reputation_score,
		       p.question_count, p.answer_count, p.best_answer_count, p.is_verified, p.created_at, p.updated_at
		FROM qa_profiles p JOIN contributor_follows cf ON p.user_id = cf.followed_id
		WHERE cf.follower_id = $1 ORDER BY p.reputation_score DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProfiles(rows)
}
