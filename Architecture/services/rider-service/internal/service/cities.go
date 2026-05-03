package service

import (
	"context"

	"github.com/atpost/rider-service/internal/store"
)

// ListCities returns active cities. Public surface (no user-id required).
func (s *Service) ListCities(ctx context.Context) ([]store.City, error) {
	return s.store.ListActiveCities(ctx)
}
