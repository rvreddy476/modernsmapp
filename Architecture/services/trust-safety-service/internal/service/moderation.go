package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

var validEntityTypes = map[string]bool{
	"user":    true,
	"post":    true,
	"comment": true,
}

// validReportCategories is the report category/reason allowlist from
// messaging/privacy spec §10.5 — exactly these 12 values are accepted. The
// category is stored in trust.reports.reason and enforced at the DB layer by
// the reports_reason_check CHECK constraint (migration 005).
var validReportCategories = map[string]bool{
	"spam":                  true,
	"harassment":            true,
	"scam_fraud":            true,
	"sexual_content":        true,
	"hate_abuse":            true,
	"impersonation":         true,
	"child_safety":          true,
	"violence_threat":       true,
	"self_harm":             true,
	"misinformation":        true,
	"intellectual_property": true,
	"other":                 true,
}

var validTransitions = map[string][]string{
	"open":      {"reviewing"},
	"reviewing": {"resolved", "dismissed"},
	"resolved":  {"dismissed"},
	"dismissed": {},
}

type Service struct {
	store       *postgres.ReportStore
	extras      *postgres.TrustExtrasStore
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

	// 1b. Validate report category against the spec §10.5 allowlist.
	if !validReportCategories[reason] {
		return nil, fmt.Errorf("invalid reason: %s (must be one of the 12 spec report categories)", reason)
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

func (s *Service) GetReport(ctx context.Context, reportIDStr string) (*postgres.Report, error) {
	reportID, err := uuid.Parse(reportIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid report ID")
	}
	return s.store.GetReport(ctx, reportID)
}

func (s *Service) UpdateReport(ctx context.Context, actorID, reportIDStr, newStatus string, assignedTo *uuid.UUID, resolutionNotes string) (*postgres.Report, error) {
	reportID, err := uuid.Parse(reportIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid report ID")
	}

	current, err := s.store.GetReport(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("report not found: %w", err)
	}

	allowed := validTransitions[current.Status]
	valid := false
	for _, st := range allowed {
		if st == newStatus {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("invalid transition from %s to %s", current.Status, newStatus)
	}

	report, err := s.store.UpdateReport(ctx, reportID, newStatus, assignedTo, resolutionNotes)
	if err != nil {
		return nil, err
	}

	// Publish event for terminal statuses
	if (newStatus == "resolved" || newStatus == "dismissed") && s.kafkaWriter != nil {
		eventType := events.ReportResolved
		if newStatus == "dismissed" {
			eventType = events.ReportDismissed
		}
		payload := map[string]interface{}{
			"report_id":   report.ID.String(),
			"entity_type": report.EntityType,
			"entity_id":   report.EntityID.String(),
			"status":      newStatus,
			"actor_id":    actorID,
		}
		pBytes, _ := json.Marshal(payload)
		actor := actorID
		envelope := events.NewEnvelope(ctx, eventType, &actor, pBytes)
		eBytes, _ := json.Marshal(envelope)
		_ = s.kafkaWriter.WriteMessages(ctx, kafka.Message{
			Key:   []byte(report.ID.String()),
			Value: eBytes,
		})
	}

	return report, nil
}
