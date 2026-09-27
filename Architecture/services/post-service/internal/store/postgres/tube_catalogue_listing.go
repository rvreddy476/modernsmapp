package postgres

import (
	"context"

	"github.com/google/uuid"
)

// Tube catalogue listing (MTube search, 2026-09-27).
//
// search-service rebuilds its tube_channels / tube_collections indices by
// walking these two reads through the internal route in
// internal/http/channels_internal_list.go. Neither channels nor playlists
// publish lifecycle events, so a walk over the source of truth is the only
// way the index can start, and the only way it can heal.
//
// Both are keyset pages over the surrogate id (uuid order is arbitrary but
// stable, which is all a full walk needs): rows with id > after, ascending,
// at most limit. A caller loops until a page comes back short.

// ListChannelsAfter returns up to limit channel rows with id > after.
func (s *Store) ListChannelsAfter(ctx context.Context, after uuid.UUID, limit int) ([]Channel, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, handle, description, avatar_media_id, subscriber_count, created_at, updated_at
		FROM channels
		WHERE id > $1
		ORDER BY id ASC
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Channel, 0, limit)
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(&ch.ID, &ch.UserID, &ch.Name, &ch.Handle, &ch.About, &ch.AvatarMediaID,
			&ch.SubscriberCount, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// ListPlaylistsAfter returns up to limit playlist rows with id > after,
// EVERY visibility included. The caller decides what to do with a
// non-public row (search-service deletes its document); listing only the
// public ones would leave a playlist that went private sitting in the
// index with no signal to remove it.
func (s *Store) ListPlaylistsAfter(ctx context.Context, after uuid.UUID, limit int) ([]Playlist, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, channel_id, title, description, cover_url, visibility, item_count, created_at, updated_at
		FROM playlists
		WHERE id > $1
		ORDER BY id ASC
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Playlist, 0, limit)
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.CreatorID, &p.ChannelID, &p.Title, &p.Description, &p.CoverURL,
			&p.Visibility, &p.ItemCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
