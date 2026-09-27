package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	System collections (MTube, 2026-09-27; migration 051 part D).

	playlists.kind is 'user' for everything a creator makes and one of the
	two system kinds below for the collections the SERVER owns on the
	viewer's behalf:

	  watch_later  "Queue"   POST/DELETE /v1/posts/:id/watch-later
	  liked        "Loved"   filled by liking a long video, emptied by unliking

	At most one of each system kind per owner (uq_playlists_system_kind), so
	GetOrCreateSystemPlaylist is an upsert on that index and two concurrent
	first reads produce one row. System playlists are always private and
	refuse rename / delete / visibility change at the service.
*/

// Playlist kinds.
const (
	PlaylistKindUser       = "user"
	PlaylistKindWatchLater = "watch_later"
	PlaylistKindLiked      = "liked"
)

// SystemPlaylistTitle is the title a system playlist is created with.
func SystemPlaylistTitle(kind string) string {
	switch kind {
	case PlaylistKindWatchLater:
		return "Queue"
	case PlaylistKindLiked:
		return "Loved"
	}
	return ""
}

// IsSystemPlaylistKind reports whether kind names a server-owned collection.
func IsSystemPlaylistKind(kind string) bool {
	return kind == PlaylistKindWatchLater || kind == PlaylistKindLiked
}

// GetOrCreateSystemPlaylist returns the owner's collection of the given
// system kind, creating it (private, titled by SystemPlaylistTitle) on first
// read. Race-safe through the partial unique index.
func (s *Store) GetOrCreateSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string) (*Playlist, error) {
	if !IsSystemPlaylistKind(kind) {
		return nil, fmt.Errorf("not a system playlist kind: %q", kind)
	}
	p := &Playlist{}
	err := s.db.QueryRow(ctx, `
		INSERT INTO playlists (creator_id, title, description, visibility, kind)
		VALUES ($1, $2, '', 'private', $3)
		ON CONFLICT (creator_id, kind) WHERE kind <> 'user'
		DO UPDATE SET updated_at = playlists.updated_at
		RETURNING `+playlistColumns,
		ownerID, SystemPlaylistTitle(kind), kind,
	).Scan(playlistScanDestinations(p)...)
	if err != nil {
		return nil, fmt.Errorf("get or create system playlist %s: %w", kind, err)
	}
	return p, nil
}

// GetSystemPlaylist returns the owner's collection of the given kind, or
// nil when it has not been created yet.
func (s *Store) GetSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string) (*Playlist, error) {
	p := &Playlist{}
	err := s.db.QueryRow(ctx,
		`SELECT `+playlistColumns+` FROM playlists WHERE creator_id = $1 AND kind = $2`,
		ownerID, kind).Scan(playlistScanDestinations(p)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// AddToSystemPlaylist appends postID to the owner's collection of the given
// kind (creating the collection if needed). Idempotent: a post already in
// the collection stays where it is. Returns whether a row was added.
func (s *Store) AddToSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string, postID uuid.UUID) (bool, error) {
	p, err := s.GetOrCreateSystemPlaylist(ctx, ownerID, kind)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialise appends on this playlist so two adds cannot pick the same
	// position (the PK is (playlist_id, position)).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM playlists WHERE id = $1 FOR UPDATE`, p.ID); err != nil {
		return false, err
	}
	// The PK is (playlist_id, position), so "already in the collection"
	// is not a conflict the INSERT would see; the NOT EXISTS is what makes
	// the add idempotent, under the row lock above.
	tag, err := tx.Exec(ctx, `
		INSERT INTO playlist_items (playlist_id, post_id, position)
		SELECT $1, $2, COALESCE((SELECT MAX(position) + 1 FROM playlist_items WHERE playlist_id = $1), 0)
		WHERE NOT EXISTS (SELECT 1 FROM playlist_items WHERE playlist_id = $1 AND post_id = $2)`, p.ID, postID)
	if err != nil {
		return false, fmt.Errorf("add to %s: %w", kind, err)
	}
	added := tag.RowsAffected() > 0
	if added {
		if _, err := tx.Exec(ctx,
			`UPDATE playlists SET item_count = (SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1), updated_at = NOW() WHERE id = $1`,
			p.ID); err != nil {
			return false, err
		}
	}
	return added, tx.Commit(ctx)
}

// RemoveFromSystemPlaylist removes postID from the owner's collection.
// Idempotent; a missing collection or a post not in it is a no-op.
func (s *Store) RemoveFromSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string, postID uuid.UUID) (bool, error) {
	p, err := s.GetSystemPlaylist(ctx, ownerID, kind)
	if err != nil || p == nil {
		return false, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `DELETE FROM playlist_items WHERE playlist_id = $1 AND post_id = $2`, p.ID, postID)
	if err != nil {
		return false, err
	}
	removed := tag.RowsAffected() > 0
	if removed {
		if _, err := tx.Exec(ctx,
			`UPDATE playlists SET item_count = (SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1), updated_at = NOW() WHERE id = $1`,
			p.ID); err != nil {
			return false, err
		}
	}
	return removed, tx.Commit(ctx)
}

// IsInSystemPlaylist answers viewer_queued (kind watch_later) for one post.
func (s *Store) IsInSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string, postID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM playlist_items pi
			JOIN playlists p ON p.id = pi.playlist_id
			WHERE p.creator_id = $1 AND p.kind = $2 AND pi.post_id = $3)`,
		ownerID, kind, postID).Scan(&exists)
	return exists, err
}
