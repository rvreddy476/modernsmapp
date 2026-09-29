// Copyright case holds (Copyright Match plan, sections 6.4 "restriction
// commands", 9.2). Admin-service token routes (admin_token.go) requiring
// trust_safety:copyright.act; the actor written to trust.admin_audit and
// signed into the restriction command is the token's act claim.
//
//	POST /v1/internal/admin/trust/copyright/cases              open a case shell and place its hold (201)
//	GET  /v1/internal/admin/trust/copyright/cases/:id          the case and its enforcement state
//	POST /v1/internal/admin/trust/copyright/cases/:id/place    re-place a released hold
//	POST /v1/internal/admin/trust/copyright/cases/:id/release  release an active hold
//
// Step-up: the service token carries no step-up evidence (shared
// servicetoken has no such claim), so a fresh two-factor check is
// admin-service's gate to enforce before it mints the token — the same
// way the strike routes are protected. admin-service must register these
// four routes with stepUp: true (handler_trust.go); until it does, no
// token reaches them at all, because trust_safety:copyright.act is not
// yet in identity's catalogue.
//
// No user-facing route exists: parties learn of a hold from the case
// pipeline, never from here.
package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Error codes the copyright routes answer with.
const (
	CodeCopyrightReasonInvalid = "INVALID_REASON_CODE"
	CodeCopyrightNotFound      = "CASE_NOT_FOUND"
	CodeCopyrightTransition    = "INVALID_TRANSITION"
	CodeCopyrightUnavailable   = "COPYRIGHT_UNAVAILABLE"
	CodeCopyrightCaseExists    = "CASE_EXISTS"
)

// copyrightReasonAllowed is moderationcap's closed action / reason table.
func copyrightReasonAllowed(action, reason string) bool {
	return moderationcap.RestrictionReasonAllowed(action, strings.TrimSpace(reason))
}

type createCopyrightCaseRequest struct {
	SubjectPostID   string `json:"subject_post_id" binding:"required"`
	SubjectAuthorID string `json:"subject_author_id" binding:"required"`
	ReasonCode      string `json:"reason_code" binding:"required"`
	// CaseID is optional: the console may pin one per click so a replay
	// fails on the primary key instead of opening a second case.
	CaseID string `json:"case_id"`
}

type copyrightTransitionRequest struct {
	ReasonCode string `json:"reason_code" binding:"required"`
}

// CreateCopyrightCase — POST /v1/internal/admin/trust/copyright/cases.
func (h *Handler) CreateCopyrightCase(c *gin.Context) {
	ctx := c.Request.Context()
	meta, ok := adminAuditMeta(c, "")
	if !ok || !viaAdminToken(c) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil)
		return
	}
	var req createCopyrightCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	postID, err := uuid.Parse(strings.TrimSpace(req.SubjectPostID))
	if err != nil || postID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid subject_post_id", nil)
		return
	}
	authorID, err := uuid.Parse(strings.TrimSpace(req.SubjectAuthorID))
	if err != nil || authorID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid subject_author_id", nil)
		return
	}
	var caseID uuid.UUID
	if raw := strings.TrimSpace(req.CaseID); raw != "" {
		if caseID, err = uuid.Parse(raw); err != nil || caseID == uuid.Nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid case_id", nil)
			return
		}
	}
	// The reason table is closed (moderationcap): refused before any
	// transaction, so the handler is testable without a database.
	if !copyrightReasonAllowed(postgres.RestrictionActionPlaceHold, req.ReasonCode) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeCopyrightReasonInvalid,
			"reason_code is not allowed for place_hold", gin.H{"action": postgres.RestrictionActionPlaceHold})
		return
	}
	hold, err := h.svc.CreateCopyrightHold(ctx, service.CreateCopyrightHoldInput{
		SubjectPostID: postID, SubjectAuthorID: authorID, ReasonCode: req.ReasonCode, CaseID: caseID,
	}, meta)
	if err != nil {
		writeCopyrightError(c, "CreateCopyrightCase", err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, hold, nil)
}

// GetCopyrightCase — GET /v1/internal/admin/trust/copyright/cases/:id.
func (h *Handler) GetCopyrightCase(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid case ID", nil)
		return
	}
	hold, err := h.svc.GetCopyrightHold(ctx, id)
	if err != nil {
		writeCopyrightError(c, "GetCopyrightCase", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, hold, nil)
}

// PlaceCopyrightHold — POST /v1/internal/admin/trust/copyright/cases/:id/place.
func (h *Handler) PlaceCopyrightHold(c *gin.Context) {
	h.copyrightTransition(c, postgres.RestrictionActionPlaceHold)
}

// ReleaseCopyrightHold — POST /v1/internal/admin/trust/copyright/cases/:id/release.
func (h *Handler) ReleaseCopyrightHold(c *gin.Context) {
	h.copyrightTransition(c, postgres.RestrictionActionReleaseHold)
}

func (h *Handler) copyrightTransition(c *gin.Context, action string) {
	ctx := c.Request.Context()
	meta, ok := adminAuditMeta(c, "")
	if !ok || !viaAdminToken(c) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid case ID", nil)
		return
	}
	var req copyrightTransitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if !copyrightReasonAllowed(action, req.ReasonCode) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeCopyrightReasonInvalid,
			"reason_code is not allowed for "+action, gin.H{"action": action})
		return
	}
	var hold *service.CopyrightHold
	if action == postgres.RestrictionActionPlaceHold {
		hold, err = h.svc.PlaceHold(ctx, id, req.ReasonCode, meta)
	} else {
		hold, err = h.svc.ReleaseHold(ctx, id, req.ReasonCode, meta)
	}
	if err != nil {
		writeCopyrightError(c, "CopyrightTransition", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, hold, nil)
}

func writeCopyrightError(c *gin.Context, op string, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, postgres.ErrCopyrightCaseNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeCopyrightNotFound, "Copyright case not found", nil)
	case errors.Is(err, postgres.ErrInvalidTransition):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeCopyrightTransition, err.Error(), nil)
	case errors.Is(err, service.ErrInvalidCopyrightReason):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeCopyrightReasonInvalid, err.Error(), nil)
	case errors.Is(err, service.ErrInvalidCopyrightCase), errors.Is(err, postgres.ErrCopyrightCaseInvalidInput):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
	case errors.Is(err, postgres.ErrActorRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, codeActorRequired, "A verified admin user is required", nil)
	case errors.Is(err, service.ErrCopyrightUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeCopyrightUnavailable, "Copyright cases are not configured", nil)
	case errors.Is(err, postgres.ErrCopyrightCaseExists):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeCopyrightCaseExists, "A case with this id already exists", nil)
	default:
		slog.ErrorContext(ctx, op+" error", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Copyright case change failed", nil)
	}
}
