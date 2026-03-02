package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/facebook-like/shared/events"
	"github.com/facebook-like/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

var validEntityTypes = map[string]bool{
	"user":    true,
	"post":    true,
	"comment": true,
}

type Service struct {
	store       *postgres.ReportStore
	kafkaWriter *kafka.Writer
}

func New(store *postgres.ReportStore, kafkaWriter *kafka.Writer) *Service {
	return &Service{store: store, kafkaWriter: kafkaWriter}
}

func (s *Service) FileReport(ctx context.Context, reporterID, entityID uuid.UUID, entityType, reason, details string) (*postgres.Report, error) {
	// 1. Validate entityType
	if !validEntityTypes[entityType] {
		return nil, fmt.Errorf("invalid entity_type: %s (must be user, post, or comment)", entityType)
	}

	// 2. Check for duplicate open report
	isDup, err := s.store.CheckDuplicate(ctx, reporterID, entityID)
	if err != nil {
		return nil, fmt.Errorf("duplicate check failed: %w", err)
	}
	if isDup {
		return nil, fmt.Errorf("you have already filed an open report for this entity")
	}

	// 3. Create report
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

	// 4. Publish ReportFiled event to Kafka
	if s.kafkaWriter != nil {
		payload := events.ReportFiledPayload{
			ReportID:   report.ID.String(),
			ReporterID: report.ReporterID.String(),
			EntityType: report.EntityType,
			EntityID:   report.EntityID.String(),
			Reason:     report.Reason,
			CreatedAt:  report.CreatedAt,
		}
		pBytes, _ := json.Marshal(payload)
		reporterStr := report.ReporterID.String()
		envelope := events.NewEnvelope(ctx, events.ReportFiled, &reporterStr, pBytes)
		eBytes, _ := json.Marshal(envelope)
		if err := s.kafkaWriter.WriteMessages(ctx, kafka.Message{
			Key:   []byte(report.EntityID.String()),
			Value: eBytes,
		}); err != nil {
			// Non-fatal: log but don't fail the report
			fmt.Printf("[trust-safety] failed to publish ReportFiled event: %v\n", err)
		}
	}

	return report, nil
}

func (s *Service) ListReports(ctx context.Context, limit, offset int) ([]postgres.Report, error) {
	return s.store.GetReports(ctx, limit, offset)
}
