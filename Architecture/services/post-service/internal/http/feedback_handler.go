package http

import (
	"net/http"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type submitFeedbackRequest struct {
	FeedbackType string  `json:"feedback_type"`
	PostID       *string `json:"post_id"`
	Message      string  `json:"message" binding:"required"`
	Context      string  `json:"context"`
}

// SubmitFeedback — POST /v1/feedback. Stores a product-feedback note (distinct
// from a trust-safety report). Mirrors the ToggleBookmark/CreatePlaylist pattern.
func (h *Handler) SubmitFeedback(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	var req submitFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	f := &postgres.Feedback{
		UserID:       userID,
		FeedbackType: req.FeedbackType,
		Message:      req.Message,
		Context:      req.Context,
	}
	if req.PostID != nil {
		if id, err := uuid.Parse(*req.PostID); err == nil {
			f.PostID = &id
		}
	}

	if err := h.svc.SubmitFeedback(c.Request.Context(), f); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, f, nil)
}
