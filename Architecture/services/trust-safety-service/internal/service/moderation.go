package service

import (
	"context"
	"time"

	"github.com/facebook-like/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type Service struct {
	store *postgres.ReportStore
}

func New(store *postgres.ReportStore) *Service {
	return &Service{store: store}
}

func (s *Service) FileReport(ctx context.Context, reporterID, entityID uuid.UUID, entityType, reason, details string) (*postgres.Report, error) {
	// TODO: Validate entityType (user, post, comment)
	// TODO: Check if already reported (optional for v1)

	report := &postgres.Report{
		ID:         uuid.New(),
		ReporterID: reporterID,
		EntityType: entityType,
		EntityID:   entityID,
		Reason:     reason,
		Details:    details,
		Status:     "open",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.store.CreateReport(ctx, report); err != nil {
		return nil, err
	}

	// TODO: Publish ReportFiled event to Kafka (outbox pattern recommended, but direct for v1 MVP)

	return report, nil
}

func (s *Service) ListReports(ctx context.Context, limit, offset int) ([]postgres.Report, error) {
	return s.store.GetReports(ctx, limit, offset)
}
