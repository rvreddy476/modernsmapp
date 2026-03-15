package service

import (
	"context"
	"fmt"

	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// RequestDataExport creates a new data export request for a user.
// If the user already has a pending/processing request, it returns the existing one.
func (s *Service) RequestDataExport(ctx context.Context, userID uuid.UUID) (*postgres.DataExportRequest, error) {
	// Check for an existing active request
	existing, err := s.store.GetLatestExportRequest(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing export: %w", err)
	}
	if existing != nil && (existing.Status == "queued" || existing.Status == "processing") {
		return existing, nil
	}

	req, err := s.store.CreateExportRequest(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to create export request: %w", err)
	}
	return req, nil
}

// GetDataExportStatus returns the status of a specific export request, verifying ownership.
func (s *Service) GetDataExportStatus(ctx context.Context, id, userID uuid.UUID) (*postgres.DataExportRequest, error) {
	req, err := s.store.GetExportRequest(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get export request: %w", err)
	}
	if req == nil {
		return nil, nil
	}
	if req.UserID != userID {
		return nil, fmt.Errorf("NOT_FOUND")
	}
	return req, nil
}
