package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SlugHistory tracks slug changes for SEO redirects.
type SlugHistory struct {
	ID        uuid.UUID `json:"id"`
	ReelID    uuid.UUID `json:"reel_id"`
	OldSlug   string    `json:"old_slug"`
	NewSlug   string    `json:"new_slug"`
	ChangedAt time.Time `json:"changed_at"`
}

// InsertSlugHistory records a slug change.
func (s *Store) InsertSlugHistory(ctx context.Context, reelID uuid.UUID, oldSlug, newSlug string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO slug_history (reel_id, old_slug, new_slug, changed_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (old_slug) DO UPDATE SET new_slug = EXCLUDED.new_slug, changed_at = NOW()
	`, reelID, oldSlug, newSlug)
	return err
}

// LookupSlugRedirect finds the current reel_id for an old slug.
// Returns the reel_id and latest slug, or nil if not found.
func (s *Store) LookupSlugRedirect(ctx context.Context, oldSlug string) (*uuid.UUID, *string, error) {
	var reelID uuid.UUID
	var newSlug string
	err := s.db.QueryRow(ctx, `
		SELECT reel_id, new_slug FROM slug_history WHERE old_slug = $1
	`, oldSlug).Scan(&reelID, &newSlug)
	if err != nil {
		return nil, nil, err
	}
	return &reelID, &newSlug, nil
}

// GetSlugHistoryByReel returns all slug changes for a reel.
func (s *Store) GetSlugHistoryByReel(ctx context.Context, reelID uuid.UUID) ([]SlugHistory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, reel_id, old_slug, new_slug, changed_at
		FROM slug_history WHERE reel_id = $1
		ORDER BY changed_at DESC
	`, reelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []SlugHistory
	for rows.Next() {
		var h SlugHistory
		if err := rows.Scan(&h.ID, &h.ReelID, &h.OldSlug, &h.NewSlug, &h.ChangedAt); err != nil {
			return nil, err
		}
		history = append(history, h)
	}
	return history, nil
}
