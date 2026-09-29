package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Appeals (Copyright Match plan section 6.3, P-3) ─────────────────────────

// appealsAPI answers the appeal routes; svc by default, a fake in tests.
type appealsAPI interface {
	SubmitAppeal(ctx context.Context, userID uuid.UUID, contentType, contentID, reason string, decisionID *uuid.UUID) (*postgres.ContentAppeal, error)
	ReviewAppeal(ctx context.Context, id uuid.UUID, status, note string, meta postgres.AuditMeta) error
}

// Appeal error codes. Each service error maps to exactly one.
const (
	CodeAppealInvalid          = "VALIDATION_ERROR"                  // 422
	CodeAppealNotAppealable    = "NOT_APPEALABLE"                    // 409
	CodeAppealActiveExists     = "ACTIVE_APPEAL_EXISTS"              // 409
	CodeAppealCopyrightCase    = "COPYRIGHT_CASE_USE_COUNTER_NOTICE" // 409
	CodeAppealDecisionStale    = "APPEAL_DECISION_STALE"             // 409
	CodeAppealTransition       = "APPEAL_TRANSITION"                 // 409
	CodeAppealSuperseded       = "APPEAL_SUPERSEDED"                 // 409
	CodeAppealSubjectChanged   = "APPEAL_SUBJECT_CHANGED"            // 409
	CodeAppealDecisionConflict = "DECISION_CONFLICT"                 // 409
	CodeAppealOverturnPending  = "OVERTURN_PENDING"                  // 503
	CodeAppealOverturnInFlight = "OVERTURN_IN_FLIGHT"                // 409
	CodeAppealsUnavailable     = "APPEALS_UNAVAILABLE"               // 503
	CodeAppealNotFound         = "NOT_FOUND"                         // 404
)

type submitAppealRequest struct {
	ContentType  string `json:"content_type" binding:"required"`
	ContentID    string `json:"content_id" binding:"required"`
	AppealReason string `json:"appeal_reason" binding:"required"`
	// DecisionID, when given, is the decision the author is appealing; the
	// submission is refused when it is no longer the post's current one.
	DecisionID *string `json:"decision_id,omitempty"`
}

func (h *Handler) SubmitAppeal(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || userID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req submitAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	var decisionID *uuid.UUID
	if req.DecisionID != nil {
		id, err := uuid.Parse(*req.DecisionID)
		if err != nil || id == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, CodeAppealInvalid, "decision_id must be a UUID", nil)
			return
		}
		decisionID = &id
	}
	appeal, err := h.appeals.SubmitAppeal(c.Request.Context(), userID, req.ContentType, req.ContentID, req.AppealReason, decisionID)
	if err != nil {
		writeAppealError(c, "SubmitAppeal", err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, appeal, nil)
}

func (h *Handler) AdminListAppeals(c *gin.Context) {
	// ?mine=true is a user's own list; it has no meaning on the admin token path.
	if c.Query("mine") == "true" && !viaAdminToken(c) {
		userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
			return
		}
		appeals, err := h.svc.ListUserAppeals(c.Request.Context(), userID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, CodeAppealsUnavailable, "Appeals are temporarily unavailable", nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": appeals}, nil)
		return
	}
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	status := c.Query("status")
	limit, offset := paginate(c)
	appeals, err := h.svc.ListAppeals(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.Error("ListAppeals", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list appeals", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": appeals}, nil)
}

type reviewAppealRequest struct {
	Status string `json:"status" binding:"required"`
	Note   string `json:"note"`
}

func (h *Handler) ReviewAppeal(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	meta, ok := adminAuditMeta(c, "")
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, codeActorRequired, "A verified admin user is required", nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid appeal ID", nil)
		return
	}
	var req reviewAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	meta.Reason = req.Note
	if err := h.appeals.ReviewAppeal(c.Request.Context(), id, req.Status, req.Note, meta); err != nil {
		writeAppealError(c, "ReviewAppeal", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// writeAppealError maps an appeal failure to its one response. A refusal
// that closed the appeal (superseded) or left it in flight (pending) says
// so, because the console must show the state the appeal now has.
func writeAppealError(c *gin.Context, op string, err error) {
	ctx := c.Request.Context()
	write := func(status int, code, msg string) {
		api.ErrorWithContext(ctx, c.Writer, status, code, msg, nil)
	}
	switch {
	case errors.Is(err, service.ErrAppealInvalid):
		write(http.StatusUnprocessableEntity, CodeAppealInvalid, err.Error())
	case errors.Is(err, postgres.ErrActorRequired):
		write(http.StatusBadRequest, codeActorRequired, "A verified admin user is required")
	case errors.Is(err, service.ErrAppealNotFound):
		write(http.StatusNotFound, CodeAppealNotFound, "Appeal not found")
	case errors.Is(err, postgres.ErrActiveAppealExists):
		write(http.StatusConflict, CodeAppealActiveExists, "An active appeal already exists")
	case errors.Is(err, service.ErrAppealCopyrightCase):
		write(http.StatusConflict, CodeAppealCopyrightCase, "This decision is a copyright case; submit a counter-notice instead")
	case errors.Is(err, service.ErrAppealDecisionStale):
		write(http.StatusConflict, CodeAppealDecisionStale, "The decision appealed is no longer the post's current decision")
	case errors.Is(err, service.ErrAppealNotEligible):
		write(http.StatusConflict, CodeAppealNotAppealable, "Content is not eligible for appeal")
	case errors.Is(err, service.ErrAppealSuperseded):
		write(http.StatusConflict, CodeAppealSuperseded, "A later decision on the post superseded this appeal; it is closed as superseded")
	case errors.Is(err, service.ErrAppealSubjectChanged):
		write(http.StatusConflict, CodeAppealSubjectChanged, "The post changed since the appeal was submitted; the overturn was refused and the appeal stays open")
	case errors.Is(err, service.ErrPostDecisionConflict):
		write(http.StatusConflict, CodeAppealDecisionConflict, "The overturn's decision id was already used with different claims; the appeal stays in overturning")
	case errors.Is(err, service.ErrAppealOverturnInFlight):
		write(http.StatusConflict, CodeAppealOverturnInFlight, "Another adjudicator's overturn is in flight; it will be confirmed or retried shortly")
	case errors.Is(err, service.ErrAppealTransition):
		write(http.StatusConflict, CodeAppealTransition, err.Error())
	case errors.Is(err, service.ErrAppealOverturnPending):
		slog.Warn(op+" canonical overturn pending", "error", err)
		write(http.StatusServiceUnavailable, CodeAppealOverturnPending, "The overturn could not be confirmed with post-service; it stays in flight and will be retried")
	case errors.Is(err, service.ErrAppealsUnavailable):
		slog.Error(op+" unavailable", "error", err)
		write(http.StatusServiceUnavailable, CodeAppealsUnavailable, "Appeals are temporarily unavailable")
	default:
		slog.Error(op+" error", "error", err)
		write(http.StatusInternalServerError, "INTERNAL_ERROR", "Appeal request failed")
	}
}
