package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Self-service keyword filters — TikTok's "Content preferences → Filter
// keywords". The list is privacy-first: only the owner (via X-User-Id from
// the gateway) can read or write it, and the only other reader is
// feed-service over the internal endpoint. Post authors never learn who
// filtered them.

// GetMyKeywordFilters returns the caller's own hide list.
// GET /v1/users/me/keyword-filters → {"data":{"keywords":["..."]}}
func (h *Handler) GetMyKeywordFilters(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	keywords, err := h.svc.GetUserKeywordFilters(c.Request.Context(), userID)
	if err != nil {
		slog.Error("GetMyKeywordFilters", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get keyword filters", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"keywords": keywords}, nil)
}

type putKeywordFiltersRequest struct {
	Keywords []string `json:"keywords"`
}

// PutMyKeywordFilters atomically replaces the caller's hide list.
// PUT /v1/users/me/keyword-filters {"keywords":["Word","#tag"]}
func (h *Handler) PutMyKeywordFilters(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req putKeywordFiltersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if req.Keywords == nil {
		req.Keywords = []string{}
	}
	keywords, err := h.svc.ReplaceUserKeywordFilters(c.Request.Context(), userID, req.Keywords)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTooManyKeywords):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "TOO_MANY_KEYWORDS", err.Error(), nil)
		case errors.Is(err, service.ErrInvalidKeyword):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_KEYWORD", err.Error(), nil)
		default:
			slog.Error("PutMyKeywordFilters", "err", err)
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save keyword filters", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"keywords": keywords}, nil)
}

// InternalGetKeywordFilters serves a user's hide keywords to peer services
// (feed-service). The whole router already sits behind RequireInternalKey
// (X-Internal-Service-Key), so this handler only needs the user_id.
// GET /v1/internal/keyword-filters?user_id= → {"data":{"keywords":["..."]}}
func (h *Handler) InternalGetKeywordFilters(c *gin.Context) {
	userID, err := uuid.Parse(c.Query("user_id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user_id", nil)
		return
	}
	keywords, err := h.svc.GetUserKeywordFilters(c.Request.Context(), userID)
	if err != nil {
		slog.Error("InternalGetKeywordFilters", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get keyword filters", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"keywords": keywords}, nil)
}
