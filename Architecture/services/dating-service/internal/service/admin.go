package service

import (
	"context"

	"github.com/atpost/dating-service/internal/store"
)

// Admin read-side service wrappers for the /admin/dating console
// (PRODUCTION_GAP_ANALYSIS.md §P0-8). The store does the real work;
// these wrappers exist so the HTTP layer doesn't touch the store
// directly, matching the rest of the service.

// ListReports returns dating_reports for the admin console with
// optional status + category filters.
func (s *Service) ListReports(ctx context.Context, status, category string, limit, offset int) ([]*store.Report, error) {
	return s.store.ListReports(ctx, status, category, limit, offset)
}

// ListPanicEvents returns recent dating_safety_events of kind 'panic'
// across all users for the on-call queue.
func (s *Service) ListPanicEvents(ctx context.Context, limit int) ([]*store.SafetyEvent, error) {
	return s.store.ListPanicEvents(ctx, limit)
}

// ListPendingPhotos returns photos awaiting moderation, oldest-first.
func (s *Service) ListPendingPhotos(ctx context.Context, limit int) ([]*store.Photo, error) {
	return s.store.ListPendingPhotos(ctx, limit)
}
