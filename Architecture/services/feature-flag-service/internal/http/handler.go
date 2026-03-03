package http

import (
	"log"
	"net/http"
	"strings"

	"github.com/atpost/feature-flag-service/internal/service"
	"github.com/atpost/feature-flag-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

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

		admin := v1.Group("/admin/flags")
		admin.Use(h.AdminAuthMiddleware())
		admin.POST("", h.UpsertFlag)
		admin.GET("", h.ListFlags)
	}
}

// AdminAuthMiddleware protects admin endpoints with a scope check.
func (h *Handler) AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		scopes := c.GetHeader("X-Scopes")
		if !strings.Contains(scopes, "admin") {
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

	if err := h.svc.UpsertFlag(c.Request.Context(), &flag); err != nil {
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
