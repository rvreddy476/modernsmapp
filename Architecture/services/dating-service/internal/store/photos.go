package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreatePhotoParams is the payload accepted by POST /v1/dating/photos.
type CreatePhotoParams struct {
	MediaID    uuid.UUID `json:"media_id"`
	SortOrder  int       `json:"sort_order"`
	IsPrimary  bool      `json:"is_primary"`
	Visibility string    `json:"visibility"`
}

// UpdatePhotoParams is the payload accepted by PATCH /v1/dating/photos/:id.
// All fields are optional — only non-nil fields are written.
type UpdatePhotoParams struct {
	SortOrder  *int    `json:"sort_order,omitempty"`
	IsPrimary  *bool   `json:"is_primary,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
}

// ErrPhotoNotFound is returned when no photo matches the id+user combination.
var ErrPhotoNotFound = errors.New("not_found: photo not found")

func scanPhoto(row pgx.Row) (*Photo, error) {
	p := &Photo{}
	err := row.Scan(&p.ID, &p.UserID, &p.MediaID, &p.SortOrder, &p.IsPrimary,
		&p.Visibility, &p.ModerationStatus, &p.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPhotoNotFound
		}
		return nil, fmt.Errorf("scan photo: %w", err)
	}
	return p, nil
}

// ListPhotos returns the user's photos in sort_order ascending.
func (s *Store) ListPhotos(ctx context.Context, userID uuid.UUID) ([]Photo, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, media_id, sort_order, is_primary, visibility, moderation_status, created_at
        FROM dating_photos WHERE user_id = $1
        ORDER BY sort_order ASC, created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}
	defer rows.Close()

	var out []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.ID, &p.UserID, &p.MediaID, &p.SortOrder, &p.IsPrimary,
			&p.Visibility, &p.ModerationStatus, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan photo row: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// CreatePhoto inserts a new photo for the user. If is_primary is true, all
// other photos for this user are demoted in the same transaction.
func (s *Store) CreatePhoto(ctx context.Context, userID uuid.UUID, p CreatePhotoParams) (*Photo, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if p.IsPrimary {
		if _, err := tx.Exec(ctx, `UPDATE dating_photos SET is_primary = false WHERE user_id = $1`, userID); err != nil {
			return nil, fmt.Errorf("demote primaries: %w", err)
		}
	}

	visibility := p.Visibility
	if visibility == "" {
		visibility = "public"
	}

	row := tx.QueryRow(ctx, `
        INSERT INTO dating_photos (user_id, media_id, sort_order, is_primary, visibility, moderation_status)
        VALUES ($1, $2, $3, $4, $5, 'pending')
        RETURNING id, user_id, media_id, sort_order, is_primary, visibility, moderation_status, created_at`,
		userID, p.MediaID, p.SortOrder, p.IsPrimary, visibility)
	out, err := scanPhoto(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit photo insert: %w", err)
	}
	return out, nil
}

// UpdatePhoto partially updates a photo owned by the user. Returns
// ErrPhotoNotFound if no row matches.
func (s *Store) UpdatePhoto(ctx context.Context, userID, photoID uuid.UUID, p UpdatePhotoParams) (*Photo, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if p.IsPrimary != nil && *p.IsPrimary {
		if _, err := tx.Exec(ctx, `UPDATE dating_photos SET is_primary = false WHERE user_id = $1`, userID); err != nil {
			return nil, fmt.Errorf("demote primaries: %w", err)
		}
	}

	if p.SortOrder != nil {
		if _, err := tx.Exec(ctx, `UPDATE dating_photos SET sort_order = $3 WHERE id = $1 AND user_id = $2`, photoID, userID, *p.SortOrder); err != nil {
			return nil, fmt.Errorf("update sort_order: %w", err)
		}
	}
	if p.IsPrimary != nil {
		if _, err := tx.Exec(ctx, `UPDATE dating_photos SET is_primary = $3 WHERE id = $1 AND user_id = $2`, photoID, userID, *p.IsPrimary); err != nil {
			return nil, fmt.Errorf("update is_primary: %w", err)
		}
	}
	if p.Visibility != nil {
		if _, err := tx.Exec(ctx, `UPDATE dating_photos SET visibility = $3 WHERE id = $1 AND user_id = $2`, photoID, userID, *p.Visibility); err != nil {
			return nil, fmt.Errorf("update visibility: %w", err)
		}
	}

	row := tx.QueryRow(ctx, `
        SELECT id, user_id, media_id, sort_order, is_primary, visibility, moderation_status, created_at
        FROM dating_photos WHERE id = $1 AND user_id = $2`, photoID, userID)
	out, err := scanPhoto(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit photo update: %w", err)
	}
	return out, nil
}

// SetPhotoModerationStatus flips moderation_status on a photo.
// Admin / scanner / consumer driven — never called from the
// user-facing UpdatePhoto path. Returns the updated row (with the
// owning user_id) so the caller can fan-out invalidations + profile-
// state transitions without a second lookup. P0-6 + Phase 1 §P0-10
// in dating/PRODUCTION_GAP_ANALYSIS.md.
func (s *Store) SetPhotoModerationStatus(ctx context.Context, photoID uuid.UUID, status string) (*Photo, error) {
	switch status {
	case "approved", "rejected", "pending":
	default:
		return nil, fmt.Errorf("invalid moderation status %q", status)
	}
	row := s.db.QueryRow(ctx, `
        UPDATE dating_photos
        SET moderation_status = $2
        WHERE id = $1
        RETURNING id, user_id, media_id, sort_order, is_primary, visibility, moderation_status, created_at
    `, photoID, status)
	out, err := scanPhoto(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPhotoNotFound
		}
		return nil, err
	}
	return out, nil
}

// ListPendingPhotos returns photos awaiting moderation, oldest-first
// so the /admin/dating/photos queue clears in arrival order. P0-8.
func (s *Store) ListPendingPhotos(ctx context.Context, limit int) ([]*Photo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, media_id, sort_order, is_primary, visibility, moderation_status, created_at
        FROM dating_photos
        WHERE moderation_status = 'pending'
        ORDER BY created_at ASC
        LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending photos: %w", err)
	}
	defer rows.Close()
	out := make([]*Photo, 0, limit)
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountApprovedPrimaryPhotos returns how many approved + public photos
// the user has. The profile-state machine uses this to decide whether
// to graduate pending_photo → pending_selfie after a moderation event.
func (s *Store) CountApprovedPrimaryPhotos(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM dating_photos
        WHERE user_id = $1
          AND is_primary = true
          AND moderation_status = 'approved'
          AND visibility = 'public'
    `, userID).Scan(&n)
	return n, err
}

// DeletePhoto removes the photo if it belongs to the user. Returns
// ErrPhotoNotFound if nothing was deleted.
func (s *Store) DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM dating_photos WHERE id = $1 AND user_id = $2`, photoID, userID)
	if err != nil {
		return fmt.Errorf("delete photo: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPhotoNotFound
	}
	return nil
}
