package http

import (
	"net/http"
	"time"

	"github.com/facebook-like/analytics-service/internal/store/postgres"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DashboardHandler struct {
	aggStore *postgres.AggregateStore
}

func NewDashboardHandler(aggStore *postgres.AggregateStore) *DashboardHandler {
	return &DashboardHandler{aggStore: aggStore}
}

func (h *DashboardHandler) RegisterRoutes(v1 *gin.RouterGroup) {
	dash := v1.Group("/dashboard")
	{
		dash.GET("/overview", h.GetOverview)
		dash.GET("/content", h.GetContentList)
		dash.GET("/content/:contentId", h.GetContentDetail)
		dash.GET("/content/:contentId/trend", h.GetContentTrend)
		dash.GET("/trend", h.GetCreatorTrend)
	}
}

func (h *DashboardHandler) GetOverview(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-User-Id", nil, nil)
		return
	}
	creatorID, err := uuid.Parse(userID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil, nil)
		return
	}

	period := c.DefaultQuery("period", "7d")
	since := parsePeriod(period)

	overview, err := h.aggStore.GetCreatorOverview(c.Request.Context(), creatorID, since)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch overview", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, overview, nil)
}

func (h *DashboardHandler) GetContentList(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-User-Id", nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch content list", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid content ID", nil, nil)
		return
	}

	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetContentHourlyTrend(c.Request.Context(), contentID, since)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch content detail", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, trend, nil)
}

func (h *DashboardHandler) GetContentTrend(c *gin.Context) {
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid content ID", nil, nil)
		return
	}

	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetContentHourlyTrend(c.Request.Context(), contentID, since)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trend", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, trend, nil)
}

func (h *DashboardHandler) GetCreatorTrend(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-User-Id", nil, nil)
		return
	}
	creatorID, _ := uuid.Parse(userID)
	since := parsePeriod(c.DefaultQuery("period", "7d"))

	trend, err := h.aggStore.GetCreatorDailyTrend(c.Request.Context(), creatorID, since)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trend", nil, nil)
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
