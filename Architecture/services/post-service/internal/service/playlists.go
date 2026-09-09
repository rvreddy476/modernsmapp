package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreatePlaylist creates a new playlist owned by the creator.
func (s *Service) CreatePlaylist(ctx context.Context, p *postgres.Playlist) error {
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.Visibility == "" {
		p.Visibility = "public"
	}
	return s.pgStore.CreatePlaylist(ctx, p)
}

// GetPlaylist retrieves a playlist by ID, enforcing visibility: private playlists are
// only visible to their creator. Pass a nil callerID for unauthenticated callers.
func (s *Service) GetPlaylist(ctx context.Context, id uuid.UUID, callerID *uuid.UUID) (*postgres.Playlist, error) {
	// Fail closed: with no store behind the lookup there is no way to know
	// whether this playlist is private, so it is not served.
	if s.authoringOwners == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	p, err := s.authoringOwners.GetPlaylist(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlaylistNotFound
		}
		return nil, err
	}
	if p == nil {
		return nil, ErrPlaylistNotFound
	}
	if p.Visibility == "private" {
		if callerID == nil || *callerID != p.CreatorID {
			return nil, ErrPlaylistPrivate
		}
	}
	return p, nil
}

// ListPlaylistsByCreator returns paginated playlists for the given creator.
func (s *Service) ListPlaylistsByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.Playlist, error) {
	return s.pgStore.ListPlaylistsByCreator(ctx, creatorID, limit, offset)
}

// DeletePlaylist removes a playlist after verifying ownership.
func (s *Service) DeletePlaylist(ctx context.Context, callerID, playlistID uuid.UUID) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.DeletePlaylist(ctx, playlistID)
}

// AddPlaylistItem adds a post to a playlist after verifying the caller owns
// the playlist — the same rule DeletePlaylist applies. Editing a playlist is
// editing the playlist, whoever authored the post being added.
func (s *Service) AddPlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID, position int) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.AddPlaylistItem(ctx, playlistID, postID, position)
}

// RemovePlaylistItem removes a post from a playlist after verifying the
// caller owns the playlist.
func (s *Service) RemovePlaylistItem(ctx context.Context, callerID, playlistID, postID uuid.UUID) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	return s.pgStore.RemovePlaylistItem(ctx, playlistID, postID)
}

// GetPlaylistItems returns all items in a playlist ordered by position,
// enforcing the SAME visibility rule GetPlaylist applies: a private playlist
// is readable only by its creator. Reading the contents of a private playlist
// is reading the playlist; the two endpoints must not disagree. Pass a nil
// callerID for unauthenticated callers.
func (s *Service) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID, callerID *uuid.UUID) ([]postgres.PlaylistItem, error) {
	if _, err := s.GetPlaylist(ctx, playlistID, callerID); err != nil {
		return nil, err
	}
	return s.authoringOwners.GetPlaylistItems(ctx, playlistID)
}
