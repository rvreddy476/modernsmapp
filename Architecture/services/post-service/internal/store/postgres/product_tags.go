package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostProductTag is one tagged product on one video. Matches the
// migrations/020_post_product_tags.sql column set.
type PostProductTag struct {
	ID              uuid.UUID `json:"id"`
	PostID          uuid.UUID `json:"post_id"`
	AffiliateLinkID uuid.UUID `json:"affiliate_link_id"`
	CreatorID       uuid.UUID `json:"creator_id"`

	// Time window inside the video. nil = "show across the whole video".
	TimeStartMS *int32 `json:"time_start_ms,omitempty"`
	TimeEndMS   *int32 `json:"time_end_ms,omitempty"`

	// Overlay anchor as 0..100 percentages. nil = player decides.
	PositionX *float32 `json:"position_x,omitempty"`
	PositionY *float32 `json:"position_y,omitempty"`

	Label    string `json:"label"`
	ImageURL string `json:"image_url"`

	ImpressionCount int64 `json:"impression_count"`
	ClickCount      int64 `json:"click_count"`

	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateProductTag inserts a new tag. Returns ErrTagAlreadyExists when
// the (post, affiliate_link) pair already has an active tag — the caller
// usually wants to surface that as a 409.
func (s *Store) CreateProductTag(ctx context.Context, t *PostProductTag) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	const q = `
INSERT INTO post_product_tags (
    id, post_id, affiliate_link_id, creator_id,
    time_start_ms, time_end_ms, position_x, position_y,
    label, image_url
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING created_at, updated_at`
	err := s.db.QueryRow(ctx, q,
		t.ID, t.PostID, t.AffiliateLinkID, t.CreatorID,
		t.TimeStartMS, t.TimeEndMS, t.PositionX, t.PositionY,
		t.Label, t.ImageURL,
	).Scan(&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrTagAlreadyExists
		}
		return fmt.Errorf("insert post_product_tag: %w", err)
	}
	t.IsActive = true
	return nil
}

// ListProductTagsByPost returns the active tags on a post in
// created_at ASC (earliest tag first — usually the order the player
// surfaces them in if it has to pick one to render at a moment with
// overlapping time windows).
func (s *Store) ListProductTagsByPost(ctx context.Context, postID uuid.UUID) ([]*PostProductTag, error) {
	const q = `
SELECT id, post_id, affiliate_link_id, creator_id,
       time_start_ms, time_end_ms, position_x, position_y,
       label, image_url,
       impression_count, click_count,
       is_active, created_at, updated_at
FROM post_product_tags
WHERE post_id = $1 AND is_active = TRUE
ORDER BY created_at ASC`
	rows, err := s.db.Query(ctx, q, postID)
	if err != nil {
		return nil, fmt.Errorf("list post_product_tags: %w", err)
	}
	defer rows.Close()
	return scanProductTags(rows)
}

// ListProductTagsByCreator returns every active tag the creator has placed,
// newest first. Backs the creator-analytics dashboard.
func (s *Store) ListProductTagsByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]*PostProductTag, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	const q = `
SELECT id, post_id, affiliate_link_id, creator_id,
       time_start_ms, time_end_ms, position_x, position_y,
       label, image_url,
       impression_count, click_count,
       is_active, created_at, updated_at
FROM post_product_tags
WHERE creator_id = $1 AND is_active = TRUE
ORDER BY created_at DESC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, creatorID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list post_product_tags by creator: %w", err)
	}
	defer rows.Close()
	return scanProductTags(rows)
}

// GetProductTagByID — used by the delete + counter-bump paths so we
// can authorise (delete) or short-circuit (counter on a deleted tag).
func (s *Store) GetProductTagByID(ctx context.Context, tagID uuid.UUID) (*PostProductTag, error) {
	const q = `
SELECT id, post_id, affiliate_link_id, creator_id,
       time_start_ms, time_end_ms, position_x, position_y,
       label, image_url,
       impression_count, click_count,
       is_active, created_at, updated_at
FROM post_product_tags
WHERE id = $1`
	row := s.db.QueryRow(ctx, q, tagID)
	t, err := scanProductTag(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTagNotFound
		}
		return nil, err
	}
	return t, nil
}

// SoftDeleteProductTag flips is_active=false. Real delete (DELETE FROM)
// would break audit + creator-analytics historical reads.
func (s *Store) SoftDeleteProductTag(ctx context.Context, tagID uuid.UUID) error {
	const q = `UPDATE post_product_tags SET is_active = FALSE, updated_at = NOW() WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, tagID)
	if err != nil {
		return fmt.Errorf("soft-delete post_product_tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTagNotFound
	}
	return nil
}

// BumpImpression / BumpClick are intentionally unconditional + lock-free.
// At scale these route through the same Redis flush worker the engagement
// counters use; the per-row UPDATE here is the fallback for early-traffic
// services. The flush worker calls these in batched aggregate, not
// per-event.
func (s *Store) BumpProductTagImpression(ctx context.Context, tagID uuid.UUID, delta int64) error {
	_, err := s.db.Exec(ctx,
		`UPDATE post_product_tags SET impression_count = impression_count + $2, updated_at = NOW() WHERE id = $1`,
		tagID, delta,
	)
	return err
}

func (s *Store) BumpProductTagClick(ctx context.Context, tagID uuid.UUID, delta int64) error {
	_, err := s.db.Exec(ctx,
		`UPDATE post_product_tags SET click_count = click_count + $2, updated_at = NOW() WHERE id = $1`,
		tagID, delta,
	)
	return err
}

// ─── helpers ────────────────────────────────────────────────────────

// ErrTagNotFound + ErrTagAlreadyExists are surfaced by the handler as
// 404 / 409 respectively. errors.Is keeps the wrap chain trivial for
// the service layer.
var (
	ErrTagNotFound      = errors.New("post product tag not found")
	ErrTagAlreadyExists = errors.New("post product tag already exists for this post + affiliate link")
)

func scanProductTags(rows pgx.Rows) ([]*PostProductTag, error) {
	out := []*PostProductTag{}
	for rows.Next() {
		t, err := scanProductTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanProductTag(row pgx.Row) (*PostProductTag, error) {
	t := &PostProductTag{}
	err := row.Scan(
		&t.ID, &t.PostID, &t.AffiliateLinkID, &t.CreatorID,
		&t.TimeStartMS, &t.TimeEndMS, &t.PositionX, &t.PositionY,
		&t.Label, &t.ImageURL,
		&t.ImpressionCount, &t.ClickCount,
		&t.IsActive, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return t, nil
}
