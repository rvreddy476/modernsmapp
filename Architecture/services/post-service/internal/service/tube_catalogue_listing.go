package service

import (
	"context"
	"errors"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tube catalogue listing (MTube search, 2026-09-27): the two keyset walks
// search-service's tube reindex reads. Thin on purpose — the rows go out as
// the store holds them; the HTTP layer (internal/http/channels_internal_list.go)
// decides what a non-public playlist discloses.

// ErrCatalogueStoreUnavailable: the service was built without a Postgres
// store (tests), so there is nothing to walk.
var ErrCatalogueStoreUnavailable = errors.New("catalogue store not configured")

// ListChannelsAfter is Store.ListChannelsAfter.
func (s *Service) ListChannelsAfter(ctx context.Context, after uuid.UUID, limit int) ([]postgres.Channel, error) {
	if s.pgStore == nil {
		return nil, ErrCatalogueStoreUnavailable
	}
	return s.pgStore.ListChannelsAfter(ctx, after, limit)
}

// ListPlaylistsAfter is Store.ListPlaylistsAfter (every visibility).
func (s *Service) ListPlaylistsAfter(ctx context.Context, after uuid.UUID, limit int) ([]postgres.Playlist, error) {
	if s.pgStore == nil {
		return nil, ErrCatalogueStoreUnavailable
	}
	return s.pgStore.ListPlaylistsAfter(ctx, after, limit)
}
