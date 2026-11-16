package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Retired sounds (2026-11-16, migration 032; the rules are in
// asset_purge.go). The three reads and writes a retired sound needs beyond
// the delete paths: who may still hear it, and the two ways its objects go.

// ErrSoundNotFound: no sound with that id.
var ErrSoundNotFound = errors.New("sound not found")

// ErrLibrarySoundTakedown: a platform library sound is withdrawn through its
// library row (admin sounds, withdraw everywhere), not taken down here: its
// object belongs to the library row, which still names it.
var ErrLibrarySoundTakedown = errors.New("a library sound is withdrawn through the library")

// retiredSoundCarrierSQL is the first video of post $2, when post $2 is live
// and plays sound $1. post-service's tables (the same database: it reads
// and writes audio_tracks there); the order is its postMediaOrder.
const retiredSoundCarrierSQL = `
	SELECT pm.media_id
	  FROM posts p
	  JOIN post_media pm ON pm.post_id = p.id
	 WHERE p.id = $2
	   AND p.audio_track_id = $1
	   AND p.deleted_at IS NULL
	   AND pm.kind = 'video'
	 ORDER BY pm.position NULLS LAST, pm.media_id
	 LIMIT 1`

// RetiredSoundCarrier answers "may this post play this retired sound": the
// media id whose audience decides it (the post's first video), or nil when
// the post is not live, does not play the sound, or this database has no
// posts (a per-service schema: nobody can show a carrying post, so nobody
// hears a retired sound). A store fault is an error, never a nil.
func (s *MediaAssetStore) RetiredSoundCarrier(ctx context.Context, soundID, postID uuid.UUID) (*uuid.UUID, error) {
	var present bool
	if err := s.db.QueryRow(ctx,
		`SELECT to_regclass('public.posts') IS NOT NULL AND to_regclass('public.post_media') IS NOT NULL`).
		Scan(&present); err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	var mediaID uuid.UUID
	err := s.db.QueryRow(ctx, retiredSoundCarrierSQL, soundID, postID).Scan(&mediaID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mediaID, nil
}

// soundTakedownMatch is one extracted sound, in any status ($1 = its id).
const soundTakedownMatch = `id = $1 AND library_id IS NULL`

// TakeDownSound destroys one extracted sound for copyright or moderation:
// its objects are recorded for reclaim and its row marked deleted with no
// keys, so every reel that played it goes silent. It returns the keys to
// delete now. ErrSoundNotFound for no such sound; ErrLibrarySoundTakedown
// for a library sound. Taking down a sound already deleted is a no-op.
func (s *MediaAssetStore) TakeDownSound(ctx context.Context, soundID uuid.UUID) ([]string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var library *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT library_id FROM audio_tracks WHERE id = $1 FOR UPDATE`, soundID).Scan(&library)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSoundNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock sound: %w", err)
	}
	if library != nil && *library != uuid.Nil {
		return nil, ErrLibrarySoundTakedown
	}
	keys, err := destroySoundsTx(ctx, tx, soundTakedownMatch, soundID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit sound takedown: %w", err)
	}
	return keys, nil
}

// unusedRetiredSoundsSQL picks retired sounds that no post references any
// more ($1 the grace cutoff, $2 the batch), locking them. A soft-deleted
// post still references its sound (it may be restored); only the purge
// removes the reference. The grace covers an attach that read the sound as
// ready just before it was retired and commits just after.
const unusedRetiredSoundsSQL = `
	SELECT t.id FROM audio_tracks t
	 WHERE t.status = '` + SoundStatusRetired + `'
	   AND t.retired_at IS NOT NULL AND t.retired_at < $1
	   AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.audio_track_id = t.id)
	 ORDER BY t.retired_at, t.id
	 LIMIT $2
	 FOR UPDATE OF t SKIP LOCKED`

// unusedRetiredSoundMatch re-checks, inside the destroy, that each picked
// sound is still retired and still unreferenced ($1 the picked ids).
const unusedRetiredSoundMatch = `id = ANY($1::uuid[]) AND status = '` + SoundStatusRetired + `'
	AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.audio_track_id = audio_tracks.id)`

// ReclaimUnusedRetiredSounds destroys up to limit retired sounds that were
// retired before now-grace and that no post references any more, and
// returns the object keys it recorded in the reclaim ledger (the blob
// reclaim worker deletes them). A database with no posts table reclaims
// nothing: no reference can be proved absent there.
func (s *MediaAssetStore) ReclaimUnusedRetiredSounds(ctx context.Context, grace time.Duration, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	present, err := tableExists(ctx, tx, "posts")
	if err != nil {
		return nil, fmt.Errorf("probe posts: %w", err)
	}
	if !present {
		return nil, nil
	}
	rows, err := tx.Query(ctx, unusedRetiredSoundsSQL, time.Now().Add(-grace), limit)
	if err != nil {
		return nil, fmt.Errorf("list unused retired sounds: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	keys, err := destroySoundsTx(ctx, tx, unusedRetiredSoundMatch, ids)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit retired sound reclaim: %w", err)
	}
	return keys, nil
}
