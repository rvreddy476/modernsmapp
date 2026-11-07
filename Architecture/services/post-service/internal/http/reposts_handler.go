// reposts_handler.go holds the repost (Echo) routes and their registration:
// create, undo, repost state, who reposted, and a user's or creator's
// reposts. Moved out of handler.go unchanged.

package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Repost (Echo) Handlers
// ---------------------------------------------------------------------------

// RegisterRepostRoutes adds repost-specific routes.
func (h *Handler) RegisterRepostRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/posts")
	{
		v1.POST("/:postId/repost", h.CreateRepost)
		v1.DELETE("/:postId/repost", h.UndoRepost)
		v1.GET("/:postId/repost/me", h.GetRepostState)
		v1.GET("/:postId/reposters", h.ListReposters)
	}

	r.GET("/v1/users/:userId/reposts", h.ListUserReposts)
	// The reachable route: /v1/creators is proxied to this service, /v1/users
	// is not (service/creator_reposts.go).
	r.GET("/v1/creators/:creatorId/reposts", h.ListCreatorReposts)
}

type CreateRepostRequest struct {
	Type              string  `json:"type" binding:"required,oneof=plain quote"`
	QuoteText         string  `json:"quote_text"`
	SourceContextType string  `json:"source_context_type"`
	SourceContextID   *string `json:"source_context_id"`
}

func (h *Handler) CreateRepost(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	var req CreateRepostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var sourceCtxID *uuid.UUID
	if req.SourceContextID != nil && *req.SourceContextID != "" {
		parsed, err := uuid.Parse(*req.SourceContextID)
		if err == nil {
			sourceCtxID = &parsed
		}
	}

	result, err := h.svc.CreateRepost(c.Request.Context(), service.CreateRepostInput{
		UserID:            userID,
		PostID:            postID,
		Type:              req.Type,
		QuoteText:         req.QuoteText,
		SourceContextType: req.SourceContextType,
		SourceContextID:   sourceCtxID,
	})
	if err != nil {
		if writeStandingError(c, err) || writeHeartOnlyError(c, err) {
			return
		}
		switch err.Error() {
		case "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many reposts, please slow down", nil)
		case "POST_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		case "NOT_ELIGIBLE":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_ELIGIBLE", "This post cannot be reposted", nil)
		case "ALREADY_REPOSTED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ALREADY_REPOSTED", "You already reposted this post", nil)
		case "QUOTE_TEXT_REQUIRED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "QUOTE_TEXT_REQUIRED", "Add your thoughts to quote repost", nil)
		case "QUOTE_TEXT_TOO_LONG":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "QUOTE_TEXT_TOO_LONG", "Quote text must be 500 characters or fewer", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

func (h *Handler) UndoRepost(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	err = h.svc.UndoRepost(c.Request.Context(), userID, postID)
	if err != nil {
		switch err.Error() {
		case "REPOST_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "No active repost found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	c.Writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetRepostState(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	result, err := h.svc.GetRepostState(c.Request.Context(), userID, postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) ListReposters(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	cursor := c.Query("cursor")

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, optionalCallerID(c)) {
		return
	}
	result, err := h.svc.ListReposters(c.Request.Context(), postID, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ListUserReposts is GET /v1/users/:userId/reposts — the older route, kept;
// the gateway sends /v1/users to user-service, so clients use
// GET /v1/creators/:creatorId/reposts (ListCreatorReposts). Same answer.
func (h *Handler) ListUserReposts(c *gin.Context) { h.answerUserReposts(c, c.Param("userId")) }

// ListCreatorReposts is GET /v1/creators/:creatorId/reposts: the creator's
// reposts as the viewer may see them (service/creator_reposts.go). Signed-out
// viewers get the public answer. {data:{items:[…], next_cursor}}; 400
// INVALID_ID / INVALID_CURSOR.
func (h *Handler) ListCreatorReposts(c *gin.Context) { h.answerUserReposts(c, c.Param("creatorId")) }

func (h *Handler) answerUserReposts(c *gin.Context, rawUserID string) {
	ctx := c.Request.Context()
	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	cursor := c.Query("cursor")
	var viewerID *uuid.UUID
	if id, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil && id != uuid.Nil {
		viewerID = &id
	}

	result, err := h.svc.ListUserReposts(ctx, userID, viewerID, limit, cursor)
	if errors.Is(err, service.ErrInvalidRepostCursor) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil)
		return
	}
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}
