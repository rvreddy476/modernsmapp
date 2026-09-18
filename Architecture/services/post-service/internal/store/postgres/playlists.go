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
//
// ownerView is the visibility decision, made by the caller (the service
// knows who is asking; the store does not). True returns the creator's whole
// shelf; false returns the public shelf — public only, because 'unlisted'
// means "reachable with the link, not discoverable in a list". It is applied
// in SQL rather than by filtering the page afterwards: a post-hoc filter
// would hand back short pages and make limit/offset lie about what is left.
func (s *Store) ListPlaylistsByCreator(ctx context.Context, creatorID uuid.UUID, ownerView bool, limit, offset int) ([]Playlist, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, channel_id, title, description, cover_url, visibility, item_count, created_at, updated_at
		FROM playlists WHERE creator_id = $1 AND ($4 OR visibility = 'public')
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		creatorID, limit, offset, ownerView)
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

// PlaylistPatch is a partial update: a nil field is "leave it alone". Same
// shape and same COALESCE-per-column treatment VideoSeriesPatch gets — the
// nullable cover can be set but not cleared through this shape, because the
// wire contract carries no explicit "set to null" signal.
type PlaylistPatch struct {
	Title       *string
	Description *string
	Visibility  *string
	CoverURL    *string
}

// UpdatePlaylist applies the non-nil fields of patch and returns the row as
// it now stands. Returns nil, nil when there is no such playlist.
func (s *Store) UpdatePlaylist(ctx context.Context, id uuid.UUID, patch PlaylistPatch) (*Playlist, error) {
	p := &Playlist{}
	err := s.db.QueryRow(ctx, `
		UPDATE playlists SET
			title       = COALESCE($2, title),
			description = COALESCE($3, description),
			visibility  = COALESCE($4, visibility),
			cover_url   = COALESCE($5, cover_url),
			updated_at  = NOW()
		WHERE id = $1
		RETURNING id, creator_id, channel_id, title, description, cover_url, visibility, item_count, created_at, updated_at`,
		id, patch.Title, patch.Description, patch.Visibility, patch.CoverURL,
	).Scan(&p.ID, &p.CreatorID, &p.ChannelID, &p.Title, &p.Description, &p.CoverURL, &p.Visibility, &p.ItemCount, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// MovePlaylistItem moves one post to newPosition and returns the playlist's
// items in their new order.
//
// playlist_items' PRIMARY KEY is (playlist_id, position), so a position is
// not a free-standing attribute that can simply be rewritten: the moved row
// would collide with whatever already sits at the target, and a bulk
// "position = position - 1" shift can collide with itself mid-statement
// (the PK is not deferrable). The whole ordering is therefore rewritten
// inside one transaction — read the rows FOR UPDATE, reorder in Go, delete
// and re-insert with contiguous positions, preserving added_at. A playlist
// is small, and the rewrite also normalises any gaps or client-chosen
// positions AddPlaylistItem let through.
//
// Returns pgx.ErrNoRows when the post is not in the playlist.
func (s *Store) MovePlaylistItem(ctx context.Context, playlistID, postID uuid.UUID, newPosition int) ([]PlaylistItem, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		SELECT post_id, added_at FROM playlist_items
		WHERE playlist_id = $1 ORDER BY position ASC, added_at ASC
		FOR UPDATE`, playlistID)
	if err != nil {
		return nil, err
	}
	type row struct {
		postID  uuid.UUID
		addedAt time.Time
	}
	var ordered []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.postID, &r.addedAt); err != nil {
			rows.Close()
			return nil, err
		}
		ordered = append(ordered, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	from := -1
	for i, r := range ordered {
		if r.postID == postID {
			from = i
			break
		}
	}
	if from < 0 {
		return nil, pgx.ErrNoRows
	}
	to := newPosition
	if to < 0 {
		to = 0
	}
	if to > len(ordered)-1 {
		to = len(ordered) - 1
	}
	moved := ordered[from]
	ordered = append(ordered[:from], ordered[from+1:]...)
	ordered = append(ordered, row{})
	copy(ordered[to+1:], ordered[to:])
	ordered[to] = moved

	if _, err := tx.Exec(ctx, `DELETE FROM playlist_items WHERE playlist_id = $1`, playlistID); err != nil {
		return nil, err
	}
	items := make([]PlaylistItem, 0, len(ordered))
	for i, r := range ordered {
		if _, err := tx.Exec(ctx, `
			INSERT INTO playlist_items (playlist_id, post_id, position, added_at)
			VALUES ($1, $2, $3, $4)`, playlistID, r.postID, i, r.addedAt); err != nil {
			return nil, err
		}
		items = append(items, PlaylistItem{PlaylistID: playlistID, PostID: r.postID, Position: i, AddedAt: r.addedAt})
	}
	if _, err := tx.Exec(ctx, `UPDATE playlists SET updated_at = NOW() WHERE id = $1`, playlistID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
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
