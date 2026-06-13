package http

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// pageParams reads limit (default 20, max 100) and offset query params.
func pageParams(c *gin.Context) (limit, offset int) {
	limit, offset = 20, 0
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}
	return
}

type fileGrievanceRequest struct {
	Subject         string `json:"subject" binding:"required"`
	AboutEntityType string `json:"about_entity_type"`
	AboutEntityID   string `json:"about_entity_id"`
	Description     string `json:"description" binding:"required"`
}

// FileGrievance lets any authenticated user lodge a grievance.
func (h *Handler) FileGrievance(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req fileGrievanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	g, err := h.svc.FileGrievance(c.Request.Context(), userID, req.Subject, req.AboutEntityType, req.AboutEntityID, req.Description)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, g, nil)
}

// GetGrievance returns a grievance to its complainant or to an officer.
func (h *Handler) GetGrievance(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid grievance ID", nil)
		return
	}
	g, err := h.svc.GetGrievance(c.Request.Context(), id)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Grievance not found", nil)
		return
	}
	if g.ComplainantID != userID && !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Not your grievance", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, g, nil)
}

// ListGrievances returns the caller's own grievances (?mine=true) or, for
// an officer, the redressal queue (optionally filtered by ?status=).
func (h *Handler) ListGrievances(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	limit, offset := pageParams(c)

	var list []postgres.Grievance
	if c.Query("mine") == "true" {
		list, err = h.svc.ListMyGrievances(c.Request.Context(), userID, limit, offset)
	} else {
		if !hasScope(c.GetHeader("X-Scopes"), "admin") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Grievance officer scope required", nil)
			return
		}
		list, err = h.svc.ListGrievances(c.Request.Context(), c.Query("status"), limit, offset)
	}
	if err != nil {
		slog.Error("ListGrievances error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list grievances", nil)
		return
	}
	if list == nil {
		list = []postgres.Grievance{}
	}
	api.JSON(c.Writer, http.StatusOK, list, nil)
}

type updateGrievanceRequest struct {
	Status          string `json:"status" binding:"required,oneof=acknowledged resolved rejected"`
	ResolutionNotes string `json:"resolution_notes"`
}

// UpdateGrievance records an officer's verdict on a grievance.
func (h *Handler) UpdateGrievance(c *gin.Context) {
	officerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Grievance officer scope required", nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid grievance ID", nil)
		return
	}
	var req updateGrievanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	g, err := h.svc.UpdateGrievance(c.Request.Context(), id, req.Status, req.ResolutionNotes, &officerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, g, nil)
}
