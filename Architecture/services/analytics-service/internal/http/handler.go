package http

import (
	"log"
	"net/http"

	"github.com/facebook-like/analytics-service/internal/service"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *service.IngestService
}

func New(svc *service.IngestService) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/analytics")
	{
		v1.POST("/events", h.IngestEvents)
	}
}

type IngestRequest struct {
	Events []service.EventDTO `json:"events" binding:"required"`
}

func (h *Handler) IngestEvents(c *gin.Context) {
	var req IngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// Validate batch size
	if len(req.Events) > 200 {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Batch size too large (max 200)", nil, nil)
		return
	}

	userID := c.GetHeader("X-User-Id")
	sessionID := c.GetHeader("X-Session-Id")

	// Async ingest
	if err := h.svc.IngestEvents(c.Request.Context(), userID, sessionID, req.Events); err != nil {
		log.Printf("Ingest error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Ingestion failed", nil, nil)
		return
	}

	// 202 Accepted because processing is async
	api.JSON(c.Writer, http.StatusAccepted, map[string]string{"status": "accepted"}, nil)
}
