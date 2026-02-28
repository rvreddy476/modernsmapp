package http

import (
	"log"
	"net/http"
	"time"

	"github.com/facebook-like/admin-service/internal/service"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/admin")
	v1.Use(h.AdminAuthMiddleware())
	{
		v1.POST("/takedown", h.TakedownContent)
		v1.POST("/users/:userId/suspend", h.SuspendUser)
	}
}

func (h *Handler) AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("ADMIN_API_KEY")
		// In a real system, this would be a secure secret or IAM check.
		// For V1 MVP, we hardcode a "secret" or check env var.
		if apiKey != "admin-secret-123" {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid API Key", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

type TakedownRequest struct {
	EntityType string `json:"entity_type" binding:"required,oneof=post comment user message"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

func (h *Handler) TakedownContent(c *gin.Context) {
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
