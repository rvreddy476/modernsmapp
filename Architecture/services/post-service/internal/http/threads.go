package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 1 P0-8 — thread endpoints.
//   POST /v1/posts/thread          atomic ordered create (idempotent)
//   GET  /v1/posts/:postId/thread  full thread context from any entry

type threadEntryRequest struct {
	Text     string   `json:"text"`
	MediaIDs []string `json:"media_ids"`
}

type createThreadRequest struct {
	Visibility     string               `json:"visibility"`
	Entries        []threadEntryRequest `json:"entries" binding:"required"`
	IdempotencyKey string               `json:"idempotency_key"`
}

func (h *Handler) CreateThread(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	// Same rate limit as single-post creation: a thread is one authored
	// unit, not N free posts.
	if err := service.CheckPostRateLimit(c.Request.Context(), h.rdb, authorID); err != nil {
		c.Header("Retry-After", "3600")
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
		return
	}

	var req createThreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var idemKey uuid.UUID
	if req.IdempotencyKey != "" {
		idemKey, err = uuid.Parse(req.IdempotencyKey)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "idempotency_key must be a UUID", nil)
			return
		}
	}

	entries := make([]service.ThreadEntryInput, 0, len(req.Entries))
	for _, e := range req.Entries {
		entry := service.ThreadEntryInput{Text: e.Text}
		for _, idStr := range e.MediaIDs {
			id, err := uuid.Parse(idStr)
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid media ID: "+idStr, nil)
				return
			}
			entry.MediaIDs = append(entry.MediaIDs, id)
		}
		entries = append(entries, entry)
	}

	posts, err := h.svc.CreateThread(c.Request.Context(), &service.CreateThreadInput{
		AuthorID:       authorID,
		Visibility:     req.Visibility,
		Entries:        entries,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidThread) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_THREAD", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, posts, nil)
}

func (h *Handler) GetThread(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var viewerID *uuid.UUID
	if id, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &id
	}
	posts, err := h.svc.GetThread(c.Request.Context(), postID, viewerID)
	if err != nil {
		if errors.Is(err, service.ErrPostNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "thread not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}
