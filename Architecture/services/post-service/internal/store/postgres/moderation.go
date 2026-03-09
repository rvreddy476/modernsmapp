package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ModerationReview represents a moderation decision for a reel.
type ModerationReview struct {
	ID             uuid.UUID       `json:"id"`
	ReelID         uuid.UUID       `json:"reel_id"`
	ReviewerType   string          `json:"reviewer_type"`
	ReviewerID     *string         `json:"reviewer_id,omitempty"`
	Decision       string          `json:"decision"`
	Reason         *string         `json:"reason,omitempty"`
	Confidence     *float64        `json:"confidence,omitempty"`
	PolicyViolated *string         `json:"policy_violated,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// InsertModerationReview inserts a new moderation review record.
func (s *Store) InsertModerationReview(ctx context.Context, r *ModerationReview) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO moderation_reviews (id, reel_id, reviewer_type, reviewer_id, decision, reason,
		    confidence, policy_violated, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
	`, r.ID, r.ReelID, r.ReviewerType, r.ReviewerID, r.Decision, r.Reason,
		r.Confidence, r.PolicyViolated, r.Metadata)
	return err
}

// GetModerationReviewsByReel returns all moderation reviews for a reel.
func (s *Store) GetModerationReviewsByReel(ctx context.Context, reelID uuid.UUID) ([]ModerationReview, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, reel_id, reviewer_type, reviewer_id, decision, reason,
		       confidence, policy_violated, metadata, created_at
		FROM moderation_reviews WHERE reel_id = $1
		ORDER BY created_at DESC
	`, reelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []ModerationReview
	for rows.Next() {
		var r ModerationReview
		if err := rows.Scan(&r.ID, &r.ReelID, &r.ReviewerType, &r.ReviewerID,
			&r.Decision, &r.Reason, &r.Confidence, &r.PolicyViolated, &r.Metadata, &r.CreatedAt); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, nil
}

// GetLatestModerationDecision returns the most recent moderation decision for a reel.
func (s *Store) GetLatestModerationDecision(ctx context.Context, reelID uuid.UUID) (*ModerationReview, error) {
	var r ModerationReview
	err := s.db.QueryRow(ctx, `
		SELECT id, reel_id, reviewer_type, reviewer_id, decision, reason,
		       confidence, policy_violated, metadata, created_at
		FROM moderation_reviews WHERE reel_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, reelID).Scan(&r.ID, &r.ReelID, &r.ReviewerType, &r.ReviewerID,
		&r.Decision, &r.Reason, &r.Confidence, &r.PolicyViolated, &r.Metadata, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetFlaggedReels returns reels that are flagged or pending review.
func (s *Store) GetFlaggedReels(ctx context.Context, limit, offset int) ([]ModerationReview, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (reel_id) id, reel_id, reviewer_type, reviewer_id, decision, reason,
		       confidence, policy_violated, metadata, created_at
		FROM moderation_reviews
		WHERE decision IN ('flagged', 'pending_review')
		ORDER BY reel_id, created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []ModerationReview
	for rows.Next() {
		var r ModerationReview
		if err := rows.Scan(&r.ID, &r.ReelID, &r.ReviewerType, &r.ReviewerID,
			&r.Decision, &r.Reason, &r.Confidence, &r.PolicyViolated, &r.Metadata, &r.CreatedAt); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, nil
}
