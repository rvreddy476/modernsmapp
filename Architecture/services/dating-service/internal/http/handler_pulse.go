package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetPulseToday returns the caller's curated daily Pulse list.
//
// Response shape is locked (mobile is consuming) — see service.PulseResponse.
// Cached in Redis for 24 h, invalidated on profile/Tune/preferences updates.
func (h *Handler) GetPulseToday(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetPulseToday(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	// We pass the entire envelope through as `data` because the contract
	// spec puts `data` as the candidate array and `meta` as a top-level
	// sibling.
	c.JSON(http.StatusOK, resp)
}

// GetPulseNebula handles GET /v1/dating/pulse/nebula?filter=passed
//
// Sprint 2 supports `filter=passed` only (recently-passed candidates).
// Other filter values fall back to an empty list rather than 400 — keeps
// the mobile contract forgiving.
func (h *Handler) GetPulseNebula(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	filter := c.Query("filter")
	if filter == "" {
		filter = "passed"
	}
	limit := parseQueryInt(c, "limit", 100)
	offset := parseQueryInt(c, "offset", 0)

	switch filter {
	case "passed":
		resp, err := h.svc.GetPulseNebulaPassed(c.Request.Context(), userID, limit, offset)
		if err != nil {
			respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
			return
		}
		c.JSON(http.StatusOK, resp)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_FILTER",
			"filter must be one of: passed", nil)
	}
}

// parseQueryInt is a forgiving helper — falls back to fallback on any parse
// problem instead of erroring.
func parseQueryInt(c *gin.Context, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
