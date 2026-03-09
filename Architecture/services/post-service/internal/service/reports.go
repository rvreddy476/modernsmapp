package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// SubmitReport creates a new content report from a user.
func (s *Service) SubmitReport(ctx context.Context, report *postgres.ContentReport) error {
	return s.pgStore.InsertContentReport(ctx, report)
}

// ListReports returns content reports, optionally filtered by status. Used by admin dashboard.
func (s *Service) ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.ContentReport, error) {
	return s.pgStore.GetContentReports(ctx, status, limit, offset)
}

// ReviewReport updates the status and review note of a report. Used by admin dashboard.
func (s *Service) ReviewReport(ctx context.Context, reportID uuid.UUID, status, reviewerID, reviewNote string) error {
	return s.pgStore.UpdateReportStatus(ctx, reportID, status, reviewerID, reviewNote)
}
