package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dating plan lane D6 (migration 017).

// AccessScopeDatingPhoto marks an asset dating-service has prepared. The
// public read routes refuse it to anyone but its uploader.
const AccessScopeDatingPhoto = "dating_photo"

// UpdateMediaModerationScan records every label the image scanner returned
// (a JSON array, possibly empty) and the scanner's name.
func (s *MediaAssetStore) UpdateMediaModerationScan(ctx context.Context, id uuid.UUID, labels []byte, scanner string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_assets
		SET moderation_labels = $2::jsonb, moderation_scanner = $3, updated_at = NOW()
		WHERE id = $1`, id, string(labels), scanner)
	return err
}

// MarkDatingPhotoPrepared records that the original was re-encoded without
// metadata (new type, size and dimensions) and scopes the asset to dating.
func (s *MediaAssetStore) MarkDatingPhotoPrepared(ctx context.Context, id uuid.UUID, mime string, sizeBytes int64, width, height int) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_assets
		SET access_scope = 'dating_photo', metadata_stripped_at = NOW(),
		    mime_type = $2, file_size_bytes = $3, width = $4, height = $5, updated_at = NOW()
		WHERE id = $1`, id, mime, sizeBytes, width, height)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
