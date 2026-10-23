package http

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	The Action button (pinned contract 23 Oct 2026; service/action_links.go).

	GET  /v1/posts/action-labels             public, cacheable: [{value,label}]
	GET  /v1/links/:id                       public: {id,url,domain,label,status};
	                                         404 unless active; counts the tap
	                                         (once per viewer / IP+UA per hour)
	GET  /v1/posts/:postId/action/stats      author: {clicks_total, clicks_7d}
	GET  /v1/channels/me/action/stats        the caller's profile button, same shape

	Writes ride the existing routes: POST /v1/posts, PATCH /v1/posts/:id,
	reel drafts (PATCH /v1/drafts/:id), composer drafts (payload.action) and
	PATCH /v1/channels/me, all as "action": {"label","url"} — null removes
	on the PATCH-style writes, absent leaves it.

	Moderation (admin-service tokens only, social/tube reports.act):
	GET  /v1/posts/internal/admin/action-links?status=under_review|blocked&cursor&limit
	POST /v1/posts/internal/admin/action-links/:linkId/review  {"decision":"restore"|"block","note"}

	Errors: 400 INVALID_ACTION_LABEL, INVALID_ACTION_URL,
	ACTION_LINK_SHORTENER, ACTION_LINK_GROUP_INVITE, ACTION_LINK_UNSAFE;
	403 ACTION_NOT_ELIGIBLE {details:{missing:[account_age, email_verified,
	no_active_strikes]}}; 503 ACTION_LINK_CHECK_UNAVAILABLE.
*/

// actionLabelsCacheControl: the enum changes only with a deploy.
const actionLabelsCacheControl = "public, max-age=3600"

// ListActionLabels — GET /v1/posts/action-labels.
func (h *Handler) ListActionLabels(c *gin.Context) {
	c.Header("Cache-Control", actionLabelsCacheControl)
	api.JSON(c.Writer, http.StatusOK, actionlinks.Labels, nil)
}

// writeActionError answers an Action-button refusal; false when err is
// none of them.
func writeActionError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidActionShape):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", service.ErrInvalidActionShape.Error(), nil)
	case errors.Is(err, service.ErrInvalidActionLabel):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ACTION_LABEL", service.ErrInvalidActionLabel.Error(), nil)
	case errors.Is(err, actionlinks.ErrInvalidURL):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ACTION_URL", actionlinks.ErrInvalidURL.Error(), nil)
	case errors.Is(err, actionlinks.ErrShortener):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "ACTION_LINK_SHORTENER", actionlinks.ErrShortener.Error(), nil)
	case errors.Is(err, actionlinks.ErrGroupInvite):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "ACTION_LINK_GROUP_INVITE", actionlinks.ErrGroupInvite.Error(), nil)
	case errors.Is(err, service.ErrActionLinkUnsafe):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "ACTION_LINK_UNSAFE", service.ErrActionLinkUnsafe.Error(), nil)
	case errors.Is(err, service.ErrActionLinkCheckUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "ACTION_LINK_CHECK_UNAVAILABLE", service.ErrActionLinkCheckUnavailable.Error(), nil)
	case errors.Is(err, service.ErrActionNotEligible):
		details := gin.H{"missing": []string{}}
		var e *service.ActionNotEligibleError
		if errors.As(err, &e) {
			details["missing"] = e.Missing
			if e.EligibleAt != nil {
				details["eligible_at"] = e.EligibleAt.UTC().Format(time.RFC3339)
			}
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "ACTION_NOT_ELIGIBLE",
			"Action buttons need an account at least 7 days old, a verified email and no active strikes", details)
	default:
		return false
	}
	return true
}

// actionTapKey identifies one tapper for the per-hour dedupe: the signed-in
// viewer, else a hash of the client IP and user agent (no raw IP is kept).
func actionTapKey(c *gin.Context, viewer uuid.UUID) string {
	if viewer != uuid.Nil {
		return "u:" + viewer.String()
	}
	ip := strings.TrimSpace(c.GetHeader("X-Real-IP"))
	if ip == "" {
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			ip = strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		}
	}
	if ip == "" {
		ip = c.ClientIP()
	}
	sum := sha256.Sum256([]byte(ip + "|" + c.GetHeader("User-Agent")))
	return "h:" + hex.EncodeToString(sum[:16])
}

// ResolveActionLink — GET /v1/links/:id. No identity is required; a
// signed-in viewer is used for the dedupe and the members-only gate.
func (h *Handler) ResolveActionLink(c *gin.Context) {
	ctx := c.Request.Context()
	c.Header("Cache-Control", "no-store")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Link not found", nil)
		return
	}
	viewer, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	link, err := h.svc.ResolveActionLink(ctx, id, viewer, actionTapKey(c, viewer))
	switch {
	case errors.Is(err, service.ErrActionLinkNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Link not found", nil)
		return
	case errors.Is(err, service.ErrAuthoringStoreUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "Links are unavailable right now", nil)
		return
	case err != nil:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, link, nil)
}

// GetPostActionStats — GET /v1/posts/:postId/action/stats (author only).
func (h *Handler) GetPostActionStats(c *gin.Context) {
	ctx := c.Request.Context()
	caller, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	stats, err := h.svc.PostActionStats(ctx, caller, postID)
	switch {
	case errors.Is(err, service.ErrPostNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		return
	case errors.Is(err, service.ErrNotPostAuthor):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "Only the post's owner can do this", nil)
		return
	case err != nil:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// GetMyProfileActionStats — GET /v1/channels/me/action/stats.
func (h *Handler) GetMyProfileActionStats(c *gin.Context) {
	userID, ok := channelCaller(c)
	if !ok {
		return
	}
	stats, err := h.svc.ProfileActionStats(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// AdminListActionLinks — GET .../action-links?status=under_review|blocked.
func (h *Handler) AdminListActionLinks(c *gin.Context) {
	status := c.DefaultQuery("status", postgres.ActionStatusUnderReview)
	if status != postgres.ActionStatusUnderReview && status != postgres.ActionStatusBlocked {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "status must be under_review or blocked", nil)
		return
	}
	limit := 50
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	var before time.Time
	if v := c.Query("cursor"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "cursor must be RFC3339", nil)
			return
		}
		before = t
	}
	items, err := h.svc.ListActionLinksForReview(c.Request.Context(), status, before, limit)
	if err != nil {
		adminInternalError(c, "action link queue", err)
		return
	}
	var meta *api.Meta
	if len(items) == limit {
		meta = &api.Meta{NextCursor: items[len(items)-1].UpdatedAt.Format(time.RFC3339Nano)}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

type reviewActionLinkRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

// AdminReviewActionLink — POST .../action-links/:linkId/review.
func (h *Handler) AdminReviewActionLink(c *gin.Context) {
	ctx := c.Request.Context()
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	id, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid link ID", nil)
		return
	}
	var req reviewActionLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "body must be {decision, note}", nil)
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != postgres.ActionDecisionRestore && decision != postgres.ActionDecisionBlock {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "decision must be restore or block", nil)
		return
	}
	if len(req.Note) > 2000 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "note is at most 2000 characters", nil)
		return
	}
	view, err := h.svc.ReviewActionLink(ctx, actor, id, decision, req.Note)
	switch {
	case errors.Is(err, postgres.ErrActionLinkNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Link not found", nil)
		return
	case errors.Is(err, postgres.ErrActionLinkTransition):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "INVALID_TRANSITION", err.Error(), nil)
		return
	case err != nil:
		adminInternalError(c, "action link review", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}
