package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetProfile returns the caller's dating profile.
func (h *Handler) GetProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	p, err := h.svc.GetProfile(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// UpsertProfile creates or updates the caller's profile.
func (h *Handler) UpsertProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body store.UpsertProfileParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	p, err := h.svc.UpsertProfile(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPSERT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// PatchIntent updates only the intent field on the caller's profile.
func (h *Handler) PatchIntent(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Intent string `json:"intent"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	p, err := h.svc.SetIntent(c.Request.Context(), userID, body.Intent)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// PostPause toggles the paused flag on the caller's profile.
func (h *Handler) PostPause(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	p, err := h.svc.SetPaused(c.Request.Context(), userID, body.Paused)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// DeleteProfile soft-deletes the caller's dating profile.
func (h *Handler) DeleteProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.DeleteProfile(c.Request.Context(), userID, body.Reason); err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "deleted"}, nil)
}

// GetProfilePreview returns a tiny shape suitable for cross-service name
// lookups (Sprint 3 — notification-service consumer).
// GET /v1/dating/internal/profile/:userId/preview, registered behind
// requireServiceCaller(OpProfilePreview).
func (h *Handler) GetProfilePreview(c *gin.Context) {
	userID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	first, err := h.svc.Store().LookupFirstName(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"user_id":    userID,
		"first_name": first,
	}, nil)
}

// GetProfilePreviewLegacy — GET /v1/dating/profile/:userId/preview.
//
// Retired path, kept for ONE release because notification-service still
// calls it with the internal key. A request carrying a gateway-set user
// identity is a proxied user and gets 410 pointing at the internal path;
// a request without one must hold a service credential and is served.
func (h *Handler) GetProfilePreviewLegacy(c *gin.Context) {
	if hasGatewayIdentity(c) {
		movedTo(InternalProfilePreviewPath)(c)
		return
	}
	if !h.authorizeServiceCaller(c, OpProfilePreview) {
		return
	}
	h.GetProfilePreview(c)
}
