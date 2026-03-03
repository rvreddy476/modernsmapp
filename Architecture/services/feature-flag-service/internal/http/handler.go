package http

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/atpost/feature-flag-service/internal/service"
	"github.com/atpost/feature-flag-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
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

type Handler struct {
	svc *service.Evaluator
}

func New(svc *service.Evaluator) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1")
	{
		v1.GET("/flags/me", h.EvaluateMe)

		// A/B conversion tracking (no auth required — called by client)
		v1.POST("/flags/conversions", h.RecordConversion)

		admin := v1.Group("/admin/flags")
		admin.Use(h.AdminAuthMiddleware())
		admin.POST("", h.UpsertFlag)
		admin.GET("", h.ListFlags)
		admin.GET("/:key/audit", h.GetFlagAuditLog)
		admin.GET("/:key/results", h.GetExperimentResults)
	}
}

// AdminAuthMiddleware protects admin endpoints with a scope check.
func (h *Handler) AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		scopes := c.GetHeader("X-Scopes")
		if !hasScope(scopes, "admin") && !hasScope(scopes, "superadmin") {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

func (h *Handler) EvaluateMe(c *gin.Context) {
	userID := c.GetHeader("X-User-Id") // From Gateway
	if userID == "" {
		// fallback for unauth users
		userID = "anonymous"
	}

	// Optional: specific flag key in query
	key := c.Query("key")
	if key != "" {
		result, err := h.svc.Evaluate(c.Request.Context(), key, userID)
		if err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Evaluation failed", nil, nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, result, nil)
		return
	}

	// If no key, maybe list all? For V1, let's just require key or return empty.
	// Or we could implement "GetAllFlagsForUser" in service.
	// For simplicity in V1, let's just return a generic message or required key.
	api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Key query parameter required for V1", nil, nil)
}

func (h *Handler) UpsertFlag(c *gin.Context) {
	var flag postgres.Flag
	if err := c.ShouldBindJSON(&flag); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// Embed actor in context for audit log
	actor := c.GetHeader("X-User-Id")
	if actor == "" {
		actor = "system"
	}
	ctx := context.WithValue(c.Request.Context(), "actor_user_id", actor) //nolint:staticcheck

	if err := h.svc.UpsertFlag(ctx, &flag); err != nil {
		log.Printf("Upsert error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to upsert flag", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, flag, nil)
}

func (h *Handler) ListFlags(c *gin.Context) {
	flags, err := h.svc.ListFlags(c.Request.Context())
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list flags", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": flags}, nil)
}

// GetFlagAuditLog returns the audit log for a specific flag (admin only).
func (h *Handler) GetFlagAuditLog(c *gin.Context) {
	scopes := c.GetHeader("X-Scopes")
	if !hasScope(scopes, "admin") && !hasScope(scopes, "superadmin") {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "FORBIDDEN"}})
		return
	}
	key := c.Param("key")
	limit := 50
	offset := 0
	entries, err := h.svc.GetAuditLog(c.Request.Context(), key, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// RecordConversion records an A/B experiment conversion event.
func (h *Handler) RecordConversion(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	var req struct {
		FlagKey   string `json:"flag_key" binding:"required"`
		Variant   string `json:"variant" binding:"required"`
		EventType string `json:"event_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_REQUEST"}})
		return
	}
	if err := h.svc.RecordConversion(c.Request.Context(), req.FlagKey, userID, req.Variant, req.EventType); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR"}})
		return
	}
	c.Status(http.StatusCreated)
}

// GetExperimentResults returns aggregated A/B results for a flag (admin only).
func (h *Handler) GetExperimentResults(c *gin.Context) {
	scopes := c.GetHeader("X-Scopes")
	if !hasScope(scopes, "admin") && !hasScope(scopes, "superadmin") {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "FORBIDDEN"}})
		return
	}
	key := c.Param("key")
	results, err := h.svc.GetExperimentResults(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR"}})
		return
	}
	c.JSON(http.StatusOK, results)
}
