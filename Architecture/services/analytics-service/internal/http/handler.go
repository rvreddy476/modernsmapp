package http

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/atpost/analytics-service/internal/service"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc            *service.IngestService
	creatorService *service.CreatorService
	rdb            *redis.Client
	internalKey    string
}

func New(svc *service.IngestService, rdb *redis.Client) *Handler {
	return &Handler{svc: svc, rdb: rdb}
}

// WithCreatorService sets the creator analytics service.
func (h *Handler) WithCreatorService(cs *service.CreatorService) *Handler {
	h.creatorService = cs
	return h
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	v1 := r.Group("/v1/analytics")
	{
		v1.POST("/events", h.IngestEvents)
		v1.GET("/content/:contentId/views", h.GetContentViews)
		v1.GET("/creator/me", h.GetMyCreatorStats)
	}
}

func (h *Handler) GetMyCreatorStats(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHORIZED"}})
		return
	}
	if h.creatorService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "SERVICE_UNAVAILABLE"}})
		return
	}
	period := c.DefaultQuery("period", "30d")
	stats, err := h.creatorService.GetStats(c.Request.Context(), userID, period)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *Handler) GetCreatorStats(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_REQUEST"}})
		return
	}
	if h.creatorService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "SERVICE_UNAVAILABLE"}})
		return
	}
	period := c.DefaultQuery("period", "30d")
	stats, err := h.creatorService.GetStats(c.Request.Context(), userID, period)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, stats)
}

type IngestRequest struct {
	Events []service.EventDTO `json:"events" binding:"required"`
}

func (h *Handler) IngestEvents(c *gin.Context) {
	var req IngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	// Validate batch size
	if len(req.Events) > 200 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Batch size too large (max 200)", nil)
		return
	}

	userID := c.GetHeader("X-User-Id")
	result, err := h.svc.IngestEvents(c.Request.Context(), userID, req.Events)
	if err != nil {
		log.Printf("Ingest error: %v", err)
		if errors.Is(err, pgstore.ErrContentNotProjected) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "CONTENT_NOT_READY", "Content analytics is not ready yet", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ANALYTICS_EVENT", err.Error(), nil)
		return
	}

	// The event is already durable; 202 describes downstream aggregation,
	// not an in-memory acceptance queue.
	api.JSON(c.Writer, http.StatusAccepted, result, nil)
}

// GetContentViews returns real-time view counts for a specific content item.
// Reads from Redis post:views:{contentId} hash.
func (h *Handler) GetContentViews(c *gin.Context) {
	contentID := c.Param("contentId")
	if contentID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "content_id is required", nil)
		return
	}

	result, err := h.rdb.HGetAll(c.Request.Context(), "post:views:"+contentID).Result()
	if err != nil {
		log.Printf("Redis error fetching views for %s: %v", contentID, err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch view counts", nil)
		return
	}

	counts := make(map[string]int64)
	for k, v := range result {
		n, _ := strconv.ParseInt(v, 10, 64)
		counts[k] = n
	}

	// Ensure all expected fields exist with zero defaults
	for _, field := range []string{"display", "views_1s", "views_3s", "views_10s", "views_30s", "views_60s"} {
		if _, ok := counts[field]; !ok {
			counts[field] = 0
		}
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}
