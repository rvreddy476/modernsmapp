package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/store/scylla"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DashboardHandler struct {
	aggStore   *postgres.AggregateStore
	watchStore *scylla.WatchStore
}

func NewDashboardHandler(aggStore *postgres.AggregateStore) *DashboardHandler {
	return &DashboardHandler{aggStore: aggStore}
}

// WithWatchStore wires the scylla watch-session store, used by the
// retention-curve endpoint. Optional — nil means /retention returns
// 503 with a clear "scylla unavailable" message.
func (h *DashboardHandler) WithWatchStore(ws *scylla.WatchStore) *DashboardHandler {
	h.watchStore = ws
	return h
}

func (h *DashboardHandler) RegisterRoutes(v1 *gin.RouterGroup) {
	dash := v1.Group("/dashboard")
	{
		dash.GET("/overview", h.GetOverview)
		dash.GET("/content", h.GetContentList)
		dash.GET("/content/:contentId", h.GetContentDetail)
		dash.GET("/content/:contentId/trend", h.GetContentTrend)
		dash.GET("/content/:contentId/retention", h.GetContentRetention)
		dash.GET("/trend", h.GetCreatorTrend)
	}
}

// GetContentRetention returns the audience retention curve for one
// piece of content — the share of sessions still watching at each
// `bucket_sec`-second mark. Used by the studio "retention" chart on
// the per-post analytics drawer.
//
// Query params:
//
//	bucket_sec   default 1; 1 for short flicks, 5+ for long-form
//	max_buckets  default 120 (i.e. 2 minutes at 1s buckets); cap 600
//
// Empty result (no sessions yet) → 200 with []. Scylla unconfigured →
// 503 so the studio can render a friendly "data not yet available"
// state instead of a generic 500.
func (h *DashboardHandler) GetContentRetention(c *gin.Context) {
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid content ID", nil)
		return
	}
	if h.watchStore == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "ANALYTICS_UNAVAILABLE", "Retention data is not yet available for this deployment", nil)
		return
	}

	bucketSec := int64(1)
	if v := c.Query("bucket_sec"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			bucketSec = n
		}
	}
	maxBuckets := 120
	if v := c.Query("max_buckets"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxBuckets = n
		}
	}

	curve, err := h.watchStore.GetRetentionCurve(c.Request.Context(), contentID, bucketSec, maxBuckets)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute retention curve", nil)
		return
	}
	if curve == nil {
		curve = []scylla.RetentionPoint{}
	}
	api.JSON(c.Writer, http.StatusOK, curve, nil)
}

func (h *DashboardHandler) GetOverview(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-User-Id", nil)
		return
	}
	creatorID, err := uuid.Parse(userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}

	period := c.DefaultQuery("period", "7d")
	since := parsePeriod(period)

	overview, err := h.aggStore.GetCreatorOverview(c.Request.Context(), creatorID, since)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch overview", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, overview, nil)
}

func (h *DashboardHandler) GetContentList(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-User-Id", nil)
		return
	}
	creatorID, _ := uuid.Parse(userID)

	sortBy := c.DefaultQuery("sort", "views_display")
	limit := 20
	cursor := time.Now()

	if cursorStr := c.Query("cursor"); cursorStr != "" {
		if t, err := time.Parse(time.RFC3339, cursorStr); err == nil {
			cursor = t
		}
	}

	content, err := h.aggStore.GetContentList(c.Request.Context(), creatorID, limit, cursor, sortBy)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch content list", nil)
		return
	}

	var meta *api.Meta
	if len(content) == limit {
		meta = &api.Meta{NextCursor: content[len(content)-1].CreatedAt.Format(time.RFC3339)}
	}

	api.JSON(c.Writer, http.StatusOK, content, meta)
}

func (h *DashboardHandler) GetContentDetail(c *gin.Context) {
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid content ID", nil)
		return
	}

	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetContentHourlyTrend(c.Request.Context(), contentID, since)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch content detail", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, trend, nil)
}

func (h *DashboardHandler) GetContentTrend(c *gin.Context) {
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid content ID", nil)
		return
	}

	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetContentHourlyTrend(c.Request.Context(), contentID, since)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trend", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, trend, nil)
}

func (h *DashboardHandler) GetCreatorTrend(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-User-Id", nil)
		return
	}
	creatorID, _ := uuid.Parse(userID)
	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetCreatorDailyTrend(c.Request.Context(), creatorID, since)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trend", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, trend, nil)
}

func parsePeriod(period string) time.Time {
	switch period {
	case "30d":
		return time.Now().AddDate(0, 0, -30)
	case "90d":
		return time.Now().AddDate(0, 0, -90)
	default: // 7d
		return time.Now().AddDate(0, 0, -7)
	}
}
