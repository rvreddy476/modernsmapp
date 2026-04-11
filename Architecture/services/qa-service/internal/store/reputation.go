package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) AddReputationEvent(ctx context.Context, userID uuid.UUID, eventType string, points int, sourceType string, sourceID *uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO reputation_events (user_id, event_type, points, source_type, source_id)
		VALUES ($1, $2, $3, $4, $5)`, userID, eventType, points, sourceType, sourceID)
	if err != nil {
		return fmt.Errorf("insert reputation event: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO qa_profiles (user_id, reputation_score) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET reputation_score = qa_profiles.reputation_score + $2, updated_at = now()`,
		userID, points)
	if err != nil {
		return fmt.Errorf("update reputation score: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *Store) GetReputationHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]ReputationEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, event_type, points, source_type, source_id, created_at
		FROM reputation_events WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ReputationEvent
	for rows.Next() {
		var e ReputationEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.EventType, &e.Points, &e.SourceType, &e.SourceID, &e.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	return results, nil
}

func (s *Store) GetReputationScore(ctx context.Context, userID uuid.UUID) (int, error) {
	var score int
	err := s.db.QueryRow(ctx, `SELECT COALESCE(reputation_score, 0) FROM qa_profiles WHERE user_id = $1`, userID).Scan(&score)
	return score, err
}

func (s *Store) AwardBadge(ctx context.Context, userID uuid.UUID, badgeType, badgeName string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO contributor_badges (user_id, badge_type, badge_name) VALUES ($1, $2, $3)`,
		userID, badgeType, badgeName)
	return err
}

func (s *Store) GetBadges(ctx context.Context, userID uuid.UUID) ([]ContributorBadge, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, badge_type, badge_name, awarded_at
		FROM contributor_badges WHERE user_id = $1 ORDER BY awarded_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ContributorBadge
	for rows.Next() {
		var b ContributorBadge
		if err := rows.Scan(&b.ID, &b.UserID, &b.BadgeType, &b.BadgeName, &b.AwardedAt); err != nil {
			return nil, err
		}
		results = append(results, b)
	}
	return results, nil
}

func (s *Store) GetLeaderboard(ctx context.Context, topicID *uuid.UUID, limit int) ([]QAProfile, error) {
	if limit <= 0 {
		limit = 20
	}
	if topicID != nil {
		rows, err := s.db.Query(ctx, `
			SELECT DISTINCT p.user_id, p.display_name, p.bio, p.expertise_areas, p.reputation_score,
			       p.question_count, p.answer_count, p.best_answer_count, p.is_verified, p.created_at, p.updated_at
			FROM qa_profiles p
			JOIN answers a ON a.author_id = p.user_id
			JOIN question_topics qt ON qt.question_id = a.question_id
			WHERE qt.topic_id = $1
			ORDER BY p.reputation_score DESC LIMIT $2`, *topicID, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanProfiles(rows)
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, display_name, bio, expertise_areas, reputation_score,
		       question_count, answer_count, best_answer_count, is_verified, created_at, updated_at
		FROM qa_profiles ORDER BY reputation_score DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProfiles(rows)
}
