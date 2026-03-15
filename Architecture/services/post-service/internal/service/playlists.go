package service

import (
	"context"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
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
	p, err := s.pgStore.GetPlaylist(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("playlist not found")
	}
	if p.Visibility == "private" {
		if callerID == nil || *callerID != p.CreatorID {
			return nil, fmt.Errorf("forbidden: playlist is private")
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
	p, err := s.pgStore.GetPlaylist(ctx, playlistID)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("playlist not found")
	}
	if p.CreatorID != callerID {
		return fmt.Errorf("forbidden: you do not own this playlist")
	}
	return s.pgStore.DeletePlaylist(ctx, playlistID)
}

// AddPlaylistItem adds a post to a playlist at the given position.
func (s *Service) AddPlaylistItem(ctx context.Context, playlistID, postID uuid.UUID, position int) error {
	return s.pgStore.AddPlaylistItem(ctx, playlistID, postID, position)
}

// RemovePlaylistItem removes a post from a playlist.
func (s *Service) RemovePlaylistItem(ctx context.Context, playlistID, postID uuid.UUID) error {
	return s.pgStore.RemovePlaylistItem(ctx, playlistID, postID)
}

// GetPlaylistItems returns all items in a playlist ordered by position.
func (s *Service) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID) ([]postgres.PlaylistItem, error) {
	return s.pgStore.GetPlaylistItems(ctx, playlistID)
}
