// Strikes (Copyright Match plan section 6.4, P-6). Issuing and voiding are
// admin-service token routes (admin_token.go) requiring
// trust_safety:strikes.manage; the actor written to trust.admin_audit is
// the token's signed act claim.
//
//	POST /v1/internal/admin/trust/strikes                 issue (201; 200 on an idempotent replay)
//	POST /v1/internal/admin/trust/strikes/:userId/void    void one of the user's strikes
//
// The legacy POST /v1/strikes trusted X-User-Id / X-Scopes behind the
// internal key alone, which the gateway stamps on edge traffic. It is
// retired: it answers 410 STRIKE_ROUTE_RETIRED and writes nothing.
package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CodeStrikeRouteRetired is the legacy issuing route's only answer.
const CodeStrikeRouteRetired = "STRIKE_ROUTE_RETIRED"

type issueStrikeRequest struct {
	UserID      string  `json:"user_id" binding:"required"`
	Reason      string  `json:"reason" binding:"required"`
	Severity    string  `json:"severity" binding:"required"`
	ContentType string  `json:"content_type"`
	ContentID   *string `json:"content_id,omitempty"`
	CaseID      *string `json:"case_id,omitempty"`
	StrikeGroup string  `json:"strike_group"`
	// IdempotencyKey is required (service.IssueStrikeInput).
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

func parseOptionalUUID(raw *string, field string) (*uuid.UUID, string) {
	if raw == nil || *raw == "" {
		return nil, ""
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		return nil, "Invalid " + field
	}
	return &id, ""
}

// IssueStrike — POST /v1/internal/admin/trust/strikes (admin-service token,
// strikes.manage; audited).
func (h *Handler) IssueStrike(c *gin.Context) {
	ctx := c.Request.Context()
	meta, ok := adminAuditMeta(c, "")
	if !ok || !viaAdminToken(c) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil)
		return
	}
	var req issueStrikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user_id", nil)
		return
	}
	contentID, msg := parseOptionalUUID(req.ContentID, "content_id")
	if msg != "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", msg, nil)
		return
	}
	caseID, msg := parseOptionalUUID(req.CaseID, "case_id")
	if msg != "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", msg, nil)
		return
	}
	in := service.IssueStrikeInput{
		UserID: userID, Reason: req.Reason, Severity: req.Severity, ContentType: req.ContentType,
		ContentID: contentID, CaseID: caseID, StrikeGroup: req.StrikeGroup, IdempotencyKey: req.IdempotencyKey,
	}
	// Validated here first so a bad request never reaches a transaction
	// (and so this handler is testable without a database).
	if err := in.Validate(); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	meta.Reason = in.Reason
	strike, created, err := h.svc.IssueStrike(ctx, in, meta)
	if err != nil {
		writeStrikeError(c, "IssueStrike", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	api.JSON(c.Writer, status, strike, nil)
}

type voidStrikeRequest struct {
	StrikeID string `json:"strike_id" binding:"required"`
	Reason   string `json:"reason" binding:"required"`
}

// VoidStrike — POST /v1/internal/admin/trust/strikes/:userId/void
// (admin-service token, strikes.manage; audited). The strike must be the
// user's; a replay on a voided strike is 200 and changes nothing.
func (h *Handler) VoidStrike(c *gin.Context) {
	ctx := c.Request.Context()
	meta, ok := adminAuditMeta(c, "")
	if !ok || !viaAdminToken(c) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil)
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	var req voidStrikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	strikeID, err := uuid.Parse(req.StrikeID)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid strike_id", nil)
		return
	}
	meta.Reason = req.Reason
	strike, changed, err := h.svc.VoidStrike(ctx, userID, strikeID, req.Reason, meta)
	if err != nil {
		writeStrikeError(c, "VoidStrike", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"strike": strike, "changed": changed}, nil)
}

func writeStrikeError(c *gin.Context, op string, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, postgres.ErrStrikeNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Strike not found", nil)
	case errors.Is(err, service.ErrInvalidStrike):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
	case errors.Is(err, postgres.ErrActorRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, codeActorRequired, "A verified admin user is required", nil)
	default:
		slog.ErrorContext(ctx, op+" error", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Strike change failed", nil)
	}
}

// LegacyIssueStrikeRetired answers the retired POST /v1/strikes.
func (h *Handler) LegacyIssueStrikeRetired(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, CodeStrikeRouteRetired,
		"POST /v1/strikes is retired; strikes are issued through the admin-service token route", nil)
}
