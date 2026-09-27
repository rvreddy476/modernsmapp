package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Alternate audio tracks for short videos (migration 019).
//
// Status only, plus the key of the source audio; the playable outputs are
// media_variants rows named dub_<lang>_<rung> under the video asset, so the
// delivery routes need no new code path. Lifecycle mirrors media_caption_jobs:
// claim with FOR UPDATE SKIP LOCKED, reclaim stale claims, fence every
// terminal write with the claim token.

// MediaAudioTrack is one language track of a video asset.
type MediaAudioTrack struct {
	ID             uuid.UUID
	MediaAssetID   uuid.UUID
	Language       string
	Label          string
	Source         string // uploaded | generated
	SourceLanguage *string
	SourceKey      *string
	Status         string // pending | processing | ready | failed
	Attempts       int
	LastError      *string
	ClaimedAt      *time.Time
	ClaimToken     *string
	Rungs          []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ErrAudioTrackClaimLost is returned when a fenced write finds the claim
// token no longer matches — another worker reclaimed the track.
var ErrAudioTrackClaimLost = errors.New("audio track claim lost")

// ErrAudioTrackNotFound is returned for a lookup or delete that matched no row.
var ErrAudioTrackNotFound = errors.New("audio track not found")

const audioTrackColumns = `id, media_asset_id, language, label, source, source_language, source_key,
	status, attempts, last_error, claimed_at, claim_token, rungs, created_at, updated_at`

func scanAudioTrack(row pgx.Row) (*MediaAudioTrack, error) {
	var t MediaAudioTrack
	err := row.Scan(&t.ID, &t.MediaAssetID, &t.Language, &t.Label, &t.Source, &t.SourceLanguage,
		&t.SourceKey, &t.Status, &t.Attempts, &t.LastError, &t.ClaimedAt, &t.ClaimToken, &t.Rungs,
		&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if t.Rungs == nil {
		t.Rungs = []string{}
	}
	return &t, nil
}

// DubVariantPrefix is the media_variants name prefix of one language's
// muxed outputs: dub_<lang>_ (the rung follows).
func DubVariantPrefix(language string) string {
	return "dub_" + language + "_"
}

// ReplaceAudioTrack inserts a new pending track for (media, language),
// removing any previous track for that language first: its row, and the
// media_variants rows of its muxed outputs. The object keys of everything
// removed are returned so the caller can delete the blobs after commit.
//
// A second upload for the same language therefore REPLACES the first (the
// contract's stated alternative, 409 AUDIO_TRACK_EXISTS, is not used).
func (s *MediaAssetStore) ReplaceMediaAudioTrack(ctx context.Context, t *MediaAudioTrack) (staleKeys []string, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		DELETE FROM media_audio_tracks
		 WHERE media_asset_id = $1 AND language = $2
		RETURNING source_key`, t.MediaAssetID, t.Language)
	if err != nil {
		return nil, fmt.Errorf("delete previous track: %w", err)
	}
	for rows.Next() {
		var key *string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		if key != nil && *key != "" {
			staleKeys = append(staleKeys, *key)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	variantKeys, err := deleteDubVariantsTx(ctx, tx, t.MediaAssetID, t.Language)
	if err != nil {
		return nil, err
	}
	staleKeys = append(staleKeys, variantKeys...)

	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.Status == "" {
		t.Status = "pending"
	}
	if t.Rungs == nil {
		t.Rungs = []string{}
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO media_audio_tracks
		    (id, media_asset_id, language, label, source, source_language, source_key, status, rungs)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+audioTrackColumns,
		t.ID, t.MediaAssetID, t.Language, t.Label, t.Source, t.SourceLanguage, t.SourceKey, t.Status, t.Rungs)
	inserted, err := scanAudioTrack(row)
	if err != nil {
		return nil, fmt.Errorf("insert track: %w", err)
	}
	*t = *inserted

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return staleKeys, nil
}

// deleteDubVariantsTx removes one language's muxed outputs from
// media_variants and returns their object keys.
func deleteDubVariantsTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID, language string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		DELETE FROM media_variants
		 WHERE media_asset_id = $1 AND variant LIKE $2
		RETURNING object_key`, mediaID, likeEscape(DubVariantPrefix(language))+"%")
	if err != nil {
		return nil, fmt.Errorf("delete dub variants: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// likeEscape escapes LIKE metacharacters so a language tag is matched
// literally (`_` is a LIKE wildcard and the prefix is full of them).
func likeEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '%', '_':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// ListAudioTracks returns every track of an asset, oldest first.
func (s *MediaAssetStore) ListMediaAudioTracks(ctx context.Context, mediaID uuid.UUID) ([]MediaAudioTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+audioTrackColumns+`
		  FROM media_audio_tracks
		 WHERE media_asset_id = $1
		 ORDER BY created_at ASC, id ASC`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaAudioTrack
	for rows.Next() {
		t, err := scanAudioTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// CountAudioTracks returns how many tracks an asset has (for the per-asset cap).
func (s *MediaAssetStore) CountMediaAudioTracks(ctx context.Context, mediaID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM media_audio_tracks WHERE media_asset_id = $1`, mediaID).Scan(&n)
	return n, err
}

// GetAudioTrack loads one track scoped to its asset.
func (s *MediaAssetStore) GetMediaAudioTrack(ctx context.Context, mediaID, trackID uuid.UUID) (*MediaAudioTrack, error) {
	t, err := scanAudioTrack(s.db.QueryRow(ctx, `
		SELECT `+audioTrackColumns+`
		  FROM media_audio_tracks WHERE id = $1 AND media_asset_id = $2`, trackID, mediaID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAudioTrackNotFound
	}
	return t, err
}

// DeleteAudioTrack removes a track, its muxed variants' rows, and returns
// every object key that should now be deleted from the blob store.
func (s *MediaAssetStore) DeleteMediaAudioTrack(ctx context.Context, mediaID, trackID uuid.UUID) ([]string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var language string
	var sourceKey *string
	err = tx.QueryRow(ctx, `
		DELETE FROM media_audio_tracks
		 WHERE id = $1 AND media_asset_id = $2
		RETURNING language, source_key`, trackID, mediaID).Scan(&language, &sourceKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAudioTrackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("delete track: %w", err)
	}
	var keys []string
	if sourceKey != nil && *sourceKey != "" {
		keys = append(keys, *sourceKey)
	}
	variantKeys, err := deleteDubVariantsTx(ctx, tx, mediaID, language)
	if err != nil {
		return nil, err
	}
	keys = append(keys, variantKeys...)
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return keys, nil
}

// ClaimAudioTracks atomically claims pending tracks for this worker
// (FOR UPDATE SKIP LOCKED — safe across replicas). Tracks stuck in
// 'processing' past staleAfter are reclaimed from a dead worker.
func (s *MediaAssetStore) ClaimMediaAudioTracks(ctx context.Context, staleAfter time.Duration, limit int) ([]MediaAudioTrack, error) {
	rows, err := s.db.Query(ctx, `
		WITH claimable AS (
			SELECT id FROM media_audio_tracks
			WHERE status = 'pending'
			   OR (status = 'processing' AND claimed_at < $1)
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE media_audio_tracks t
		SET status = 'processing', claimed_at = NOW(), claim_token = gen_random_uuid()::text,
		    attempts = t.attempts + 1, updated_at = NOW()
		FROM claimable WHERE t.id = claimable.id
		RETURNING t.id, t.media_asset_id, t.language, t.label, t.source, t.source_language, t.source_key,
		          t.status, t.attempts, t.last_error, t.claimed_at, t.claim_token, t.rungs, t.created_at, t.updated_at`,
		time.Now().Add(-staleAfter), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaAudioTrack
	for rows.Next() {
		t, err := scanAudioTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// SetMediaAudioTrackSourceKey records the generated source audio, fenced by the
// claim token so a stale worker cannot overwrite a newer run's object.
func (s *MediaAssetStore) SetMediaAudioTrackSourceKey(ctx context.Context, trackID uuid.UUID, key string, token *string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_audio_tracks
		   SET source_key = $2, updated_at = NOW()
		 WHERE id = $1 AND status = 'processing'
		   AND ($3::text IS NULL OR claim_token = $3)`, trackID, key, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAudioTrackClaimLost
	}
	return nil
}

// CompleteAudioTrack marks a track ready with the rungs it produced.
// Conditional on holding the claim.
func (s *MediaAssetStore) CompleteMediaAudioTrack(ctx context.Context, trackID uuid.UUID, rungs []string, token *string) error {
	if rungs == nil {
		rungs = []string{}
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE media_audio_tracks
		   SET status = 'ready', rungs = $2, last_error = NULL, claim_token = NULL, updated_at = NOW()
		 WHERE id = $1 AND status = 'processing'
		   AND ($3::text IS NULL OR claim_token = $3)`, trackID, rungs, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAudioTrackClaimLost
	}
	return nil
}

// FailAudioTrack records a terminal failure. Conditional on holding the claim.
func (s *MediaAssetStore) FailMediaAudioTrack(ctx context.Context, trackID uuid.UUID, reason string, token *string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_audio_tracks
		   SET status = 'failed', last_error = $2, claim_token = NULL, updated_at = NOW()
		 WHERE id = $1 AND status = 'processing'
		   AND ($3::text IS NULL OR claim_token = $3)`, trackID, reason, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAudioTrackClaimLost
	}
	return nil
}

// ReleaseAudioTrack returns a claimed track to pending for a later retry.
func (s *MediaAssetStore) ReleaseMediaAudioTrack(ctx context.Context, trackID uuid.UUID, reason string, token *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_audio_tracks
		   SET status = 'pending', claim_token = NULL, last_error = $2, updated_at = NOW()
		 WHERE id = $1 AND status = 'processing'
		   AND ($3::text IS NULL OR claim_token = $3)`, trackID, reason, token)
	return err
}

// RequeueAudioTrack re-arms a READY track whose muxed outputs have gone
// (a transcode re-run prunes every variant it did not produce, dub ones
// included). It keeps the source audio and starts the mux over.
func (s *MediaAssetStore) RequeueMediaAudioTrack(ctx context.Context, trackID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE media_audio_tracks
		   SET status = 'pending', rungs = '{}', attempts = 0, claim_token = NULL,
		       last_error = NULL, updated_at = NOW()
		 WHERE id = $1 AND status = 'ready'`, trackID)
	return err
}
