package service

import (
	"context"
	"fmt"
	"log/slog"

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

// ListAdminAudit is the passthrough used by GET /v1/dating/admin/audit.
// Filters narrow by actor / target / action; the store applies the
// limit + offset clamps. Acceptance test D in PHASE_0_TEST_PLANS.md
// §P0-8 expects this log to be append-only — the immutability trigger
// lives in database/setup.sql.
func (s *Service) ListAdminAudit(ctx context.Context, f store.AdminAuditFilter, limit, offset int) ([]*store.AdminAuditEntry, error) {
	return s.store.ListAdminAudit(ctx, f, limit, offset)
}

// ActOnReport is the composite admin action: optionally flip the
// reported user's profile_status (restrict / suspend), then record
// the report transition. Both legs are best-effort independent —
// failing one leg shouldn't strand the other. Returns the new
// report status the caller renders back to the admin UI.
//
// adminID is the X-Admin-Id header value forwarded by the gateway;
// uuid.Nil is accepted (we slog.Warn rather than failing the action)
// so the admin click never bounces because of a missing header. The
// audit row goes into dating_admin_audit append-only after the
// action lands. A failed audit insert is logged but does NOT roll
// back the admin action.
//
// Allowed `action` values:
//
//	dismiss   - mark closed_no_action; no profile change
//	resolved  - mark resolved; no profile change (action taken externally)
//	warn      - mark actioned; no profile change (warning issued out-of-band)
//	review    - mark actioned + flag the reported user for manual moderator
//	            inspection (§P1-1 pending_review). Distinct from
//	            pending_photo / pending_selfie which are onboarding gaps —
//	            this state is the "risk score >= 86 / admin_review" bucket
//	            and bars new sparks until a moderator clears it.
//	restrict  - mark actioned + restrict the reported user
//	suspend   - mark actioned + suspend the reported user
//
// targetUserID is required for review + restrict + suspend.
func (s *Service) ActOnReport(ctx context.Context, adminID, reportID, targetUserID uuid.UUID, action string) (string, error) {
	var newStatus string
	var profileStatus string
	switch action {
	case "dismiss":
		newStatus = "closed_no_action"
	case "resolved":
		newStatus = "resolved"
	case "warn":
		newStatus = "actioned"
	case "review":
		newStatus = "actioned"
		profileStatus = store.ProfileStatusPendingReview
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

	if adminID == uuid.Nil {
		slog.Warn("admin audit: actor id missing on ActOnReport",
			"report_id", reportID, "action", action, "target_user_id", targetUserID)
	}
	entry := &store.AdminAuditEntry{
		ActorAdminID:   adminID,
		Action:         "report_" + action,
		TargetUserID:   targetUserID,
		TargetResource: "report:" + reportID.String(),
	}
	if err := s.store.InsertAdminAudit(ctx, entry); err != nil {
		// Audit failure must not roll back the action — see godoc.
		slog.Error("admin audit: insert failed for ActOnReport",
			"report_id", reportID, "action", action,
			"target_user_id", targetUserID, "actor_admin_id", adminID,
			"error", err)
	}
	return newStatus, nil
}

// errInvalidAdminAction is unexported; callers receive it as a
// generic error and the HTTP handler maps the "invalid: " prefix to
// 400 via respondServiceError.
var errInvalidAdminAction = fmt.Errorf("invalid: unknown admin action; allowed values are dismiss|resolved|warn|review|restrict|suspend")
