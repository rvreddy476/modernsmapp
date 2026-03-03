package http

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// hasScope reports whether the space-separated scopes string contains the exact target scope.
func hasScope(scopes, target string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == target {
			return true
		}
	}
	return false
}

// requireAnyScope returns true and continues if the user has any of the given scopes.
// It writes a 403 and returns false otherwise.
func requireAnyScope(c *gin.Context, scopes ...string) bool {
	userScopes := c.GetHeader("X-Scopes")
	for _, scope := range scopes {
		if hasScope(userScopes, scope) {
			return true
		}
	}
	c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
		"code":    "FORBIDDEN",
		"message": "Insufficient scope. Required: " + strings.Join(scopes, " or "),
	}})
	return false
}

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/admin")
	{
		v1.GET("/dashboard", h.GetDashboard)
		v1.GET("/audit-log", h.GetAuditLog)
		v1.GET("/reports", h.ListReports)
		v1.GET("/suspensions", h.ListSuspensions)
		v1.POST("/takedown", h.TakedownContent)
		v1.POST("/users/:userId/suspend", h.SuspendUser)
		v1.DELETE("/users/:userId/suspend", h.UnsuspendUser)
	}
}

type TakedownRequest struct {
	EntityType string `json:"entity_type" binding:"required,oneof=post comment user message"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

func (h *Handler) TakedownContent(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	var req TakedownRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// In V1, we assume the API Key user is "system-admin"
	adminActor := "system-admin"

	if err := h.svc.TakedownContent(c.Request.Context(), adminActor, req.EntityType, req.EntityID, req.Reason); err != nil {
		log.Printf("Takedown error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Takedown failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "taken_down"}, nil)
}

type SuspendRequest struct {
	Until  time.Time `json:"until" binding:"required"`
	Reason string    `json:"reason" binding:"required"`
}

func (h *Handler) SuspendUser(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid User ID", nil, nil)
		return
	}

	var req SuspendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	adminActor := "system-admin"

	if err := h.svc.SuspendUser(c.Request.Context(), adminActor, userID, req.Until, req.Reason); err != nil {
		log.Printf("Suspend error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Suspension failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "suspended"}, nil)
}

// GetDashboard returns aggregate platform stats.
func (h *Handler) GetDashboard(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	stats, err := h.svc.GetDashboard(c.Request.Context())
	if err != nil {
		log.Printf("Dashboard error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load dashboard", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// GetAuditLog returns paginated audit log entries.
func (h *Handler) GetAuditLog(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	limit, offset := parsePagination(c)

	logs, total, err := h.svc.GetAuditLogs(c.Request.Context(), limit, offset)
	if err != nil {
		log.Printf("Audit log error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load audit log", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  logs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// ListReports returns paginated reports, optionally filtered by status.
func (h *Handler) ListReports(c *gin.Context) {
	if !requireAnyScope(c, "moderator", "admin", "superadmin") {
		return
	}

	limit, offset := parsePagination(c)
	status := c.Query("status")

	reports, total, err := h.svc.ListReports(c.Request.Context(), status, limit, offset)
	if err != nil {
		log.Printf("List reports error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load reports", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  reports,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// ListSuspensions returns paginated active suspensions.
func (h *Handler) ListSuspensions(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	limit, offset := parsePagination(c)

	suspensions, total, err := h.svc.ListSuspensions(c.Request.Context(), limit, offset)
	if err != nil {
		log.Printf("List suspensions error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load suspensions", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  suspensions,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// UnsuspendUser removes a user's suspension.
func (h *Handler) UnsuspendUser(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid User ID", nil, nil)
		return
	}

	adminActor := "system-admin"

	if err := h.svc.UnsuspendUser(c.Request.Context(), adminActor, userID); err != nil {
		log.Printf("Unsuspend error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Unsuspend failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsuspended"}, nil)
}

// parsePagination extracts limit and offset query params with defaults.
func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}
