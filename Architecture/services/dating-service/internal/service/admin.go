package service

import (
	"context"
	"fmt"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
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

// ActOnReport is the composite admin action: optionally flip the
// reported user's profile_status (restrict / suspend), then record
// the report transition. Both legs are best-effort independent —
// failing one leg shouldn't strand the other. Returns the new
// report status the caller renders back to the admin UI.
//
// Allowed `action` values:
//
//	dismiss   - mark closed_no_action; no profile change
//	resolved  - mark resolved; no profile change (action taken externally)
//	warn      - mark actioned; no profile change (warning issued out-of-band)
//	restrict  - mark actioned + restrict the reported user
//	suspend   - mark actioned + suspend the reported user
//
// targetUserID is required for restrict + suspend.
func (s *Service) ActOnReport(ctx context.Context, reportID, targetUserID uuid.UUID, action string) (string, error) {
	var newStatus string
	var profileStatus string
	switch action {
	case "dismiss":
		newStatus = "closed_no_action"
	case "resolved":
		newStatus = "resolved"
	case "warn":
		newStatus = "actioned"
	case "restrict":
		newStatus = "actioned"
		profileStatus = store.ProfileStatusRestricted
	case "suspend":
		newStatus = "actioned"
		profileStatus = store.ProfileStatusSuspended
	default:
		return "", errInvalidAdminAction
	}

	if profileStatus != "" {
		if targetUserID == uuid.Nil {
			return "", errInvalidAdminAction
		}
		if _, err := s.store.SetProfileStatus(ctx, targetUserID, profileStatus); err != nil {
			return "", err
		}
		s.InvalidatePulseCache(ctx, targetUserID)
		s.InvalidateDecksForCandidate(ctx, targetUserID)
	}

	if err := s.store.SetReportStatus(ctx, reportID, newStatus); err != nil {
		return "", err
	}
	return newStatus, nil
}

// errInvalidAdminAction is unexported; callers receive it as a
// generic error and the HTTP handler maps the "invalid: " prefix to
// 400 via respondServiceError.
var errInvalidAdminAction = fmt.Errorf("invalid: unknown admin action; allowed values are dismiss|resolved|warn|restrict|suspend")
