package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CrosspostLink represents an active cross-post between modules.
type CrosspostLink struct {
	ID            uuid.UUID  `json:"id"`
	SourceModule  string     `json:"source_module"`
	SourcePostID  uuid.UUID  `json:"source_post_id"`
	TargetModule  string     `json:"target_module"`
	TargetPostID  uuid.UUID  `json:"target_post_id"`
	CreatedAt     time.Time  `json:"created_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
}

const crosspostLinkCols = `id, source_module, source_post_id, target_module, target_post_id, created_at, deleted_at`

func scanCrosspostLink(row pgx.Row) (*CrosspostLink, error) {
	var cl CrosspostLink
	err := row.Scan(&cl.ID, &cl.SourceModule, &cl.SourcePostID, &cl.TargetModule, &cl.TargetPostID, &cl.CreatedAt, &cl.DeletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &cl, nil
}

// CreateCrosspostLink creates a cross-post link and the target embed post in a single transaction.
// It creates a new post with content_type='video_embed' or 'flick_embed' and stores the embed_ref.
func (s *Store) CreateCrosspostLink(ctx context.Context, sourcePost *Post, targetModule string) (*CrosspostLink, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Determine embed content type
	embedContentType := "video_embed"
	if sourcePost.ContentType == "flick" || sourcePost.ContentType == "reel" {
		embedContentType = "flick_embed"
	}

	// Build embed_ref JSONB
	embedRef := map[string]interface{}{
		"source_post_id": sourcePost.ID.String(),
		"source_module":  sourceModuleFromContentType(sourcePost.ContentType),
		"content_type":   sourcePost.ContentType,
		"title":          sourcePost.Title,
	}
	embedRefJSON, err := json.Marshal(embedRef)
	if err != nil {
		return nil, err
	}

	// Create the target embed post
	targetPostID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, embed_ref, post_type, app_origin, review_status, created_at, updated_at)
		VALUES ($1, $2, $3, 'public', $4, $5, 'standard', 'posttube', 'approved', NOW(), NOW())
	`, targetPostID, sourcePost.AuthorID, sourcePost.Text, embedContentType, embedRefJSON)
	if err != nil {
		return nil, err
	}

	// Create the crosspost link
	linkID := uuid.New()
	sourceModule := sourceModuleFromContentType(sourcePost.ContentType)

	var cl CrosspostLink
	err = tx.QueryRow(ctx, `
		INSERT INTO crosspost_links (id, source_module, source_post_id, target_module, target_post_id, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING `+crosspostLinkCols,
		linkID, sourceModule, sourcePost.ID, targetModule, targetPostID,
	).Scan(&cl.ID, &cl.SourceModule, &cl.SourcePostID, &cl.TargetModule, &cl.TargetPostID, &cl.CreatedAt, &cl.DeletedAt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &cl, nil
}

// GetCrosspostLink returns a single active crosspost link by source post + target module.
func (s *Store) GetCrosspostLink(ctx context.Context, sourcePostID uuid.UUID, targetModule string) (*CrosspostLink, error) {
	return scanCrosspostLink(s.db.QueryRow(ctx, `
		SELECT `+crosspostLinkCols+` FROM crosspost_links
		WHERE source_post_id = $1 AND target_module = $2 AND deleted_at IS NULL
	`, sourcePostID, targetModule))
}

// GetCrosspostLinkByID returns a crosspost link by its primary key.
func (s *Store) GetCrosspostLinkByID(ctx context.Context, id uuid.UUID) (*CrosspostLink, error) {
	return scanCrosspostLink(s.db.QueryRow(ctx, `
		SELECT `+crosspostLinkCols+` FROM crosspost_links WHERE id = $1
	`, id))
}

// ListCrosspostLinks returns all active crosspost links for a source post.
func (s *Store) ListCrosspostLinks(ctx context.Context, sourcePostID uuid.UUID) ([]CrosspostLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+crosspostLinkCols+` FROM crosspost_links
		WHERE source_post_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, sourcePostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []CrosspostLink
	for rows.Next() {
		var cl CrosspostLink
		if err := rows.Scan(&cl.ID, &cl.SourceModule, &cl.SourcePostID, &cl.TargetModule, &cl.TargetPostID, &cl.CreatedAt, &cl.DeletedAt); err != nil {
			return nil, err
		}
		links = append(links, cl)
	}
	return links, rows.Err()
}

// ListCrosspostsByUser returns all active crosspost links for posts authored by the given user.
func (s *Store) ListCrosspostsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]CrosspostLink, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM crosspost_links cl
		JOIN posts p ON p.id = cl.source_post_id
		WHERE p.author_id = $1 AND cl.deleted_at IS NULL AND p.deleted_at IS NULL
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT cl.id, cl.source_module, cl.source_post_id, cl.target_module, cl.target_post_id, cl.created_at, cl.deleted_at
		FROM crosspost_links cl
		JOIN posts p ON p.id = cl.source_post_id
		WHERE p.author_id = $1 AND cl.deleted_at IS NULL AND p.deleted_at IS NULL
		ORDER BY cl.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var links []CrosspostLink
	for rows.Next() {
		var cl CrosspostLink
		if err := rows.Scan(&cl.ID, &cl.SourceModule, &cl.SourcePostID, &cl.TargetModule, &cl.TargetPostID, &cl.CreatedAt, &cl.DeletedAt); err != nil {
			return nil, 0, err
		}
		links = append(links, cl)
	}
	return links, total, rows.Err()
}

// SoftDeleteCrosspostLink soft-deletes a crosspost link and its target embed post.
func (s *Store) SoftDeleteCrosspostLink(ctx context.Context, id uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Get the target post ID first
	var targetPostID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE crosspost_links SET deleted_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING target_post_id
	`, id).Scan(&targetPostID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("crosspost link not found or already deleted")
		}
		return err
	}

	// Soft-delete the target embed post
	_, err = tx.Exec(ctx, `
		UPDATE posts SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, targetPostID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func sourceModuleFromContentType(contentType string) string {
	switch contentType {
	case "video", "long_video":
		return "posttube"
	case "flick", "reel":
		return "postgram"
	default:
		return "posttube"
	}
}
