package http

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
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
	token := viaAdminToken(c)
	var userID uuid.UUID
	if !token {
		var err error
		userID, err = uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
			return
		}
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
	if !token && g.ComplainantID != userID && !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Not your grievance", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, g, nil)
}

// ListGrievances returns the caller's own grievances (?mine=true) or, for
// an officer, the redressal queue (optionally filtered by ?status=).
func (h *Handler) ListGrievances(c *gin.Context) {
	token := viaAdminToken(c)
	var userID uuid.UUID
	var err error
	if !token {
		userID, err = uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
			return
		}
	}
	limit, offset := pageParams(c)

	var list []postgres.Grievance
	// ?mine=true is a user's own list; it has no meaning on the admin token path.
	if c.Query("mine") == "true" && !token {
		list, err = h.svc.ListMyGrievances(c.Request.Context(), userID, limit, offset)
	} else {
		if !adminAllowed(c) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Grievance officer scope required", nil)
			return
		}
		switch c.Query("overdue") {
		case "true":
			if c.Query("status") != "" {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "overdue cannot be combined with status", nil)
				return
			}
			list, err = h.svc.ListOverdueGrievances(c.Request.Context(), limit, offset)
		case "", "false":
			list, err = h.svc.ListGrievances(c.Request.Context(), c.Query("status"), limit, offset)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "overdue must be true or false", nil)
			return
		}
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
	Status          string  `json:"status" binding:"omitempty,oneof=acknowledged resolved rejected"`
	ResolutionNotes *string `json:"resolution_notes"`
	// AssignedTo hands the grievance to another officer. When omitted on a
	// status change, the acting officer becomes the assignee.
	AssignedTo *string `json:"assigned_to"`
}

// UpdateGrievance records an officer's verdict and/or assignment. Every
// change writes one audit row (who, from/to status and officer, notes).
func (h *Handler) UpdateGrievance(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Grievance officer scope required", nil)
		return
	}
	meta, ok := adminAuditMeta(c, "")
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, codeActorRequired, "A verified admin user is required", nil)
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
	change := service.GrievanceChange{Status: req.Status, Notes: req.ResolutionNotes}
	if req.AssignedTo != nil {
		assignee, err := uuid.Parse(strings.TrimSpace(*req.AssignedTo))
		if err != nil || assignee == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid assigned_to", nil)
			return
		}
		change.AssignedTo = &assignee
	} else if req.Status != "" {
		officer := meta.Actor.UserID
		change.AssignedTo = &officer
	}
	if change.Status == "" && change.AssignedTo == nil && change.Notes == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "status, assigned_to or resolution_notes is required", nil)
		return
	}
	if req.ResolutionNotes != nil {
		meta.Reason = *req.ResolutionNotes
	}
	g, err := h.svc.UpdateGrievance(c.Request.Context(), id, change, meta)
	if err != nil {
		writeAuditedChangeError(c, "UpdateGrievance", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, g, nil)
}

// GetGrievanceHistory — GET /v1/grievances/:id/history. Admin scope only.
// Returns every audited change to the grievance in order, including each
// officer assignment (prev_assignee -> new_assignee, who, when).
func (h *Handler) GetGrievanceHistory(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Grievance officer scope required", nil)
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
	history, err := h.svc.GrievanceHistory(c.Request.Context(), id)
	if err != nil {
		slog.Error("GrievanceHistory error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load grievance history", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"grievance": g, "items": history}, nil)
}
