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

type setReviewStatusRequest struct {
	PostID string `json:"post_id" binding:"required"`
	Status string `json:"status" binding:"required"` // approved | rejected
}

// SetReviewStatusInternal — POST /v1/posts/internal/review-status. Lets
// reviewer-service's ML pre-filter auto-resolve a FLAGGED post. Service-to-service
// only (gateway blocks /internal/ from non-admins). Scoped to flagged rows.
func (h *Handler) SetReviewStatusInternal(c *gin.Context) {
	var req setReviewStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if req.Status != "approved" && req.Status != "rejected" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "status must be approved or rejected", nil)
		return
	}
	postID, err := uuid.Parse(req.PostID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post_id", nil)
		return
	}
	changed, err := h.svc.AutoResolveFlagged(c.Request.Context(), postID, req.Status)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"changed": changed, "status": req.Status}, nil)
}

type setVisibilityRequest struct {
	PostID     string `json:"post_id" binding:"required"`
	Visibility string `json:"visibility" binding:"required"` // typically "public"
}

// SetVisibilityInternal — POST /v1/posts/internal/visibility. Lets the
// reviewer-service promotion worker move a STAGED post to its full visibility.
// Service-to-service only (gateway blocks /internal/ from non-admins); scoped to
// staged rows.
func (h *Handler) SetVisibilityInternal(c *gin.Context) {
	var req setVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	postID, err := uuid.Parse(req.PostID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post_id", nil)
		return
	}
	changed, err := h.svc.PromoteStaged(c.Request.Context(), postID, req.Visibility)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"changed": changed, "visibility": req.Visibility}, nil)
}
