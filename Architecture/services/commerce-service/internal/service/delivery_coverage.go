package service

import (
	"context"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func (s *Service) DeliveryAreas(ctx context.Context, enabledOnly bool) ([]postgres.DeliveryArea, error) {
	return s.store.DeliveryAreas(ctx, enabledOnly)
}
func (s *Service) SaveDeliveryArea(ctx context.Context, actor uuid.UUID, area postgres.DeliveryArea) (*postgres.DeliveryArea, error) {
	return s.store.SaveDeliveryArea(ctx, actor, area)
}
func (s *Service) DeliveryAreaSellers(ctx context.Context, area uuid.UUID) ([]postgres.DeliveryAreaSeller, error) {
	return s.store.DeliveryAreaSellers(ctx, area)
}
func (s *Service) SaveDeliveryAreaSeller(ctx context.Context, actor uuid.UUID, area postgres.DeliveryAreaSeller) error {
	return s.store.SaveDeliveryAreaSeller(ctx, actor, area)
}
