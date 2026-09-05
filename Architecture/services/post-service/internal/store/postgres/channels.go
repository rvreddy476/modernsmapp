package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tube channels (2026-09-05). One channel per account, owned by post-service.
//
// The rows live in the shared `channels` table that user-service's phase-6
// DDL created (see migrations/041_channels.sql for why it is adapted rather
// than duplicated). `about` is the table's `description` column; the
// surrogate `id` is kept only for the video_series / playlists FKs and is
// never exposed — user_id is the identity the API speaks.

// Channel is the stored channel row.
type Channel struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Name          string
	Handle        string
	About         string
	AvatarMediaID *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ChannelPatch is a partial update. A nil field is "leave as is";
// ClearAvatar removes the avatar (JSON `"avatar_media_id": null`).
type ChannelPatch struct {
	Name          *string
	Handle        *string
	About         *string
	AvatarMediaID *uuid.UUID
	ClearAvatar   bool
}

var (
	// ErrChannelExists: the account already has its one channel.
	ErrChannelExists = errors.New("channel already exists for this account")
	// ErrHandleTaken: another channel owns the handle.
	ErrHandleTaken = errors.New("channel handle is taken")
	// ErrChannelNotFound: no channel for the user / handle.
	ErrChannelNotFound = errors.New("channel not found")
	// ErrChannelOwnerUnknown: the owner has no row in the shared users
	// table (the FK user-service placed on channels.user_id).
	ErrChannelOwnerUnknown = errors.New("channel owner is not a known user")
)

const channelColumns = `id, user_id, name, handle, description, avatar_media_id, created_at, updated_at`

func scanChannel(row pgx.Row) (*Channel, error) {
	var ch Channel
	if err := row.Scan(&ch.ID, &ch.UserID, &ch.Name, &ch.Handle, &ch.About, &ch.AvatarMediaID, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &ch, nil
}

func channelPayload(ch *Channel) events.ChannelPayload {
	p := events.ChannelPayload{
		UserID:    ch.UserID.String(),
		Name:      ch.Name,
		Handle:    ch.Handle,
		About:     ch.About,
		CreatedAt: ch.CreatedAt,
		UpdatedAt: ch.UpdatedAt,
	}
	if ch.AvatarMediaID != nil {
		s := ch.AvatarMediaID.String()
		p.AvatarMediaID = &s
	}
	return p
}

// mapChannelWriteError turns the unique / FK violations the channels table
// can raise into the typed errors the service maps to 409 / 400.
func mapChannelWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505": // unique_violation
		if strings.Contains(pgErr.ConstraintName, "handle") {
			return ErrHandleTaken
		}
		if strings.Contains(pgErr.ConstraintName, "user") {
			return ErrChannelExists
		}
		return ErrHandleTaken
	case "23503": // foreign_key_violation (channels.user_id -> users.id)
		return ErrChannelOwnerUnknown
	}
	return err
}

// CreateChannel inserts the account's channel and the tube.channel.created
// outbox event in one transaction. ch is filled in from the inserted row.
func (s *Store) CreateChannel(ctx context.Context, ch *Channel) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		INSERT INTO channels (user_id, name, handle, description, avatar_media_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+channelColumns,
		ch.UserID, ch.Name, ch.Handle, ch.About, ch.AvatarMediaID)
	created, err := scanChannel(row)
	if err != nil {
		return mapChannelWriteError(err)
	}
	*ch = *created

	if err := InsertOutboxEventTx(ctx, tx, events.TubeChannelCreated, "channel", ch.UserID, channelPayload(ch)); err != nil {
		return fmt.Errorf("enqueue channel.created: %w", err)
	}
	return tx.Commit(ctx)
}

// UpdateChannel applies a partial update to the account's channel and
// enqueues tube.channel.updated in the same transaction. Returns
// ErrChannelNotFound when the account has no channel.
func (s *Store) UpdateChannel(ctx context.Context, userID uuid.UUID, patch ChannelPatch) (*Channel, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		UPDATE channels SET
			name            = COALESCE($2, name),
			handle          = COALESCE($3, handle),
			description     = COALESCE($4, description),
			avatar_media_id = CASE WHEN $6 THEN NULL ELSE COALESCE($5, avatar_media_id) END,
			updated_at      = now()
		WHERE user_id = $1
		RETURNING `+channelColumns,
		userID, patch.Name, patch.Handle, patch.About, patch.AvatarMediaID, patch.ClearAvatar)
	updated, err := scanChannel(row)
	if err != nil {
		return nil, mapChannelWriteError(err)
	}
	if updated == nil {
		return nil, ErrChannelNotFound
	}

	if err := InsertOutboxEventTx(ctx, tx, events.TubeChannelUpdated, "channel", updated.UserID, channelPayload(updated)); err != nil {
		return nil, fmt.Errorf("enqueue channel.updated: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// GetChannelByUserID returns the account's channel, or nil when it has none.
func (s *Store) GetChannelByUserID(ctx context.Context, userID uuid.UUID) (*Channel, error) {
	return scanChannel(s.db.QueryRow(ctx, `SELECT `+channelColumns+` FROM channels WHERE user_id = $1`, userID))
}

// GetChannelByHandle returns the channel owning a (lowercase) handle, or nil.
func (s *Store) GetChannelByHandle(ctx context.Context, handle string) (*Channel, error) {
	return scanChannel(s.db.QueryRow(ctx, `SELECT `+channelColumns+` FROM channels WHERE handle = $1`, handle))
}

// ChannelHandleExists reports whether any channel owns the handle.
func (s *Store) ChannelHandleExists(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channels WHERE handle = $1)`, handle).Scan(&exists)
	return exists, err
}

// GetChannelsByUserIDs loads the channels of a page of authors in one query.
// Authors without a channel are simply absent from the map.
func (s *Store) GetChannelsByUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*Channel, error) {
	out := make(map[uuid.UUID]*Channel, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+channelColumns+` FROM channels WHERE user_id = ANY($1)`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(&ch.ID, &ch.UserID, &ch.Name, &ch.Handle, &ch.About, &ch.AvatarMediaID, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		c := ch
		out[c.UserID] = &c
	}
	return out, rows.Err()
}

// channelVideoCountWhere is what "a video on the channel" means for the
// public count: a live, approved, public long video by the owner.
const channelVideoCountWhere = `
	author_id = ANY($1)
	AND content_type IN ('long_video', 'video')
	AND deleted_at IS NULL
	AND visibility = 'public'
	AND review_status = 'approved'
	AND publish_at IS NULL`

// CountChannelVideos returns the public long-video count for one owner.
func (s *Store) CountChannelVideos(ctx context.Context, userID uuid.UUID) (int, error) {
	counts, err := s.CountChannelVideosBatch(ctx, []uuid.UUID{userID})
	if err != nil {
		return 0, err
	}
	return counts[userID], nil
}

// CountChannelVideosBatch returns public long-video counts per owner.
func (s *Store) CountChannelVideosBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT author_id, COUNT(*) FROM posts WHERE `+channelVideoCountWhere+` GROUP BY author_id`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ChannelSearchHit is one channel search row with its public video count,
// so the search page does not pay one count query per row.
type ChannelSearchHit struct {
	Channel
	VideoCount int
}

// channelVideoCountCorrelated is channelVideoCountWhere for one channel row
// (correlated on channels.user_id) — the same definition of "a video on the
// channel" as the public count.
const channelVideoCountCorrelated = `
	SELECT COUNT(*) FROM posts p
	WHERE p.author_id = c.user_id
	AND p.content_type IN ('long_video', 'video')
	AND p.deleted_at IS NULL
	AND p.visibility = 'public'
	AND p.review_status = 'approved'
	AND p.publish_at IS NULL`

// EscapeLikePattern makes q safe to embed in a LIKE pattern: the wildcard
// and escape characters become literals (backslash is Postgres' default
// LIKE escape character).
func EscapeLikePattern(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return q
}

// SearchChannels finds channels whose handle starts with q or whose name
// contains q (q already trimmed and lowercased; the handle column is
// lowercase by construction, the name is compared through lower()).
// Handle-prefix matches come first, then the most-published channels, then
// handle order for a stable page. limit is applied as given.
func (s *Store) SearchChannels(ctx context.Context, q string, limit int) ([]ChannelSearchHit, error) {
	if q == "" || limit <= 0 {
		return []ChannelSearchHit{}, nil
	}
	escaped := EscapeLikePattern(q)
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.user_id, c.name, c.handle, c.description, c.avatar_media_id, c.created_at, c.updated_at,
		       (`+channelVideoCountCorrelated+`) AS video_count,
		       (c.handle LIKE $1) AS handle_prefix
		FROM channels c
		WHERE c.handle LIKE $1 OR lower(c.name) LIKE $2
		ORDER BY handle_prefix DESC, video_count DESC, c.handle ASC
		LIMIT $3`, escaped+"%", "%"+escaped+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChannelSearchHit, 0, limit)
	for rows.Next() {
		var hit ChannelSearchHit
		var handlePrefix bool
		if err := rows.Scan(&hit.ID, &hit.UserID, &hit.Name, &hit.Handle, &hit.About, &hit.AvatarMediaID,
			&hit.CreatedAt, &hit.UpdatedAt, &hit.VideoCount, &handlePrefix); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// ChannelAvatarOwners maps each media id that is some channel's avatar to
// that channel's owner. Used by the media-access authority: a channel avatar
// is public to every viewer.
func (s *Store) ChannelAvatarOwners(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := make(map[uuid.UUID]uuid.UUID)
	if len(mediaIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT avatar_media_id, user_id FROM channels WHERE avatar_media_id = ANY($1)`, mediaIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mediaID, userID uuid.UUID
		if err := rows.Scan(&mediaID, &userID); err != nil {
			return nil, err
		}
		out[mediaID] = userID
	}
	return out, rows.Err()
}
