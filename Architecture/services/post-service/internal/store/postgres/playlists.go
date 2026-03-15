package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Playlist represents a user-created collection of video posts.
type Playlist struct {
	ID          uuid.UUID  `json:"id"`
	CreatorID   uuid.UUID  `json:"creator_id"`
	ChannelID   *uuid.UUID `json:"channel_id,omitempty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	CoverURL    *string    `json:"cover_url,omitempty"`
	Visibility  string     `json:"visibility"`
	ItemCount   int        `json:"item_count"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PlaylistItem links a post to a playlist at a given position.
type PlaylistItem struct {
	PlaylistID uuid.UUID `json:"playlist_id"`
	PostID     uuid.UUID `json:"post_id"`
	Position   int       `json:"position"`
	AddedAt    time.Time `json:"added_at"`
}

// CreatePlaylist inserts a new playlist and populates id, created_at, updated_at.
func (s *Store) CreatePlaylist(ctx context.Context, p *Playlist) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO playlists (creator_id, channel_id, title, description, cover_url, visibility)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		p.CreatorID, p.ChannelID, p.Title, p.Description, p.CoverURL, p.Visibility,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

// GetPlaylist retrieves a playlist by ID. Returns nil, nil if not found.
func (s *Store) GetPlaylist(ctx context.Context, id uuid.UUID) (*Playlist, error) {
	p := &Playlist{}
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, channel_id, title, description, cover_url, visibility, item_count, created_at, updated_at
		FROM playlists WHERE id = $1`, id,
	).Scan(&p.ID, &p.CreatorID, &p.ChannelID, &p.Title, &p.Description, &p.CoverURL, &p.Visibility, &p.ItemCount, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// ListPlaylistsByCreator returns paginated playlists for a creator.
func (s *Store) ListPlaylistsByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]Playlist, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, channel_id, title, description, cover_url, visibility, item_count, created_at, updated_at
		FROM playlists WHERE creator_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		creatorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Playlist
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.CreatorID, &p.ChannelID, &p.Title, &p.Description, &p.CoverURL, &p.Visibility, &p.ItemCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// DeletePlaylist removes a playlist by ID.
func (s *Store) DeletePlaylist(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM playlists WHERE id = $1`, id)
	return err
}

// AddPlaylistItem inserts a playlist item and increments item_count in a single tx.
func (s *Store) AddPlaylistItem(ctx context.Context, playlistID, postID uuid.UUID, position int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `
		INSERT INTO playlist_items (playlist_id, post_id, position)
		VALUES ($1, $2, $3)
		ON CONFLICT (playlist_id, position) DO UPDATE SET post_id = EXCLUDED.post_id`,
		playlistID, postID, position)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE playlists SET item_count = (SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1), updated_at = NOW() WHERE id = $1`,
		playlistID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemovePlaylistItem deletes a playlist item by playlist+post and decrements item_count in a single tx.
func (s *Store) RemovePlaylistItem(ctx context.Context, playlistID, postID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `DELETE FROM playlist_items WHERE playlist_id = $1 AND post_id = $2`, playlistID, postID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE playlists SET item_count = (SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1), updated_at = NOW() WHERE id = $1`,
		playlistID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetPlaylistItems returns all items in a playlist ordered by position.
func (s *Store) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID) ([]PlaylistItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT playlist_id, post_id, position, added_at
		FROM playlist_items WHERE playlist_id = $1
		ORDER BY position ASC`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PlaylistItem
	for rows.Next() {
		var item PlaylistItem
		if err := rows.Scan(&item.PlaylistID, &item.PostID, &item.Position, &item.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
