package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type RightsCheck struct {
	ID          uuid.UUID  `json:"id"`
	PostID      uuid.UUID  `json:"post_id"`
	AudioID     *uuid.UUID `json:"audio_id,omitempty"`
	CheckType   string     `json:"check_type"`
	Status      string     `json:"status"`
	Provider    *string    `json:"provider,omitempty"`
	ProviderRef *string    `json:"provider_ref,omitempty"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Store) CreateRightsCheck(ctx context.Context, postID uuid.UUID, audioID *uuid.UUID, checkType, status, provider string) (*RightsCheck, error) {
	rc := &RightsCheck{}
	var providerPtr *string
	if provider != "" {
		providerPtr = &provider
	}
	now := time.Now()
	var checkedAt *time.Time
	if status == "cleared" {
		checkedAt = &now
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO media_rights_checks (post_id, audio_id, check_type, status, provider, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, post_id, audio_id, check_type, status, provider, provider_ref, checked_at, created_at`,
		postID, audioID, checkType, status, providerPtr, checkedAt,
	).Scan(&rc.ID, &rc.PostID, &rc.AudioID, &rc.CheckType, &rc.Status, &rc.Provider, &rc.ProviderRef, &rc.CheckedAt, &rc.CreatedAt)
	return rc, err
}

func (s *Store) GetRightsCheckByPost(ctx context.Context, postID uuid.UUID) (*RightsCheck, error) {
	rc := &RightsCheck{}
	err := s.db.QueryRow(ctx, `
		SELECT id, post_id, audio_id, check_type, status, provider, provider_ref, checked_at, created_at
		FROM media_rights_checks WHERE post_id = $1 ORDER BY created_at DESC LIMIT 1`, postID,
	).Scan(&rc.ID, &rc.PostID, &rc.AudioID, &rc.CheckType, &rc.Status, &rc.Provider, &rc.ProviderRef, &rc.CheckedAt, &rc.CreatedAt)
	if err != nil {
		return nil, nil // not found is OK
	}
	return rc, nil
}
