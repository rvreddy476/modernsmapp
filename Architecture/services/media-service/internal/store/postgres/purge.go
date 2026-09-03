package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Account control: purge (acks as "media") ────────────────────────────────

// PurgeUser erases every asset the user uploaded, in ONE transaction:
//
//  1. every blob key (asset storage_key, media_variants.object_key,
//     media_renditions.object_key, resumable_uploads.object_key) is written
//     to media_blob_reclaim so the existing blob-reclaim worker deletes the
//     objects with retries — never inline;
//  2. dependent rows for those assets are removed (explicitly, so the purge
//     does not rely on which FK clauses a given database ended up with);
//  3. the media_assets rows go;
//  4. owner slots for the user's profile go, and audio_library rows the user
//     sourced are anonymised (other posts may still use the track).
//
// Optional tables are probed with to_regclass. Idempotent.
func (s *MediaAssetStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const owned = `SELECT id FROM media_assets WHERE uploader_id = $1`

	steps := []struct{ table, sql string }{
		// 1. Blob keys into the reclaim ledger.
		{"media_assets", `INSERT INTO media_blob_reclaim (object_key, media_id)
			SELECT storage_key, id FROM media_assets WHERE uploader_id = $1 AND storage_key <> ''
			ON CONFLICT (object_key) DO NOTHING`},
		{"media_variants", `INSERT INTO media_blob_reclaim (object_key, media_id)
			SELECT object_key, media_asset_id FROM media_variants WHERE media_asset_id IN (` + owned + `) AND object_key <> ''
			ON CONFLICT (object_key) DO NOTHING`},
		{"media_renditions", `INSERT INTO media_blob_reclaim (object_key, media_id)
			SELECT object_key, media_id FROM media_renditions WHERE media_id IN (` + owned + `) AND object_key IS NOT NULL AND object_key <> ''
			ON CONFLICT (object_key) DO NOTHING`},
		{"resumable_uploads", `INSERT INTO media_blob_reclaim (object_key, media_id)
			SELECT object_key, media_id FROM resumable_uploads WHERE (media_id IN (` + owned + `) OR uploader_id = $1) AND object_key <> ''
			ON CONFLICT (object_key) DO NOTHING`},
		// 2. Dependent rows.
		{"media_variants", `DELETE FROM media_variants WHERE media_asset_id IN (` + owned + `)`},
		{"media_renditions", `DELETE FROM media_renditions WHERE media_id IN (` + owned + `)`},
		{"transcoding_jobs", `DELETE FROM transcoding_jobs WHERE media_asset_id IN (` + owned + `)`},
		{"resumable_upload_parts", `DELETE FROM resumable_upload_parts WHERE upload_id IN (
			SELECT upload_id FROM resumable_uploads WHERE media_id IN (` + owned + `) OR uploader_id = $1)`},
		{"resumable_uploads", `DELETE FROM resumable_uploads WHERE media_id IN (` + owned + `) OR uploader_id = $1`},
		{"media_subtitles", `DELETE FROM media_subtitles WHERE media_asset_id IN (` + owned + `)`},
		{"media_caption_jobs", `DELETE FROM media_caption_jobs WHERE media_id IN (` + owned + `)`},
		{"media_transcode_inbox", `DELETE FROM media_transcode_inbox WHERE media_asset_id IN (` + owned + `)`},
		{"media_chat_attachment_reservations", `DELETE FROM media_chat_attachment_reservations WHERE media_id IN (` + owned + `) OR uploader_id = $1`},
		{"media_clips", `DELETE FROM media_clips WHERE media_asset_id IN (` + owned + `)`},
		{"audio_tracks", `UPDATE audio_tracks SET source_media_id = NULL WHERE source_media_id IN (` + owned + `)`},
		{"media_event_outbox", `DELETE FROM media_event_outbox WHERE media_asset_id IN (` + owned + `)`},
		{"owner_media_slots", `DELETE FROM owner_media_slots WHERE media_asset_id IN (` + owned + `) OR (owner_type = 'profile' AND owner_id = $1)`},
		{"owner_media_resolved", `DELETE FROM owner_media_resolved WHERE media_asset_id IN (` + owned + `) OR (owner_type = 'profile' AND owner_id = $1)`},
		// 3. The assets.
		{"media_assets", `DELETE FROM media_assets WHERE uploader_id = $1`},
		// 4. Library audio: anonymise the source, keep the track.
		{"audio_library", `UPDATE audio_library SET source_user_id = NULL WHERE source_user_id = $1`},
	}
	present := map[string]bool{}
	for _, st := range steps {
		ok, known := present[st.table]
		if !known {
			ok, err = tableExists(ctx, tx, st.table)
			if err != nil {
				return fmt.Errorf("purge: probe %s: %w", st.table, err)
			}
			present[st.table] = ok
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, st.sql, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", st.table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, tx pgx.Tx, table string) (bool, error) {
	var oid *uint32
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::oid`, "public."+table).Scan(&oid); err != nil {
		return false, err
	}
	return oid != nil, nil
}
