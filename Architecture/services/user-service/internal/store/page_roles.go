package store

import (
	"context"

	"github.com/google/uuid"
)

// GetPageRole returns the active role of a user on a page ("" if none).
func (s *Store) GetPageRole(ctx context.Context, pageID, userID uuid.UUID) (string, error) {
	var role string
	err := s.db.QueryRow(ctx,
		`SELECT role FROM page_roles WHERE page_id=$1 AND user_id=$2 AND deleted_at IS NULL`,
		pageID, userID).Scan(&role)
	if err != nil {
		if isNoRows(err) {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

// IsPageOwnerOrAdmin reports whether the user holds owner/admin on the page.
func (s *Store) IsPageOwnerOrAdmin(ctx context.Context, pageID, userID uuid.UUID) (bool, error) {
	role, err := s.GetPageRole(ctx, pageID, userID)
	if err != nil {
		return false, err
	}
	return role == "owner" || role == "admin", nil
}

// AssignPageRole upserts a role for a user on a page (revives a soft-deleted
// row or inserts a new one).
func (s *Store) AssignPageRole(ctx context.Context, pageID, userID uuid.UUID, role string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE page_roles SET role=$3, deleted_at=NULL
		 WHERE page_id=$1 AND user_id=$2`, pageID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		_, err = s.db.Exec(ctx,
			`INSERT INTO page_roles (page_id, user_id, role) VALUES ($1,$2,$3)`,
			pageID, userID, role)
	}
	return err
}

// RemovePageRole soft-deletes a user's role on a page.
func (s *Store) RemovePageRole(ctx context.Context, pageID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE page_roles SET deleted_at=NOW()
		 WHERE page_id=$1 AND user_id=$2 AND deleted_at IS NULL`, pageID, userID)
	return err
}
