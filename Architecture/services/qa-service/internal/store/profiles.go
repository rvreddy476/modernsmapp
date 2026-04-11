package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type UpdateProfileParams struct {
	DisplayName    *string  `json:"display_name,omitempty"`
	Bio            *string  `json:"bio,omitempty"`
	ExpertiseAreas []string `json:"expertise_areas,omitempty"`
}

func (s *Store) GetOrCreateQAProfile(ctx context.Context, userID uuid.UUID) (*QAProfile, error) {
	p := &QAProfile{}
	err := s.db.QueryRow(ctx, `
		INSERT INTO qa_profiles (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET updated_at = qa_profiles.updated_at
		RETURNING user_id, display_name, bio, expertise_areas, reputation_score,
		          question_count, answer_count, best_answer_count, is_verified, created_at, updated_at`,
		userID,
	).Scan(&p.UserID, &p.DisplayName, &p.Bio, &p.ExpertiseAreas, &p.ReputationScore,
		&p.QuestionCount, &p.AnswerCount, &p.BestAnswerCount, &p.IsVerified, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get or create qa profile: %w", err)
	}
	return p, nil
}

func (s *Store) GetQAProfile(ctx context.Context, userID uuid.UUID) (*QAProfile, error) {
	p := &QAProfile{}
	err := s.db.QueryRow(ctx, `
		SELECT user_id, display_name, bio, expertise_areas, reputation_score,
		       question_count, answer_count, best_answer_count, is_verified, created_at, updated_at
		FROM qa_profiles WHERE user_id = $1`, userID,
	).Scan(&p.UserID, &p.DisplayName, &p.Bio, &p.ExpertiseAreas, &p.ReputationScore,
		&p.QuestionCount, &p.AnswerCount, &p.BestAnswerCount, &p.IsVerified, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get qa profile: %w", err)
	}
	return p, nil
}

func (s *Store) UpdateQAProfile(ctx context.Context, userID uuid.UUID, p UpdateProfileParams) (*QAProfile, error) {
	if p.DisplayName != nil {
		_, _ = s.db.Exec(ctx, `UPDATE qa_profiles SET display_name = $2, updated_at = now() WHERE user_id = $1`, userID, *p.DisplayName)
	}
	if p.Bio != nil {
		_, _ = s.db.Exec(ctx, `UPDATE qa_profiles SET bio = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Bio)
	}
	if p.ExpertiseAreas != nil {
		_, _ = s.db.Exec(ctx, `UPDATE qa_profiles SET expertise_areas = $2, updated_at = now() WHERE user_id = $1`, userID, p.ExpertiseAreas)
	}
	return s.GetQAProfile(ctx, userID)
}

func scanProfiles(rows pgx.Rows) ([]QAProfile, error) {
	var results []QAProfile
	for rows.Next() {
		var p QAProfile
		if err := rows.Scan(&p.UserID, &p.DisplayName, &p.Bio, &p.ExpertiseAreas, &p.ReputationScore,
			&p.QuestionCount, &p.AnswerCount, &p.BestAnswerCount, &p.IsVerified, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		results = append(results, p)
	}
	return results, nil
}
