package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Scheduled publish routes (founder, 2026-09-05). The create-side half —
// `publish_at`, `hashtags`, `mentions` on POST /v1/posts — is in
// handler.go / CreatePostRequest; this file is the author's schedule
// management:
//
//	GET   /v1/posts/me/scheduled?limit=&cursor=   newest publish_at first
//	PATCH /v1/posts/{id}/schedule {"publish_at": "…"}   reschedule
//	PATCH /v1/posts/{id}/schedule {"publish_at": null}  publish now
//
// DELETE /v1/posts/{id} still soft-deletes a scheduled post.

// parsePublishAt reads an optional RFC3339 `publish_at`. An absent or
// empty value is "publish now" (nil). The window check is the service's
// (ValidatePublishAt); this only rejects an unparseable timestamp, with
// the same code so the client has one branch.
func parsePublishAt(raw *string) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
	if err != nil {
		return nil, service.ErrInvalidPublishAt
	}
	t = t.UTC()
	return &t, nil
}

// writeScheduleError maps the scheduling and explicit-tag errors onto their
// stable codes; returns false when err is none of them.
func writeScheduleError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidPublishAt):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_PUBLISH_AT",
			"publish_at must be an RFC3339 time between 5 minutes and 30 days from now", nil)
	case errors.Is(err, service.ErrTooManyHashtags):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "TOO_MANY_HASHTAGS", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidHashtag):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_HASHTAG", err.Error(), nil)
	case errors.Is(err, service.ErrTooManyMentions):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "TOO_MANY_MENTIONS", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidMention):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_MENTION", err.Error(), nil)
	case errors.Is(err, service.ErrPostNotScheduled):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "NOT_SCHEDULED", "Post is not scheduled", nil)
	default:
		return false
	}
	return true
}

// scheduleRequest is the PATCH body. PublishAt is a raw message so the three
// shapes are distinguishable: absent and `null` both mean "publish now";
// a string is the new time.
type scheduleRequest struct {
	PublishAt json.RawMessage `json:"publish_at"`
}

// UpdateSchedule — PATCH /v1/posts/:postId/schedule. Author only.
func (h *Handler) UpdateSchedule(c *gin.Context) {
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req scheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	var publishAt *time.Time
	if len(req.PublishAt) > 0 && string(req.PublishAt) != "null" {
		var raw string
		if err := json.Unmarshal(req.PublishAt, &raw); err != nil {
			writeScheduleError(c, service.ErrInvalidPublishAt)
			return
		}
		publishAt, err = parsePublishAt(&raw)
		if err != nil {
			writeScheduleError(c, err)
			return
		}
	}

	p, err := h.svc.ReschedulePost(c.Request.Context(), postID, actorID, publishAt)
	if err != nil {
		switch {
		case writeScheduleError(c, err):
		case errors.Is(err, service.ErrPostNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		case errors.Is(err, service.ErrPostForbidden):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Not the post author", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// ListMyScheduledPosts — GET /v1/posts/me/scheduled?limit=&cursor=. The
// caller's scheduled posts, newest publish_at first; every item carries
// publish_at and is_scheduled=true.
func (h *Handler) ListMyScheduledPosts(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	posts, next, err := h.svc.ListMyScheduledPosts(c.Request.Context(), userID, limit, c.DefaultQuery("cursor", ""))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, posts, meta)
}
