// reactions_handler.go holds the reaction, like and share routes on a post:
// legacy react / unreact / my reaction, the like toggle, sharing, and the
// multi-reaction toggle with its counts. Moved out of handler.go unchanged.

package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ReactionRequest struct {
	Reaction string `json:"reaction" binding:"required"`
}

func (h *Handler) React(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	var req ReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	if err := h.svc.React(c.Request.Context(), postID, userID, req.Reaction); err != nil {
		if writeHeartOnlyError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "reacted"}, nil)
}

func (h *Handler) Unreact(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	if err := h.svc.Unreact(c.Request.Context(), postID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unreacted"}, nil)
}

func (h *Handler) GetMyReaction(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	reaction, err := h.svc.GetMyReaction(c.Request.Context(), postID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if reaction == "" {
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"reaction": nil}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"reaction": reaction}, nil)
}

// ============================================================
// New Engagement Handlers
// ============================================================

func (h *Handler) ToggleLike(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	// NoLikes check is now inside the service (audit H2).
	result, err := h.svc.ToggleLike(c.Request.Context(), postID, userID)
	if err != nil {
		switch {
		case err.Error() == "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many like toggles, please slow down", nil)
		case errors.Is(err, service.ErrLikesDisabled):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "LIKES_DISABLED", "Likes are disabled on this post", nil)
		case errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "POST_NOT_FOUND", "post not found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

type ShareRequest struct {
	ShareType string `json:"share_type" binding:"required,oneof=repost quote external"`
	QuoteText string `json:"quote_text"`
}

func (h *Handler) SharePost(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	var req ShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	result, err := h.svc.SharePost(c.Request.Context(), postID, userID, req.ShareType, req.QuoteText)
	if err != nil {
		if writeHeartOnlyError(c, err) {
			return
		}
		switch err.Error() {
		case "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many shares, please slow down", nil)
		case "CIRCLE_SHARE_RESTRICTED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "CIRCLE_SHARE_RESTRICTED", "Cannot share this post type externally", nil)
		case "ALREADY_SHARED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ALREADY_SHARED", "You already reposted this", nil)
		case "POST_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ============================================================
// Multi-Reaction Handlers
// ============================================================

type ToggleReactionRequest struct {
	ReactionType string `json:"reaction_type" binding:"required"`
}

func (h *Handler) ToggleReaction(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	var req ToggleReactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	result, err := h.svc.ToggleReaction(c.Request.Context(), postID, userID, req.ReactionType)
	if err != nil {
		switch {
		case writeHeartOnlyError(c, err):
			// 400 REACTION_NOT_ALLOWED: a post takes the heart only.
		case err.Error() == "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many reactions, please slow down", nil)
		case errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
			// Collapse "not found" + "not visible" into the same
			// response so the engagement endpoint doesn't disclose
			// the existence of a restricted post to a non-allowed
			// viewer. 404 + generic message.
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "POST_NOT_FOUND", "post not found", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) GetReactionCounts(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, optionalCallerID(c)) {
		return
	}
	counts, err := h.svc.GetReactionCounts(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}
