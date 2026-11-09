package service

import (
	"context"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ListPartnerOrdersPage is the restaurant owner's order list, filtered by
// status and paged by cursor (food server lane, 9 Nov 2026).
func (s *Service) ListPartnerOrdersPage(ctx context.Context, ownerID, restaurantID uuid.UUID, f postgres.PartnerOrderFilter) ([]postgres.Order, *postgres.OrderCursor, error) {
	return s.store.ListPartnerOrdersPage(ctx, ownerID, restaurantID, f)
}
