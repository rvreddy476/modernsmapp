package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// GenerateSettlementFile builds the CSV for the requested kind +
// period and inserts the audit row in food.settlement_files. `kind`
// is 'restaurant' or 'delivery'.
func (s *Service) GenerateSettlementFile(ctx context.Context, adminID uuid.UUID, kind string, from, to time.Time) (*postgres.SettlementFile, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("invalid: to before from")
	}
	switch kind {
	case "restaurant":
		return s.store.GenerateRestaurantSettlementFile(ctx, adminID, from, to)
	case "delivery":
		return s.store.GenerateDeliverySettlementFile(ctx, adminID, from, to)
	default:
		return nil, fmt.Errorf("invalid kind: %s", kind)
	}
}

// ListSettlementFiles returns the audit log of generated files.
func (s *Service) ListSettlementFiles(ctx context.Context, limit int) ([]postgres.SettlementFile, error) {
	return s.store.ListSettlementFiles(ctx, limit)
}

// GetSettlementFileBody streams the CSV body for the download handler.
func (s *Service) GetSettlementFileBody(ctx context.Context, fileID uuid.UUID) ([]byte, string, error) {
	return s.store.GetSettlementFileBody(ctx, fileID)
}
