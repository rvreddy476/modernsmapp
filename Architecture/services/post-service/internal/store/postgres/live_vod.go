package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Live -> video (MTube, 2026-09-27; migration 051 part B).

	When live-service-v2 announces a finished recording
	(live.stream.vod_ready) the consumer creates ONE long_video post for the
	streamer: source = 'live', live_stream_id = the stream. The partial
	unique index uq_posts_live_stream is what makes a redelivered event a
	no-op — the INSERT of a second post for the same stream cannot commit.
*/

// PostSource values.
const (
	PostSourceUpload = "upload"
	PostSourceLive   = "live"
)

// GetPostByLiveStream returns the VOD post created for a stream, or nil.
func (s *Store) GetPostByLiveStream(ctx context.Context, streamID uuid.UUID) (*Post, error) {
	p, err := scanPost(s.db.QueryRow(ctx,
		`SELECT `+postCols+` FROM posts WHERE live_stream_id = $1 AND deleted_at IS NULL`, streamID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

// FindMediaByStorageKeySuffix resolves a recording URL to the media asset
// media-service registered for it: the asset whose storage_key ends with
// the URL's object key. Returns uuid.Nil when none matches.
func (s *Store) FindMediaByStorageKeySuffix(ctx context.Context, keySuffix string) (uuid.UUID, error) {
	if keySuffix == "" {
		return uuid.Nil, nil
	}
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id FROM media_assets
		WHERE file_type = 'video' AND storage_key LIKE '%' || $1
		ORDER BY created_at DESC LIMIT 1`, keySuffix).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

// LiveVODInsert is what CreateLiveVODPost writes.
type LiveVODInsert struct {
	Post          *Post
	StreamID      uuid.UUID
	MediaID       uuid.UUID
	VideoMetadata *VideoMetadata
	// EventType / EventPayload: the PostCreated outbox row committed with
	// the post (empty type = none).
	EventType    string
	EventPayload interface{}
}

// CreateLiveVODPost inserts the post, its media row, its video_metadata and
// the outbox event in ONE transaction, idempotently: if a post already
// exists for the stream (a redelivered event, a concurrent consumer) that
// post is returned and created is false. Nothing else is written then.
func (s *Store) CreateLiveVODPost(ctx context.Context, in LiveVODInsert) (post *Post, created bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialise on the stream id: the advisory lock turns a concurrent
	// second delivery into a reader of the first one's row.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "live_vod:"+in.StreamID.String()); err != nil {
		return nil, false, err
	}
	existing, err := scanPost(tx.QueryRow(ctx,
		`SELECT `+postCols+` FROM posts WHERE live_stream_id = $1`, in.StreamID))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	if err := insertPostTx(ctx, tx, in.Post); err != nil {
		return nil, false, fmt.Errorf("insert vod post: %w", err)
	}
	// source / live_stream_id are not in insertPostTx's column list (that
	// list is the ordinary create path's); stamp them here, same tx.
	if _, err := tx.Exec(ctx,
		`UPDATE posts SET source = $2, live_stream_id = $3 WHERE id = $1`,
		in.Post.ID, PostSourceLive, in.StreamID); err != nil {
		return nil, false, fmt.Errorf("stamp vod source: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO post_media (post_id, media_id, kind, position) VALUES ($1, $2, 'video', 0)
		 ON CONFLICT DO NOTHING`, in.Post.ID, in.MediaID); err != nil {
		return nil, false, fmt.Errorf("attach vod media: %w", err)
	}
	if vm := in.VideoMetadata; vm != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO video_metadata (post_id, duration_seconds, orientation, trim_start_ms,
				computed_category, final_category, upload_status, media_asset_id, created_at, updated_at)
			VALUES ($1, $2, $3, 0, $4, $5, $6, $7, NOW(), NOW())
			ON CONFLICT (post_id) DO NOTHING`,
			vm.PostID, vm.DurationSeconds, vm.Orientation, vm.ComputedCategory, vm.FinalCategory,
			vm.UploadStatus, vm.MediaAssetID); err != nil {
			return nil, false, fmt.Errorf("insert vod video_metadata: %w", err)
		}
	}
	if in.EventType != "" {
		if err := InsertOutboxEventTx(ctx, tx, in.EventType, "post", in.Post.ID, in.EventPayload); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return in.Post, true, nil
}
