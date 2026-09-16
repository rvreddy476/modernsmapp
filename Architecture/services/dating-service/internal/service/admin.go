package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

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

// ListPanicIncidents is the on-call queue: paginated, optionally one status,
// and never coordinates (lane D8).
func (s *Service) ListPanicIncidents(ctx context.Context, status string, limit, offset int) ([]*store.PanicIncidentSummary, error) {
	return s.store.ListPanicIncidents(ctx, status, limit, offset)
}

// maxPanicResolutionNoteChars bounds a resolve note.
const maxPanicResolutionNoteChars = 1000

// GetPanicIncidentForAdmin returns one incident with its full-precision
// location. Every view writes a "panic_viewed" dating_admin_audit row
// FIRST; if the audit cannot be written the coordinates are not returned
// (a location read with no trail never happens).
func (s *Service) GetPanicIncidentForAdmin(ctx context.Context, adminID, incidentID uuid.UUID) (*store.PanicIncident, error) {
	if adminID == uuid.Nil {
		return nil, errAdminActorRequired
	}
	inc, err := s.store.GetPanicIncident(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	if err := s.store.InsertAdminAudit(ctx, &store.AdminAuditEntry{
		ActorAdminID:   adminID,
		Action:         "panic_viewed",
		TargetUserID:   inc.UserID,
		TargetResource: "panic_incident:" + incidentID.String(),
	}); err != nil {
		return nil, fmt.Errorf("audit panic incident view: %w", err)
	}
	return inc, nil
}

// ResolvePanic closes an incident with a note and audits it ("panic_resolved",
// the note in internal_notes). Resolving a resolved incident is a no-op.
func (s *Service) ResolvePanic(ctx context.Context, adminID, incidentID uuid.UUID, note string) (*store.PanicIncident, error) {
	if adminID == uuid.Nil {
		return nil, errAdminActorRequired
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxPanicResolutionNoteChars {
		return nil, fmt.Errorf("invalid: note must be at most %d characters", maxPanicResolutionNoteChars)
	}
	inc, resolved, err := s.store.ResolvePanicIncident(ctx, incidentID, adminID, note)
	if err != nil {
		if errors.Is(err, store.ErrPanicAlreadyResolved) {
			return inc, nil
		}
		return nil, err
	}
	if !resolved {
		return inc, nil
	}
	if aerr := s.store.InsertAdminAudit(ctx, &store.AdminAuditEntry{
		ActorAdminID:   adminID,
		Action:         "panic_resolved",
		TargetUserID:   inc.UserID,
		TargetResource: "panic_incident:" + incidentID.String(),
		InternalNotes:  note,
	}); aerr != nil {
		slog.Error("admin audit: insert failed for ResolvePanic",
			"panic_id", incidentID, "actor_admin_id", adminID, "error", aerr)
	}
	return inc, nil
}

// ListPendingPhotos returns photos awaiting moderation, oldest-first.
func (s *Service) ListPendingPhotos(ctx context.Context, limit int) ([]*store.Photo, error) {
	return s.store.ListPendingPhotos(ctx, limit)
}

// AdminStats is the passthrough used by GET /v1/dating/internal/admin/stats.
func (s *Service) AdminStats(ctx context.Context) (*store.AdminStats, error) {
	return s.store.AdminStats(ctx)
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
// adminID is the admin's gateway-derived user id (the HTTP layer's
// requireAdmin). uuid.Nil is refused before anything changes: an admin
// action with no accountable actor never lands. The audit row goes into
// dating_admin_audit append-only after the action lands. A failed audit
// insert is logged but does NOT roll back the admin action.
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
//	reinstate - mark resolved + lift a review / restrict / suspend hold,
//	            restoring the user's remembered onboarding step (or
//	            'paused' if they had paused) — never a skipped step
//
// targetUserID is optional: every action applies to the report's own
// target, and a targetUserID naming anyone else is refused with
// ErrReportTargetMismatch before anything changes (lane D8 — a report id
// can never be used to act on a different user). Every profile change goes
// through store.TransitionProfileStatus as the admin actor; a refused edge
// (e.g. reinstating a profile that is not held) fails the action before the
// report changes.
func (s *Service) ActOnReport(ctx context.Context, adminID, reportID, targetUserID uuid.UUID, action string) (string, error) {
	if adminID == uuid.Nil {
		return "", errAdminActorRequired
	}
	var newStatus string
	var profileEvent store.ProfileEvent
	switch action {
	case "dismiss":
		newStatus = "closed_no_action"
	case "resolved":
		newStatus = "resolved"
	case "warn":
		newStatus = "actioned"
	case "review":
		newStatus = "actioned"
		profileEvent = store.ProfileEventReview
	case "restrict":
		newStatus = "actioned"
		profileEvent = store.ProfileEventRestrict
	case "suspend":
		newStatus = "actioned"
		profileEvent = store.ProfileEventSuspend
	case "reinstate":
		newStatus = "resolved"
		profileEvent = store.ProfileEventReinstate
	default:
		return "", errInvalidAdminAction
	}

	report, err := s.store.GetReportByID(ctx, reportID)
	if err != nil {
		return "", err
	}
	if targetUserID != uuid.Nil && targetUserID != report.TargetID {
		return "", ErrReportTargetMismatch
	}
	targetUserID = report.TargetID

	if profileEvent != "" {
		if _, err := s.store.TransitionProfileStatus(ctx, targetUserID, profileEvent, store.ProfileActorAdmin); err != nil {
			return "", err
		}
		s.InvalidatePulseCache(ctx, targetUserID)
		s.InvalidateDecksForCandidate(ctx, targetUserID)
	}

	if err := s.store.SetReportStatus(ctx, reportID, newStatus); err != nil {
		return "", err
	}

	// Phase 1 notification fanout — let the reporter know their report
	// has been resolved/actioned/dismissed (dating.report.status_updated).
	// We need the reporter_id to scope the user-facing notification, so
	// re-read the row. The store layer enforces SetReportStatus has
	// landed; a missing row at this point is a race we log + skip.
	// A reporter purged since filing holds a subject token, not an account:
	// nobody to notify.
	if s.producer != nil && !report.ReporterAnonymised {
		if perr := s.producer.PublishReportStatusUpdated(ctx, reportID, report.ReporterID, newStatus); perr != nil {
			slog.Warn("publish report.status_updated failed",
				"report_id", reportID, "status", newStatus, "error", perr)
		}
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
var errInvalidAdminAction = fmt.Errorf("invalid: unknown admin action; allowed values are dismiss|resolved|warn|review|restrict|suspend|reinstate")

// errAdminActorRequired refuses an admin mutation with no actor. The
// "forbidden: " prefix maps to 403 in respondServiceError.
var errAdminActorRequired = fmt.Errorf("forbidden: admin actor required")
