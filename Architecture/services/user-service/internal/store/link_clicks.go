package store

import (
	"context"

	"github.com/google/uuid"
)

// Profile link click counting (POST /v1/users/:userId/links/:platform/click).
// Only the per-link total is stored; who clicked is never written here.

// UserLinkExists reports whether owner has a link for platform.
func (s *Store) UserLinkExists(ctx context.Context, ownerID uuid.UUID, platform string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_links WHERE user_id = $1 AND platform = $2)`,
		ownerID, platform).Scan(&ok)
	return ok, err
}

// IncrementLinkClick adds one to the link's click_count. A link removed
// since UserLinkExists is a no-op.
func (s *Store) IncrementLinkClick(ctx context.Context, ownerID uuid.UUID, platform string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE user_links SET click_count = COALESCE(click_count, 0) + 1 WHERE user_id = $1 AND platform = $2`,
		ownerID, platform)
	return err
}
