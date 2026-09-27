package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	System collections (MTube, 2026-09-27).

	  GET    /v1/playlists/system/:kind       kind = watch_later | liked;
	                                          created on first read, private
	  POST   /v1/posts/:id/watch-later        add to the caller's Queue (idempotent)
	  DELETE /v1/posts/:id/watch-later        remove (idempotent)

	The `liked` collection ("Loved") is written by ToggleLike alone, and
	only for LONG videos: reels keep /v1/reels/liked and are never
	double-written here.

	A system playlist refuses rename, delete and visibility change
	(ErrSystemPlaylist -> 409 SYSTEM_PLAYLIST); its items are managed by the
	routes above, never by the generic playlist-item routes (those refuse
	too, through the same guard).
*/

var (
	// ErrSystemPlaylist: the playlist is server-owned; the generic edit /
	// delete / item routes do not apply to it.
	ErrSystemPlaylist = errors.New("this collection is managed by the app and cannot be edited or deleted")
	// ErrInvalidSystemPlaylistKind: :kind is not watch_later or liked.
	ErrInvalidSystemPlaylistKind = errors.New("kind must be watch_later or liked")
)

// systemPlaylistStore is the slice of the store the system-collection flows
// need, an interface so the guards can be tested without a database.
type systemPlaylistStore interface {
	GetOrCreateSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string) (*postgres.Playlist, error)
	AddToSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string, postID uuid.UUID) (bool, error)
	RemoveFromSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string, postID uuid.UUID) (bool, error)
	IsInSystemPlaylist(ctx context.Context, ownerID uuid.UUID, kind string, postID uuid.UUID) (bool, error)
}

// GetSystemPlaylist returns the caller's collection of the given kind,
// creating it on first read.
func (s *Service) GetSystemPlaylist(ctx context.Context, callerID uuid.UUID, kind string) (*postgres.Playlist, error) {
	if s.systemPlaylists == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if !postgres.IsSystemPlaylistKind(kind) {
		return nil, ErrInvalidSystemPlaylistKind
	}
	if callerID == uuid.Nil {
		return nil, ErrNotPlaylistOwner
	}
	return s.systemPlaylists.GetOrCreateSystemPlaylist(ctx, callerID, kind)
}

// WatchLaterResult is the body of POST/DELETE /v1/posts/:id/watch-later.
type WatchLaterResult struct {
	Queued bool `json:"queued"`
}

// AddToWatchLater queues a post the caller may see. A post the caller may
// not see (private, followers-only, deleted, processing) is ErrPostNotFound
// / ErrPostNotVisible — 404 at the route, so the queue can never confirm
// that a hidden post exists.
func (s *Service) AddToWatchLater(ctx context.Context, callerID, postID uuid.UUID) (*WatchLaterResult, error) {
	if s.systemPlaylists == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if _, err := s.loadPostForEngagement(ctx, postID, callerID); err != nil {
		return nil, err
	}
	if _, err := s.systemPlaylists.AddToSystemPlaylist(ctx, callerID, postgres.PlaylistKindWatchLater, postID); err != nil {
		return nil, fmt.Errorf("add to watch later: %w", err)
	}
	return &WatchLaterResult{Queued: true}, nil
}

// RemoveFromWatchLater is the inverse; idempotent, and it does not gate on
// visibility (a post that became private after it was queued can still be
// removed from the queue).
func (s *Service) RemoveFromWatchLater(ctx context.Context, callerID, postID uuid.UUID) (*WatchLaterResult, error) {
	if s.systemPlaylists == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if _, err := s.systemPlaylists.RemoveFromSystemPlaylist(ctx, callerID, postgres.PlaylistKindWatchLater, postID); err != nil {
		return nil, fmt.Errorf("remove from watch later: %w", err)
	}
	return &WatchLaterResult{Queued: false}, nil
}

// viewerQueued answers post detail's viewer_queued; best-effort false.
func (s *Service) viewerQueued(ctx context.Context, viewerID, postID uuid.UUID) bool {
	if s.systemPlaylists == nil || viewerID == uuid.Nil {
		return false
	}
	queued, err := s.systemPlaylists.IsInSystemPlaylist(ctx, viewerID, postgres.PlaylistKindWatchLater, postID)
	return err == nil && queued
}

// syncLikedCollection mirrors a long-video like into the caller's "Loved"
// collection. Reels are skipped: /v1/reels/liked is their list. Best-effort
// — a like is a like whether or not the mirror write lands.
func (s *Service) syncLikedCollection(ctx context.Context, userID uuid.UUID, post *postgres.Post, liked bool) {
	if s.systemPlaylists == nil || post == nil || !isLongVideoContentType(post.ContentType) {
		return
	}
	var err error
	if liked {
		_, err = s.systemPlaylists.AddToSystemPlaylist(ctx, userID, postgres.PlaylistKindLiked, post.ID)
	} else {
		_, err = s.systemPlaylists.RemoveFromSystemPlaylist(ctx, userID, postgres.PlaylistKindLiked, post.ID)
	}
	if err != nil {
		slog.WarnContext(ctx, "liked collection sync skipped", "post_id", post.ID, "err", err)
	}
}

// requireUserPlaylistOwner is requirePlaylistOwner plus the system-kind
// guard: the generic playlist writes (rename, describe, cover, visibility,
// delete, add / move / remove item) refuse a server-owned collection.
func (s *Service) requireUserPlaylistOwner(ctx context.Context, callerID, playlistID uuid.UUID) error {
	if err := s.requirePlaylistOwner(ctx, callerID, playlistID); err != nil {
		return err
	}
	p, err := s.authoringOwners.GetPlaylist(ctx, playlistID)
	if err != nil {
		return fmt.Errorf("lookup playlist kind: %w", err)
	}
	if p != nil && postgres.IsSystemPlaylistKind(p.Kind) {
		return ErrSystemPlaylist
	}
	return nil
}
