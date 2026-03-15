package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OwnerMediaSlot represents a slot assignment linking an owner entity to a media asset.
type OwnerMediaSlot struct {
	ID           uuid.UUID  `json:"id"`
	OwnerType    string     `json:"owner_type"`
	OwnerID      uuid.UUID  `json:"owner_id"`
	SlotName     string     `json:"slot_name"`
	MediaAssetID uuid.UUID  `json:"media_asset_id"`
	Status       string     `json:"status"` // pending, active, replaced, removed
	CropX        *float64   `json:"crop_x,omitempty"`
	CropY        *float64   `json:"crop_y,omitempty"`
	CropW        *float64   `json:"crop_w,omitempty"`
	CropH        *float64   `json:"crop_h,omitempty"`
	FocalX       float64    `json:"focal_x"`
	FocalY       float64    `json:"focal_y"`
	AssignedAt   time.Time  `json:"assigned_at"`
	ReplacedAt   *time.Time `json:"replaced_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ResolvedSlot is the denormalized fast-read representation of an active slot.
type ResolvedSlot struct {
	OwnerType    string            `json:"owner_type"`
	OwnerID      uuid.UUID         `json:"owner_id"`
	SlotName     string            `json:"slot_name"`
	MediaAssetID uuid.UUID         `json:"media_asset_id"`
	Blurhash     *string           `json:"blurhash,omitempty"`
	Width        *int              `json:"width,omitempty"`
	Height       *int              `json:"height,omitempty"`
	Variants     map[string]string `json:"variants"`
	FocalX       float64           `json:"focal_x"`
	FocalY       float64           `json:"focal_y"`
	ResolvedAt   time.Time         `json:"resolved_at"`
}

// OwnerRef identifies an owner entity for batch queries.
type OwnerRef struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
}

// AssignSlot replaces any existing active slot for the same owner+slot_name and
// inserts a new slot. If the media asset is already 'ready', the slot is set to
// 'active'; otherwise it is 'pending'. The resolved table is refreshed within
// the same transaction.
func (s *MediaAssetStore) AssignSlot(ctx context.Context, slot OwnerMediaSlot) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Mark existing active slot as replaced
	_, err = tx.Exec(ctx, `
		UPDATE owner_media_slots
		SET status = 'replaced', replaced_at = NOW(), updated_at = NOW()
		WHERE owner_type = $1 AND owner_id = $2 AND slot_name = $3 AND status = 'active'
	`, slot.OwnerType, slot.OwnerID, slot.SlotName)
	if err != nil {
		return fmt.Errorf("replace existing slot: %w", err)
	}

	// Determine status based on media asset processing_status
	var processingStatus string
	err = tx.QueryRow(ctx,
		`SELECT processing_status FROM media_assets WHERE id = $1`,
		slot.MediaAssetID,
	).Scan(&processingStatus)
	if err != nil {
		return fmt.Errorf("check media asset status: %w", err)
	}

	slotStatus := "pending"
	if processingStatus == "ready" {
		slotStatus = "active"
	}

	// Insert the new slot
	if slot.ID == uuid.Nil {
		slot.ID = uuid.New()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO owner_media_slots
			(id, owner_type, owner_id, slot_name, media_asset_id, status,
			 crop_x, crop_y, crop_w, crop_h, focal_x, focal_y,
			 assigned_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW(), NOW())
	`, slot.ID, slot.OwnerType, slot.OwnerID, slot.SlotName, slot.MediaAssetID, slotStatus,
		slot.CropX, slot.CropY, slot.CropW, slot.CropH, slot.FocalX, slot.FocalY)
	if err != nil {
		return fmt.Errorf("insert slot: %w", err)
	}

	// Refresh the resolved table
	if err := s.refreshResolved(ctx, tx, slot.OwnerType, slot.OwnerID, slot.SlotName); err != nil {
		return fmt.Errorf("refresh resolved: %w", err)
	}

	return tx.Commit(ctx)
}

// RemoveSlot marks the active slot for a given owner+slot_name as 'removed'
// and deletes the corresponding resolved entry.
func (s *MediaAssetStore) RemoveSlot(ctx context.Context, ownerType string, ownerID uuid.UUID, slotName string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE owner_media_slots
		SET status = 'removed', updated_at = NOW()
		WHERE owner_type = $1 AND owner_id = $2 AND slot_name = $3 AND status = 'active'
	`, ownerType, ownerID, slotName)
	if err != nil {
		return fmt.Errorf("remove slot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	// Remove from resolved table
	_, err = tx.Exec(ctx, `
		DELETE FROM owner_media_resolved
		WHERE owner_type = $1 AND owner_id = $2 AND slot_name = $3
	`, ownerType, ownerID, slotName)
	if err != nil {
		return fmt.Errorf("delete resolved: %w", err)
	}

	return tx.Commit(ctx)
}

// GetActiveSlots returns all resolved slots for an owner entity.
func (s *MediaAssetStore) GetActiveSlots(ctx context.Context, ownerType string, ownerID uuid.UUID) ([]ResolvedSlot, error) {
	rows, err := s.db.Query(ctx, `
		SELECT owner_type, owner_id, slot_name, media_asset_id, blurhash,
		       width, height, variants, focal_x, focal_y, resolved_at
		FROM owner_media_resolved
		WHERE owner_type = $1 AND owner_id = $2
		ORDER BY slot_name
	`, ownerType, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResolvedSlots(rows)
}

// GetActiveSlot returns a single resolved slot for an owner entity.
func (s *MediaAssetStore) GetActiveSlot(ctx context.Context, ownerType string, ownerID uuid.UUID, slotName string) (*ResolvedSlot, error) {
	var r ResolvedSlot
	var variantsJSON []byte
	err := s.db.QueryRow(ctx, `
		SELECT owner_type, owner_id, slot_name, media_asset_id, blurhash,
		       width, height, variants, focal_x, focal_y, resolved_at
		FROM owner_media_resolved
		WHERE owner_type = $1 AND owner_id = $2 AND slot_name = $3
	`, ownerType, ownerID, slotName).Scan(
		&r.OwnerType, &r.OwnerID, &r.SlotName, &r.MediaAssetID, &r.Blurhash,
		&r.Width, &r.Height, &variantsJSON, &r.FocalX, &r.FocalY, &r.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Variants = make(map[string]string)
	if len(variantsJSON) > 0 {
		_ = json.Unmarshal(variantsJSON, &r.Variants)
	}
	return &r, nil
}

// GetResolvedBatch fetches resolved slots for multiple owners in a single query.
// Returns a map keyed by "ownerType:ownerID".
func (s *MediaAssetStore) GetResolvedBatch(ctx context.Context, owners []OwnerRef) (map[string][]ResolvedSlot, error) {
	if len(owners) == 0 {
		return nil, nil
	}

	// Build a VALUES clause for (owner_type, owner_id) pairs
	args := make([]interface{}, 0, len(owners)*2)
	valueParts := make([]string, 0, len(owners))
	for i, o := range owners {
		valueParts = append(valueParts, fmt.Sprintf("($%d, $%d::uuid)", i*2+1, i*2+2))
		args = append(args, o.Type, o.ID)
	}

	query := fmt.Sprintf(`
		SELECT r.owner_type, r.owner_id, r.slot_name, r.media_asset_id, r.blurhash,
		       r.width, r.height, r.variants, r.focal_x, r.focal_y, r.resolved_at
		FROM owner_media_resolved r
		INNER JOIN (VALUES %s) AS req(owner_type, owner_id)
			ON r.owner_type = req.owner_type AND r.owner_id = req.owner_id
		ORDER BY r.owner_type, r.owner_id, r.slot_name
	`, strings.Join(valueParts, ", "))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	slots, err := scanResolvedSlots(rows)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]ResolvedSlot, len(owners))
	for _, slot := range slots {
		key := slot.OwnerType + ":" + slot.OwnerID.String()
		result[key] = append(result[key], slot)
	}
	return result, nil
}

// ActivatePendingSlot finds all pending slots referencing the given media asset,
// sets them to 'active', and refreshes the resolved table for each.
func (s *MediaAssetStore) ActivatePendingSlot(ctx context.Context, mediaAssetID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Find all pending slots for this media asset
	rows, err := tx.Query(ctx, `
		SELECT owner_type, owner_id, slot_name
		FROM owner_media_slots
		WHERE media_asset_id = $1 AND status = 'pending'
	`, mediaAssetID)
	if err != nil {
		return fmt.Errorf("find pending slots: %w", err)
	}

	type slotRef struct {
		ownerType string
		ownerID   uuid.UUID
		slotName  string
	}
	var pending []slotRef
	for rows.Next() {
		var ref slotRef
		if err := rows.Scan(&ref.ownerType, &ref.ownerID, &ref.slotName); err != nil {
			rows.Close()
			return fmt.Errorf("scan pending slot: %w", err)
		}
		pending = append(pending, ref)
	}
	rows.Close()

	if len(pending) == 0 {
		return nil
	}

	// Activate them
	_, err = tx.Exec(ctx, `
		UPDATE owner_media_slots
		SET status = 'active', updated_at = NOW()
		WHERE media_asset_id = $1 AND status = 'pending'
	`, mediaAssetID)
	if err != nil {
		return fmt.Errorf("activate pending slots: %w", err)
	}

	// Refresh resolved for each activated slot
	for _, ref := range pending {
		if err := s.refreshResolved(ctx, tx, ref.ownerType, ref.ownerID, ref.slotName); err != nil {
			return fmt.Errorf("refresh resolved for %s/%s/%s: %w", ref.ownerType, ref.ownerID, ref.slotName, err)
		}
	}

	return tx.Commit(ctx)
}

// refreshResolved upserts the owner_media_resolved row for a given owner+slot_name
// by joining owner_media_slots with media_assets and media_variants.
func (s *MediaAssetStore) refreshResolved(ctx context.Context, tx pgx.Tx, ownerType string, ownerID uuid.UUID, slotName string) error {
	// Check if there is an active slot
	var slotMediaAssetID uuid.UUID
	var focalX, focalY float64
	err := tx.QueryRow(ctx, `
		SELECT media_asset_id, focal_x, focal_y
		FROM owner_media_slots
		WHERE owner_type = $1 AND owner_id = $2 AND slot_name = $3 AND status = 'active'
	`, ownerType, ownerID, slotName).Scan(&slotMediaAssetID, &focalX, &focalY)
	if err == pgx.ErrNoRows {
		// No active slot — remove resolved entry if any
		_, _ = tx.Exec(ctx, `
			DELETE FROM owner_media_resolved
			WHERE owner_type = $1 AND owner_id = $2 AND slot_name = $3
		`, ownerType, ownerID, slotName)
		return nil
	}
	if err != nil {
		return fmt.Errorf("query active slot: %w", err)
	}

	// Fetch media asset metadata
	var blurhash *string
	var width, height *int
	err = tx.QueryRow(ctx, `
		SELECT blurhash, width, height
		FROM media_assets
		WHERE id = $1
	`, slotMediaAssetID).Scan(&blurhash, &width, &height)
	if err != nil {
		return fmt.Errorf("query media asset: %w", err)
	}

	// Collect variant object keys
	variantRows, err := tx.Query(ctx, `
		SELECT variant, object_key
		FROM media_variants
		WHERE media_asset_id = $1
	`, slotMediaAssetID)
	if err != nil {
		return fmt.Errorf("query variants: %w", err)
	}
	defer variantRows.Close()

	variants := make(map[string]string)
	for variantRows.Next() {
		var name, objectKey string
		if err := variantRows.Scan(&name, &objectKey); err != nil {
			return fmt.Errorf("scan variant: %w", err)
		}
		variants[name] = objectKey
	}
	variantRows.Close()

	variantsJSON, err := json.Marshal(variants)
	if err != nil {
		return fmt.Errorf("marshal variants: %w", err)
	}

	// Upsert resolved row
	_, err = tx.Exec(ctx, `
		INSERT INTO owner_media_resolved
			(owner_type, owner_id, slot_name, media_asset_id, blurhash, width, height, variants, focal_x, focal_y, resolved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (owner_type, owner_id, slot_name) DO UPDATE SET
			media_asset_id = EXCLUDED.media_asset_id,
			blurhash = EXCLUDED.blurhash,
			width = EXCLUDED.width,
			height = EXCLUDED.height,
			variants = EXCLUDED.variants,
			focal_x = EXCLUDED.focal_x,
			focal_y = EXCLUDED.focal_y,
			resolved_at = EXCLUDED.resolved_at
	`, ownerType, ownerID, slotName, slotMediaAssetID, blurhash, width, height, variantsJSON, focalX, focalY)
	if err != nil {
		return fmt.Errorf("upsert resolved: %w", err)
	}

	return nil
}

// scanResolvedSlots scans rows from owner_media_resolved into ResolvedSlot slices.
func scanResolvedSlots(rows pgx.Rows) ([]ResolvedSlot, error) {
	var slots []ResolvedSlot
	for rows.Next() {
		var r ResolvedSlot
		var variantsJSON []byte
		if err := rows.Scan(
			&r.OwnerType, &r.OwnerID, &r.SlotName, &r.MediaAssetID, &r.Blurhash,
			&r.Width, &r.Height, &variantsJSON, &r.FocalX, &r.FocalY, &r.ResolvedAt,
		); err != nil {
			return nil, err
		}
		r.Variants = make(map[string]string)
		if len(variantsJSON) > 0 {
			_ = json.Unmarshal(variantsJSON, &r.Variants)
		}
		slots = append(slots, r)
	}
	return slots, nil
}
