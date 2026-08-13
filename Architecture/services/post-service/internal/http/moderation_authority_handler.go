package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/moderationcap"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func hasAnyScope(raw string, wanted ...string) bool {
	set := make(map[string]struct{}, len(wanted))
	for _, scope := range wanted {
		set[scope] = struct{}{}
	}
	for _, scope := range strings.Fields(raw) {
		if _, ok := set[scope]; ok {
			return true
		}
	}
	return false
}

type moderatePostRequest struct {
	DecisionID string  `json:"decision_id" binding:"required"`
	Action     string  `json:"action" binding:"required"`
	Reason     string  `json:"reason" binding:"required"`
	ReportID   *string `json:"report_id,omitempty"`
}

func (h *Handler) ModeratePost(c *gin.Context) {
	if !hasAnyScope(c.GetHeader("X-Scopes"), "moderator", "admin", "superadmin") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Moderator scope required", nil)
		return
	}
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid actor identity", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req moderatePostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	decisionID, err := uuid.Parse(req.DecisionID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DECISION_ID", "decision_id must be a UUID", nil)
		return
	}
	var reportID *uuid.UUID
	if req.ReportID != nil {
		parsed, parseErr := uuid.Parse(*req.ReportID)
		if parseErr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REPORT_ID", "report_id must be a UUID", nil)
			return
		}
		reportID = &parsed
	}
	h.applyModeration(c, postgres.ModeratePostInput{
		DecisionID: decisionID, PostID: postID, ActorID: actorID,
		Action: req.Action, Reason: req.Reason, Source: "admin", SourceRefID: reportID,
	})
}

func (h *Handler) GetModerationSubjectInternal(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	subject, err := h.svc.GetModerationSubject(c.Request.Context(), postID)
	if err != nil {
		// Deliberately non-enumerating to callers: trust-safety only needs to
		// know that the submitted subject is not appealable.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Moderation subject not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, subject, nil)
}

type internalModerationRequest struct {
	Claims     moderationcap.Claims `json:"claims" binding:"required"`
	Capability string               `json:"capability" binding:"required"`
}

func (h *Handler) ModeratePostInternal(c *gin.Context) {
	var req internalModerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if h.moderationVerifier == nil || h.moderationVerifier.Verify(req.Claims, req.Capability) != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Moderation capability rejected", nil)
		return
	}
	postID, err := uuid.Parse(req.Claims.SubjectID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Moderation capability rejected", nil)
		return
	}
	decisionID, err := uuid.Parse(req.Claims.DecisionID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Moderation capability rejected", nil)
		return
	}
	actorID, err := uuid.Parse(req.Claims.ActorID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Moderation capability rejected", nil)
		return
	}
	sourceRef := decisionID
	h.applyModeration(c, postgres.ModeratePostInput{
		DecisionID: decisionID, PostID: postID, ActorID: actorID,
		Action: req.Claims.Decision, Reason: req.Claims.Reason,
		Source: "appeal", SourceRefID: &sourceRef, ExpectedRevision: req.Claims.ContentRevision,
	})
}

func (h *Handler) applyModeration(c *gin.Context, in postgres.ModeratePostInput) {
	decision, err := h.svc.ModeratePost(c.Request.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrModerationDecisionConflict):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "DECISION_CONFLICT", err.Error(), nil)
		case errors.Is(err, postgres.ErrModerationRevision):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "STALE_MODERATION_SUBJECT", err.Error(), nil)
		case errors.Is(err, postgres.ErrModerationTransition):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "INVALID_TRANSITION", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Moderation action failed", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, decision, nil)
}
