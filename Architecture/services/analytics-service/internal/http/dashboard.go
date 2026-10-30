package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/store/scylla"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// watchCharts is the slice of the Scylla watch store the retention and
// demographics endpoints read; an interface so the cache in front of it
// is testable without a cluster.
type watchCharts interface {
	GetRetentionCurve(ctx context.Context, contentID uuid.UUID, bucketSec int64, maxBuckets int) ([]scylla.RetentionPoint, bool, error)
	GetAudienceDemographics(ctx context.Context, contentID uuid.UUID, topN int) (*scylla.AudienceDemographics, error)
}

type DashboardHandler struct {
	aggStore   *postgres.AggregateStore
	watchStore watchCharts
	charts     *chartCache
	// owns is the ownership check behind every per-content route;
	// aggStore.OwnsContent outside tests.
	owns func(ctx context.Context, creatorID, contentID uuid.UUID) (bool, error)
}

func NewDashboardHandler(aggStore *postgres.AggregateStore) *DashboardHandler {
	return &DashboardHandler{
		aggStore: aggStore,
		charts:   newChartCache(chartCacheTTL, chartCacheMaxEntries),
		owns:     aggStore.OwnsContent,
	}
}

// WithWatchStore wires the scylla watch-session store, used by the
// retention-curve endpoint. Optional — nil means /retention returns
// 503 with a clear "scylla unavailable" message.
func (h *DashboardHandler) WithWatchStore(ws *scylla.WatchStore) *DashboardHandler {
	if ws != nil {
		h.watchStore = ws
	}
	return h
}

// sampledHeader tells the studio a chart was computed from a sample of a
// very large partition (audit M9); see scylla.chartScanLimit. The
// retention body is a bare array, so the flag rides as a header there and
// on demographics alike.
const sampledHeader = "X-Analytics-Sampled"

func (h *DashboardHandler) RegisterRoutes(v1 *gin.RouterGroup) {
	dash := v1.Group("/dashboard")
	{
		dash.GET("/overview", h.GetOverview)
		dash.GET("/content", h.GetContentList)
		dash.GET("/content/:contentId", h.GetContentDetail)
		dash.GET("/content/:contentId/trend", h.GetContentTrend)
		dash.GET("/content/:contentId/retention", h.GetContentRetention)
		dash.GET("/content/:contentId/demographics", h.GetContentDemographics)
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
	contentID, ok := h.requireOwnedContent(c)
	if !ok {
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

	key := fmt.Sprintf("retention|%s|%d|%d", contentID, bucketSec, maxBuckets)
	value, sampled, err := h.charts.get(c.Request.Context(), key, func(ctx context.Context) (any, bool, error) {
		return h.watchStore.GetRetentionCurve(ctx, contentID, bucketSec, maxBuckets)
	})
	if err != nil {
		// The response stays opaque (this is a private, per-content
		// surface), but the cause has to reach the operator: a bare
		// "Failed to compute retention curve" with no log line is the
		// reason this endpoint could 500 unexplained.
		slog.Error("retention curve failed", "content_id", contentID.String(), "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute retention curve", nil)
		return
	}
	curve, _ := value.([]scylla.RetentionPoint)
	if curve == nil {
		curve = []scylla.RetentionPoint{}
	}
	if sampled {
		c.Writer.Header().Set(sampledHeader, "true") // retention
	}
	api.JSON(c.Writer, http.StatusOK, curve, nil)
}

// GetContentDemographics returns a top-N country + surface breakdown
// for one piece of content. Powers the studio's "Who's watching"
// drawer.
//
// Query params:
//
//	top_n  default 10; max 50 (per dimension)
//
// Same "scylla unconfigured → 503" contract as the retention endpoint.
func (h *DashboardHandler) GetContentDemographics(c *gin.Context) {
	contentID, ok := h.requireOwnedContent(c)
	if !ok {
		return
	}
	if h.watchStore == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "ANALYTICS_UNAVAILABLE", "Audience data is not yet available for this deployment", nil)
		return
	}
	topN := 10
	if v := c.Query("top_n"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topN = n
		}
	}
	key := fmt.Sprintf("demographics|%s|%d", contentID, topN)
	value, sampled, err := h.charts.get(c.Request.Context(), key, func(ctx context.Context) (any, bool, error) {
		dem, err := h.watchStore.GetAudienceDemographics(ctx, contentID, topN)
		if err != nil || dem == nil {
			return dem, false, err
		}
		return dem, dem.Sampled, nil
	})
	if err != nil {
		slog.Error("audience demographics failed", "content_id", contentID.String(), "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute demographics", nil)
		return
	}
	if sampled {
		c.Writer.Header().Set(sampledHeader, "true") // demographics
	}
	api.JSON(c.Writer, http.StatusOK, value, nil)
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
	creatorID, err := uuid.Parse(userID)
	if err != nil || creatorID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

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
	contentID, ok := h.requireOwnedContent(c)
	if !ok {
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
	contentID, ok := h.requireOwnedContent(c)
	if !ok {
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
	creatorID, err := uuid.Parse(userID)
	if err != nil || creatorID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetCreatorDailyTrend(c.Request.Context(), creatorID, since)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trend", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, trend, nil)
}

// requireOwnedContent is the owner-privacy gate for the dashboard routes.
// The decision lives in requireOwnedContentID (content_insights.go) so the
// MTube content route and these share one implementation.
func (h *DashboardHandler) requireOwnedContent(c *gin.Context) (uuid.UUID, bool) {
	return requireOwnedContentID(c, h.owns)
}

func writePrivateContentNotFound(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Analytics content not found", nil)
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
