package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Service-to-service operations under /internal: projection repair, the consumer
// dead-letter queue and projection health. Routes are in RegisterRoutes.

// EnsureUser repairs the local app.users projection for :userId on demand.
// 200 — the row exists or was just rebuilt from profile-service.
// 404 — the user does not exist in the identity source either.
// 503 — the repair source is unreachable; the caller may retry or proceed.
func (h *Handler) EnsureUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.EnsureUser(c.Request.Context(), id); err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotInIdentity):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "USER_NOT_FOUND", "user does not exist", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "REPAIR_FAILED", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ListDLQ returns parked (failed) Kafka events. ?all=true includes already-
// replayed entries; the default is unreplayed only.
func (h *Handler) ListDLQ(c *gin.Context) {
	entries, err := h.store.ListDLQ(c.Request.Context(), c.Query("all") != "true", 200)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if entries == nil {
		entries = []store.DLQEntry{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": entries, "count": len(entries)}, nil)
}

// ReplayDLQ re-dispatches one parked event through the consumer's handlers.
// Handlers are idempotent, so replaying an already-applied event is safe.
func (h *Handler) ReplayDLQ(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid dlq id", nil)
		return
	}
	if h.dlqConsumer == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "REPLAY_UNAVAILABLE", "replay not wired", nil)
		return
	}
	if err := h.dlqConsumer.ReplayOne(c.Request.Context(), id); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPLAY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true, "id": id}, nil)
}

// ProjectionHealth reports whether the app.users projection is converged with
// the identity master — master vs. local counts, reconcile status, DLQ depth.
func (h *Handler) ProjectionHealth(c *gin.Context) {
	report, err := h.svc.ProjectionHealth(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}
