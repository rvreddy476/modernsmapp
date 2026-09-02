package http

import (
	"errors"
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/atpost/identity-user-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 — module preferences (privacy-first, server-driven) and region.
//
// GET  /v1/users/me/modules  → current selection, or the defaults when the
//                              user never chose (never a 404).
// PUT  /v1/users/me/modules  → {"modules":[...],"home_module":"feed",
//                              "complete_onboarding":true}
// PUT  /v1/users/me/region   → {"country_code":"IN"}
//
// All three sit behind the same JWT middleware as /me/settings.

// updateModulesRequest is the PUT /v1/users/me/modules body.
type updateModulesRequest struct {
	Modules            []string `json:"modules"`
	HomeModule         string   `json:"home_module"`
	CompleteOnboarding bool     `json:"complete_onboarding"`
}

// updateRegionRequest is the PUT /v1/users/me/region body.
type updateRegionRequest struct {
	CountryCode string `json:"country_code"`
}

func (h *Handler) authedUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return userID, true
}

func (h *Handler) GetMyModules(c *gin.Context) {
	userID, ok := h.authedUserID(c)
	if !ok {
		return
	}

	p, err := h.svc.GetModulePreferences(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch module preferences", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) UpdateMyModules(c *gin.Context) {
	userID, ok := h.authedUserID(c)
	if !ok {
		return
	}

	var req updateModulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if req.Modules == nil {
		// Omitting the array entirely is a malformed request, not "no
		// modules" — [] is how a client says feed-only.
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "modules is required (use [] for none)", nil, nil)
		return
	}

	p, err := h.svc.UpdateModulePreferences(c.Request.Context(), userID, req.Modules, req.HomeModule, req.CompleteOnboarding)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidModule):
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_MODULE", err.Error(), nil, nil)
		case errors.Is(err, service.ErrInvalidHomeModule):
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_HOME_MODULE", err.Error(), nil, nil)
		default:
			h.log.Error("failed to update module preferences", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) UpdateMyRegion(c *gin.Context) {
	userID, ok := h.authedUserID(c)
	if !ok {
		return
	}

	var req updateRegionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	region, err := h.svc.SetRegion(c.Request.Context(), userID, req.CountryCode)
	if err != nil {
		if errors.Is(err, service.ErrInvalidRegion) {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REGION",
				"country_code must be a two-letter ISO-3166-1 alpha-2 code", nil, nil)
			return
		}
		h.log.Error("failed to update region", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"region": region}, nil)
}
