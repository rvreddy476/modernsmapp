// video_tools_handler.go holds the creator-tool routes for a video post:
// video detail, trim, category override, cover frame (and its refusal
// mapper), publish, and the distribution update. Moved out of handler.go
// unchanged.

package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func writeDistributionError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrUnsupportedDistribution):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"UNSUPPORTED_DISTRIBUTION", err.Error(), nil)
		return true
	case errors.Is(err, service.ErrInvalidDistribution):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_DISTRIBUTION", err.Error(), nil)
		return true
	}
	return false
}

// UpdateDistribution replaces a post's distribution policy (P0-1).
// PATCH /v1/posts/:postId/distribution  body: {"version":1,...}
// Owner-only. The row update + rev bump + outbox event are atomic.
func (h *Handler) UpdateDistribution(c *gin.Context) {
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "unreadable body", nil)
		return
	}

	p, err := h.svc.UpdateDistribution(c.Request.Context(), postID, actorID, raw)
	if err != nil {
		switch {
		case writeDistributionError(c, err):
		case errors.Is(err, service.ErrPostNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "post not found", nil)
		case errors.Is(err, service.ErrNotPostAuthor):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not the post author", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// ============================================================
// Video Creator Tools Handlers
// ============================================================

func (h *Handler) GetVideoDetail(c *gin.Context) {
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil)
		return
	}
	// storage_video_url is owner-only; GetVideoDetailForCaller redacts it for
	// everyone else, including the tokenless caller the gateway lets through.
	var callerID *uuid.UUID
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			callerID = &id
		}
	}
	vm, err := h.svc.GetVideoDetailForCaller(c.Request.Context(), videoID, callerID)
	if err != nil {
		// The post's own read gate (2026-09-28): a post the caller may not
		// see is the same 404 as a missing one; an 18+ post answers the
		// age codes GET /v1/posts/:id answers.
		if errors.Is(err, service.ErrAgeSignIn) || errors.Is(err, service.ErrAgeRestricted) || errors.Is(err, service.ErrAgeUnverified) {
			writeReadGateError(c, err)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Video metadata not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, vm, nil)
}

func (h *Handler) UpdateTrim(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil)
		return
	}

	var req struct {
		TrimStartMs int  `json:"trim_start_ms"`
		TrimEndMs   *int `json:"trim_end_ms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateVideoTrim(c.Request.Context(), videoID, userID, req.TrimStartMs, req.TrimEndMs); err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) OverrideCategory(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil)
		return
	}

	var req struct {
		Category string `json:"category" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	// Two meanings on one route, kept apart by the value (2026-09-27):
	// "flick" / "long_video" is the historic CONTENT-TYPE override the
	// Android app sends; anything else is a taxonomy slug for
	// posts.category, validated against the post's kind — 422
	// INVALID_CATEGORY otherwise. Both answer {"status":"updated"}.
	if kind := strings.ToLower(strings.TrimSpace(req.Category)); kind != "flick" && kind != "long_video" {
		if err := h.svc.SetPostCategory(c.Request.Context(), userID, videoID, req.Category); err != nil {
			writePostEditError(c, err)
			return
		}
		api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
		return
	}

	if err := h.svc.OverrideCategory(c.Request.Context(), videoID, userID, req.Category); err != nil {
		if strings.Contains(err.Error(), "unauthorized") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		if strings.Contains(err.Error(), "cannot classify") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) SetCoverFrame(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil)
		return
	}

	var req struct {
		CoverMediaID *string `json:"cover_media_id"`
		ThumbnailURL *string `json:"thumbnail_url"`
		TimestampMs  *int    `json:"timestamp_ms"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var coverMediaID *uuid.UUID
	if req.CoverMediaID != nil {
		id, err := uuid.Parse(*req.CoverMediaID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid cover media ID", nil)
			return
		}
		coverMediaID = &id
	}

	if err := h.svc.SetCoverFrame(c.Request.Context(), videoID, userID, coverMediaID, req.ThumbnailURL); err != nil {
		if writeCoverMediaError(c, err) || writeMediaClaimsError(c, err) {
			return
		}
		if strings.Contains(err.Error(), "unauthorized") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// writeCoverMediaError answers a refused COVER exactly as PATCH /v1/posts/:id
// does (writePostEditError): 403 MEDIA_NOT_OWNED, 422 MEDIA_NOT_FOUND,
// MEDIA_NOT_READY, MEDIA_TYPE_MISMATCH. It runs before the create guards,
// whose 400s are about attachments; returns false for any other error.
func writeCoverMediaError(c *gin.Context, err error) bool {
	var coverErr *service.CoverMediaError
	if !errors.As(err, &coverErr) {
		return false
	}
	writePostEditError(c, err)
	return true
}

func (h *Handler) PublishVideo(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	videoID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid video ID", nil)
		return
	}

	if err := h.svc.PublishVideo(c.Request.Context(), videoID, userID); err != nil {
		if writeStandingError(c, err) {
			return
		}
		if strings.Contains(err.Error(), "unauthorized") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		if strings.Contains(err.Error(), "not ready") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "NOT_READY", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "published"}, nil)
}
