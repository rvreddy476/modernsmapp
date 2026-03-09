package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ReelCrossPost tracks a cross-post intent for a reel.
type ReelCrossPost struct {
	ID             uuid.UUID  `json:"id"`
	SourceReelID   uuid.UUID  `json:"source_reel_id"`
	TargetType     string     `json:"target_type"`
	TargetID       *string    `json:"target_id,omitempty"`
	Status         string     `json:"status"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
	ErrorMessage   *string    `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
}

// CreateCrossPost inserts a cross-post intent.
func (s *Store) CreateCrossPost(ctx context.Context, cp *ReelCrossPost) error {
	if cp.ID == uuid.Nil {
		cp.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO reel_crosspost (id, source_reel_id, target_type, target_id, status, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (source_reel_id, target_type, target_id) DO NOTHING
	`, cp.ID, cp.SourceReelID, cp.TargetType, cp.TargetID, cp.Status, cp.IdempotencyKey)
	return err
}

// UpdateCrossPostStatus updates the status of a cross-post.
func (s *Store) UpdateCrossPostStatus(ctx context.Context, id uuid.UUID, status string, errMsg *string) error {
	if status == "published" {
		_, err := s.db.Exec(ctx, `
			UPDATE reel_crosspost SET status = $2, published_at = NOW(), error_message = NULL
			WHERE id = $1
		`, id, status)
		return err
	}
	_, err := s.db.Exec(ctx, `
		UPDATE reel_crosspost SET status = $2, error_message = $3
		WHERE id = $1
	`, id, status, errMsg)
	return err
}

// GetCrossPostsByReel returns all cross-post records for a reel.
func (s *Store) GetCrossPostsByReel(ctx context.Context, reelID uuid.UUID) ([]ReelCrossPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_reel_id, target_type, target_id, status, idempotency_key, error_message, created_at, published_at
		FROM reel_crosspost WHERE source_reel_id = $1
		ORDER BY created_at
	`, reelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []ReelCrossPost
	for rows.Next() {
		var cp ReelCrossPost
		if err := rows.Scan(&cp.ID, &cp.SourceReelID, &cp.TargetType, &cp.TargetID,
			&cp.Status, &cp.IdempotencyKey, &cp.ErrorMessage, &cp.CreatedAt, &cp.PublishedAt); err != nil {
			return nil, err
		}
		posts = append(posts, cp)
	}
	return posts, nil
}

// GetPendingCrossPosts returns cross-posts that need to be processed.
func (s *Store) GetPendingCrossPosts(ctx context.Context, limit int) ([]ReelCrossPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_reel_id, target_type, target_id, status, idempotency_key, error_message, created_at, published_at
		FROM reel_crosspost WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []ReelCrossPost
	for rows.Next() {
		var cp ReelCrossPost
		if err := rows.Scan(&cp.ID, &cp.SourceReelID, &cp.TargetType, &cp.TargetID,
			&cp.Status, &cp.IdempotencyKey, &cp.ErrorMessage, &cp.CreatedAt, &cp.PublishedAt); err != nil {
			return nil, err
		}
		posts = append(posts, cp)
	}
	return posts, nil
}
