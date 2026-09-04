package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Post purge: delete one asset on behalf of its last referrer ─────────────
//
// Post soft delete → hard purge (founder decision, 2026-09-04). When
// post-service purges a post it deletes its post_media rows and then asks
// this service, per asset, to delete the asset: DELETE /v1/media/internal/
// {id} with {"referrer":"post","referrer_id":<postId>}. post-service has
// already established that no OTHER post references the asset; this side
// re-checks every reference table IT can see, because a compromised or
// buggy caller must not be able to delete a live avatar, story or draft
// attachment by naming a post.
//
// This is deliberately NOT DeleteOrphanMediaAtomic: that path is the
// sweeper's, refuses confirmed assets by policy, and resolves its reference
// list through ResolveLiveReferences, which currently refuses to run on this
// database ("unclassified media reference(s)"). A purge names its referrer
// and the asset is confirmed by definition, so the checks here are explicit.

// AssetPurgeRecord is what the purge needs after the rows are gone.
type AssetPurgeRecord struct {
	MediaID    uuid.UUID
	UploaderID uuid.UUID
	// Prefix is the asset's object-key prefix ("user/<uploader>/<media>/");
	// every object under it belongs to this asset alone.
	Prefix string
	// ObjectKeys are the keys the row tables knew about, recorded in
	// media_blob_reclaim before the rows were removed.
	ObjectKeys []string
}

// AssetPrefix is the object-key prefix every object of an asset lives under
// (media.go builds storage keys as user/<uploader>/<media>/original, and
// frames, waveform, variants and HLS output all nest beneath the same
// directory).
func AssetPrefix(uploaderID, mediaID uuid.UUID) string {
	return fmt.Sprintf("user/%s/%s/", uploaderID, mediaID)
}

// DeleteAssetForReferrer removes an asset's rows in ONE transaction, after
// verifying — inside that transaction, with the asset row locked — that no
// live reference remains other than the named referrer's (which the caller
// has already removed). Returns ErrMediaNotFound when the asset is already
// gone and ErrMediaStillReferenced when anything still holds it.
//
// Reference tables are probed with to_regclass so a database that lacks one
// (a per-service schema in production) simply skips it; the RESTRICT
// foreign keys on post_media / post_draft_media / stories are the backstop
// and surface as ErrMediaStillReferenced too.
func (s *MediaAssetStore) DeleteAssetForReferrer(ctx context.Context, mediaID uuid.UUID, referrerKind string, referrerID uuid.UUID) (*AssetPurgeRecord, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var uploaderID uuid.UUID
	var storageKey string
	err = tx.QueryRow(ctx, `
		SELECT uploader_id, storage_key FROM media_assets WHERE id = $1 FOR UPDATE`, mediaID).
		Scan(&uploaderID, &storageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMediaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock media row: %w", err)
	}

	// Every claim this database can show us. $2 is the referrer (a post id)
	// whose own rows the caller has already removed; a surviving row for it
	// is still a live reference and refuses the delete.
	//
	// pgx refuses a call whose argument count differs from the query's
	// placeholders ("expected 1 arguments, got 2"), and that error takes the
	// fail-closed branch below — which is exactly what happened on the first
	// live purge (2026-09-04, 409 for every asset). Each check therefore
	// declares the arguments it binds.
	checks := []struct {
		table string
		sql   string
		args  []any
	}{
		{"post_media", `SELECT EXISTS (SELECT 1 FROM post_media WHERE media_id = $1)`, []any{mediaID}},
		{"posts", `SELECT EXISTS (SELECT 1 FROM posts WHERE cover_media_id = $1 AND id <> $2)`, []any{mediaID, referrerID}},
		{"post_draft_media", `SELECT EXISTS (
			SELECT 1 FROM post_draft_media dm JOIN post_drafts d ON d.id = dm.draft_id
			WHERE dm.media_id = $1 AND d.status <> 'deleted')`, []any{mediaID}},
		{"stories", `SELECT EXISTS (SELECT 1 FROM stories WHERE media_id = $1)`, []any{mediaID}},
		{"owner_media_slots", `SELECT EXISTS (SELECT 1 FROM owner_media_slots WHERE media_asset_id = $1)`, []any{mediaID}},
		{"video_series", `SELECT EXISTS (SELECT 1 FROM video_series WHERE cover_media_id = $1)`, []any{mediaID}},
		{"media_chat_attachment_reservations", `SELECT EXISTS (SELECT 1 FROM media_chat_attachment_reservations WHERE media_id = $1)`, []any{mediaID}},
	}
	for _, c := range checks {
		ok, err := tableExists(ctx, tx, c.table)
		if err != nil {
			return nil, fmt.Errorf("%w: probe %s: %v", ErrMediaStillReferenced, c.table, err)
		}
		if !ok {
			continue
		}
		var referenced bool
		if err := tx.QueryRow(ctx, c.sql, c.args...).Scan(&referenced); err != nil {
			// Fail closed: an unanswerable check is a refusal, never a guess.
			return nil, fmt.Errorf("%w: %s check unavailable: %v", ErrMediaStillReferenced, c.table, err)
		}
		if referenced {
			return nil, fmt.Errorf("%w: %s", ErrMediaStillReferenced, c.table)
		}
	}

	// Record every key the rows know about BEFORE they go, so a failed
	// object delete is retried by the blob reclaim sweeper.
	keys := []string{storageKey}
	for _, q := range []string{
		`SELECT object_key FROM media_variants WHERE media_asset_id = $1`,
		`SELECT object_key FROM media_renditions WHERE media_id = $1 AND object_key IS NOT NULL`,
	} {
		rows, err := tx.Query(ctx, q, mediaID)
		if err != nil {
			// media_renditions is optional; a missing table is not a failure.
			continue
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err == nil && k != "" {
				keys = append(keys, k)
			}
		}
		rows.Close()
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_blob_reclaim (object_key, media_id)
			VALUES ($1, $2) ON CONFLICT (object_key) DO NOTHING`, k, mediaID); err != nil {
			return nil, fmt.Errorf("record blob reclaim: %w", err)
		}
	}

	// Dependent rows, explicitly (same list as PurgeUser, scoped to one
	// asset) so the purge does not depend on which FK clauses this
	// database ended up with. Optional tables are probed.
	steps := []struct{ table, sql string }{
		{"media_variants", `DELETE FROM media_variants WHERE media_asset_id = $1`},
		{"media_renditions", `DELETE FROM media_renditions WHERE media_id = $1`},
		{"transcoding_jobs", `DELETE FROM transcoding_jobs WHERE media_asset_id = $1`},
		{"resumable_upload_parts", `DELETE FROM resumable_upload_parts WHERE upload_id IN (
			SELECT upload_id FROM resumable_uploads WHERE media_id = $1)`},
		{"resumable_uploads", `DELETE FROM resumable_uploads WHERE media_id = $1`},
		{"media_subtitles", `DELETE FROM media_subtitles WHERE media_asset_id = $1`},
		{"media_caption_jobs", `DELETE FROM media_caption_jobs WHERE media_id = $1`},
		{"media_transcode_inbox", `DELETE FROM media_transcode_inbox WHERE media_asset_id = $1`},
		{"media_clips", `DELETE FROM media_clips WHERE media_asset_id = $1`},
		{"audio_tracks", `UPDATE audio_tracks SET source_media_id = NULL WHERE source_media_id = $1`},
		{"media_event_outbox", `DELETE FROM media_event_outbox WHERE media_asset_id = $1`},
		{"owner_media_resolved", `DELETE FROM owner_media_resolved WHERE media_asset_id = $1`},
	}
	present := map[string]bool{}
	for _, st := range steps {
		ok, known := present[st.table]
		if !known {
			ok, err = tableExists(ctx, tx, st.table)
			if err != nil {
				return nil, fmt.Errorf("purge asset: probe %s: %w", st.table, err)
			}
			present[st.table] = ok
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, st.sql, mediaID); err != nil {
			return nil, fmt.Errorf("purge asset: %s: %w", st.table, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, mediaID); err != nil {
		if isForeignKeyViolation(err) {
			// A RESTRICT foreign key found a claim the checks above did not
			// know about — the backstop did its job.
			return nil, fmt.Errorf("%w: foreign key", ErrMediaStillReferenced)
		}
		return nil, fmt.Errorf("purge asset: delete media_assets: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("purge asset: commit: %w", err)
	}
	return &AssetPurgeRecord{
		MediaID: mediaID, UploaderID: uploaderID,
		Prefix: AssetPrefix(uploaderID, mediaID), ObjectKeys: keys,
	}, nil
}
