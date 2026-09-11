package http

import (
	"errors"
	"log"
	"net/http"

	"github.com/atpost/analytics-service/internal/aggregation"
	"github.com/atpost/analytics-service/internal/personalization"
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
	aggStore       *pgstore.AggregateStore
	rdb            *redis.Client
	internalKey    string
	// personalization is the viewer-signal warmer, exposed for on-demand
	// runs under /v1/analytics/internal/. Optional — see personalization.go.
	personalization *personalization.Warmer
	// hourlyAgg/dailyRollup back the on-demand aggregation route in
	// aggregation_ops.go. Optional.
	hourlyAgg   *aggregation.HourlyAggregator
	dailyRollup *aggregation.DailyRollup
}

// WithAggregateStore wires the durable aggregate store that backs the
// content view counters when the Redis real-time cache is cold or
// unavailable. Optional — nil keeps the historical Redis-only behaviour.
func (h *Handler) WithAggregateStore(s *pgstore.AggregateStore) *Handler {
	h.aggStore = s
	return h
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

		// Viewer-signal warmer: run it now, or ask when it last ran.
		// Registered only when the warmer is wired. See
		// personalization.go for why these are under /internal/.
		if h.personalization != nil {
			v1.POST("/internal/personalization/run", h.RunPersonalization)
			v1.GET("/internal/personalization", h.PersonalizationStatus)
		}

		// On-demand aggregation: rebuild an hour or a whole day now,
		// instead of waiting for the timer. See aggregation_ops.go.
		if h.hourlyAgg != nil && h.dailyRollup != nil {
			v1.POST("/internal/aggregate", h.RunAggregation)
		}

		// The visible view count post-service and feed-service put on a
		// post, in one batch read per page. See content_views.go.
		if h.aggStore != nil {
			v1.POST("/internal/content-views", h.ContentViewsBatch)
		}
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

// GetContentViews returns the view-bucket counters for one content item
// from content_hourly_agg, rebuilt from the ingested milestone and
// play_end events. It used to consult a Redis post:views:{contentId}
// hash first; that hash was written only by the Kafka VideoViewConsumer,
// which subscribed to events nothing produced, so it was always empty
// (plan 5B, issue M-13). The aggregate is the only source now. Response
// shape is unchanged — the same six fields.
func (h *Handler) GetContentViews(c *gin.Context) {
	contentID := c.Param("contentId")
	if contentID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "content_id is required", nil)
		return
	}

	counts := make(map[string]int64)
	if h.aggStore != nil {
		if parsed, err := uuid.Parse(contentID); err == nil {
			buckets, bErr := h.aggStore.GetContentViewBuckets(c.Request.Context(), parsed)
			if bErr != nil {
				log.Printf("Aggregate view lookup failed for %s: %v", contentID, bErr)
			} else {
				counts["display"] = buckets.Display
				counts["views_1s"] = buckets.Views1s
				counts["views_3s"] = buckets.Views3s
				counts["views_10s"] = buckets.Views10s
				counts["views_30s"] = buckets.Views30s
				counts["views_60s"] = buckets.Views60s
			}
		}
	}

	// Ensure all expected fields exist with zero defaults
	for _, field := range []string{"display", "views_1s", "views_3s", "views_10s", "views_30s", "views_60s"} {
		if _, ok := counts[field]; !ok {
			counts[field] = 0
		}
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}
