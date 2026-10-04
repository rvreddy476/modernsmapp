package postgres

import (
	"context"
	"github.com/google/uuid"
)

const AccessScopeDoorstepPhoto = "doorstep_photo"

// Ownership, release and incompatible-scope guards are checked in the same
// statement. Idempotent; false is indistinguishable from an unknown asset.
func (s *MediaAssetStore) PrepareDoorstepPhoto(ctx context.Context, id, owner uuid.UUID) (bool, error) {
	// Doorstep, not the abandoned-composer sweeper, owns evidence retention.
	tag, err := s.db.Exec(ctx, `UPDATE media_assets SET access_scope='doorstep_photo',upload_purpose=NULL,updated_at=NOW()
        WHERE id=$1 AND uploader_id=$2 AND file_type='image' AND processing_status='ready'
        AND moderation_status='passed' AND (access_scope IS NULL OR access_scope='doorstep_photo')
        AND storage_key NOT LIKE 'public/%'
        AND NOT EXISTS (SELECT 1 FROM media_variants WHERE media_asset_id=$1 AND object_key LIKE 'public/%')`, id, owner)
	return tag.RowsAffected() == 1, err
}
