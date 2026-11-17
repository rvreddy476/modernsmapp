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

// ModeratePost (POST /v1/posts/:postId/moderation) was RETIRED (security
// sweep #12, 21 Oct 2026): it granted moderator rights from the X-Scopes
// header, which only the gateway's strip-and-restamp kept honest. The admin
// console's decision write is AdminModeratePost (admin_token.go), judged by a
// signed admin-service token; trust-safety's is ModeratePostInternal below.

// moderatePost is the decision write behind the admin-service token route.
// allow, when set, is asked about the action before anything is written and
// answers the refusal itself.
func (h *Handler) moderatePost(c *gin.Context, actorID uuid.UUID, allow func(c *gin.Context, postID uuid.UUID, action string) bool) {
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
	if allow != nil && !allow(c, postID, req.Action) {
		return
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
	reader := h.restrictionStore()
	if reader == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Moderation subject reader not configured", nil)
		return
	}
	subject, err := reader.GetModerationSubject(c.Request.Context(), postID)
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
	in, ok := internalModerationInput(req.Claims)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Moderation capability rejected", nil)
		return
	}
	h.applyModeration(c, in)
}

// reportPolicyPrefix marks a signed decision as an upheld user report
// (policy_version "report-v1"): trust-safety's report resolution. Any other
// policy version is an appeal outcome ("appeal-v1"), as before.
const reportPolicyPrefix = "report-"

// internalModerationInput turns verified capability claims into the decision
// write. A report decision is recorded with source "report" and the report
// id (the claims' decision_id: one takedown per report, so a replay of the
// same resolution is the same decision) as its source_ref_id; the store
// refuses anything but "reject" from that source. false = a malformed id.
func internalModerationInput(cl moderationcap.Claims) (postgres.ModeratePostInput, bool) {
	postID, err := uuid.Parse(cl.SubjectID)
	if err != nil {
		return postgres.ModeratePostInput{}, false
	}
	decisionID, err := uuid.Parse(cl.DecisionID)
	if err != nil {
		return postgres.ModeratePostInput{}, false
	}
	actorID, err := uuid.Parse(cl.ActorID)
	if err != nil {
		return postgres.ModeratePostInput{}, false
	}
	source := "appeal"
	if strings.HasPrefix(strings.TrimSpace(cl.PolicyVersion), reportPolicyPrefix) {
		source = "report"
	}
	sourceRef := decisionID
	return postgres.ModeratePostInput{
		DecisionID: decisionID, PostID: postID, ActorID: actorID,
		Action: cl.Decision, Reason: cl.Reason,
		Source: source, SourceRefID: &sourceRef, ExpectedRevision: cl.ContentRevision,
	}, true
}

func (h *Handler) applyModeration(c *gin.Context, in postgres.ModeratePostInput) {
	decision, err := h.svc.ModeratePost(c.Request.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrModerationDecisionConflict):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "DECISION_CONFLICT", err.Error(), nil)
		case errors.Is(err, postgres.ErrModerationRevision):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "STALE_MODERATION_SUBJECT", err.Error(), nil)
		case errors.Is(err, postgres.ErrModerationInput):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DECISION", err.Error(), nil)
		case errors.Is(err, postgres.ErrModerationTransition):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "INVALID_TRANSITION", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Moderation action failed", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, decision, nil)
}
