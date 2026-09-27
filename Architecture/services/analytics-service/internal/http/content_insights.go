package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube content insights (2026-09-27).
//
//	GET /v1/analytics/content/:contentId?period=7d|28d|90d|365d   (default 28d)
//	  200 {"data": {
//	         "content_id", "period",
//	         "views_by_day":            [{"day":"YYYY-MM-DD","views":n}]   dense, oldest first
//	         "average_view_duration_ms": float,
//	         "average_percent_viewed":   float (0..100),
//	         "retention":                [100 floats in 0..1],
//	         "traffic":                  [{"surface","views"}]
//	       }}
//	  404 NOT_FOUND   not the owner and not an admin — identical to an unknown id
//	  401 UNAUTHORIZED no verified user
//
// The read is private to the content's owner (analytics.content_ownership,
// the same gate the dashboard routes use) or to an admin: the gateway
// stamps X-Admin-Role from the token's scopes and strips it from clients,
// so its presence is the gateway's word, not the caller's.
//
// Views are display views. The retention curve is computed from the
// coverage bitmaps of finalized sessions in the period (see
// postgres.RetentionBuckets); averages from the same sessions.

// contentInsightsStore is the slice of the aggregate store this route
// reads; an interface so the gate and the shape are testable without
// PostgreSQL.
type contentInsightsStore interface {
	OwnsContent(ctx context.Context, creatorID, contentID uuid.UUID) (bool, error)
	GetContentViewsByDay(ctx context.Context, contentID uuid.UUID, since, now time.Time) ([]postgres.DayViews, error)
	GetContentSessionCoverage(ctx context.Context, contentID uuid.UUID, since time.Time) ([]postgres.SessionCoverage, error)
	GetContentTrafficBySurface(ctx context.Context, contentID uuid.UUID, since time.Time) ([]postgres.SurfaceViews, error)
}

// ContentInsights is the response body.
type ContentInsights struct {
	ContentID             uuid.UUID               `json:"content_id"`
	Period                string                  `json:"period"`
	ViewsByDay            []postgres.DayViews     `json:"views_by_day"`
	AverageViewDurationMS float64                 `json:"average_view_duration_ms"`
	AveragePercentViewed  float64                 `json:"average_percent_viewed"`
	Retention             []float64               `json:"retention"`
	Traffic               []postgres.SurfaceViews `json:"traffic"`
}

// retentionBuckets is the fixed length of the retention array.
const retentionBuckets = 100

// insightsPeriodDays is the MTube period set; default 28d.
func insightsPeriodDays(period string) (string, int) {
	switch period {
	case "7d":
		return period, 7
	case "28d", "":
		return "28d", 28
	case "90d":
		return period, 90
	case "365d":
		return period, 365
	}
	return "28d", 28
}

// adminRoleHeader is what the gateway stamps for a platform-role holder
// (api-gateway stampAdminRole): the highest of superadmin, admin,
// moderator. Only the first two read another creator's analytics.
const adminRoleHeader = "X-Admin-Role"

func callerIsAdmin(c *gin.Context) bool {
	switch strings.ToLower(strings.TrimSpace(c.GetHeader(adminRoleHeader))) {
	case "superadmin", "admin":
		return true
	}
	return false
}

// requireOwnedContentID is the owner-privacy gate shared with the
// dashboard routes: a verified caller, a content id they own, or the
// same 404 an unknown id gets. Unresolved ownership is a 503, never a yes.
func requireOwnedContentID(c *gin.Context, owns func(ctx context.Context, creatorID, contentID uuid.UUID) (bool, error)) (uuid.UUID, bool) {
	creatorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || creatorID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil || contentID == uuid.Nil {
		writePrivateContentNotFound(c)
		return uuid.Nil, false
	}
	ok, err := owns(c.Request.Context(), creatorID, contentID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "ANALYTICS_UNAVAILABLE", "Analytics authorization is unavailable", nil)
		return uuid.Nil, false
	}
	if !ok {
		writePrivateContentNotFound(c)
		return uuid.Nil, false
	}
	return contentID, true
}

// requireOwnedOrAdminContentID widens the gate to an admin caller. The
// admin still needs a verified user id; the ownership query is skipped,
// not answered yes, so an unknown id is still a 404 for everyone.
func requireOwnedOrAdminContentID(c *gin.Context, store contentInsightsStore) (uuid.UUID, bool) {
	if !callerIsAdmin(c) {
		return requireOwnedContentID(c, store.OwnsContent)
	}
	if _, err := uuid.Parse(c.GetHeader("X-User-Id")); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	contentID, err := uuid.Parse(c.Param("contentId"))
	if err != nil || contentID == uuid.Nil {
		writePrivateContentNotFound(c)
		return uuid.Nil, false
	}
	return contentID, true
}

func (h *Handler) insightsStore() contentInsightsStore {
	if h.insights != nil {
		return h.insights
	}
	if h.aggStore == nil {
		return nil
	}
	return h.aggStore
}

func (h *Handler) insightsNow() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// GetContentInsights — GET /v1/analytics/content/:contentId
func (h *Handler) GetContentInsights(c *gin.Context) {
	store := h.insightsStore()
	if store == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "aggregate store not configured", nil)
		return
	}
	contentID, ok := requireOwnedOrAdminContentID(c, store)
	if !ok {
		return
	}
	period, days := insightsPeriodDays(c.Query("period"))
	now := h.insightsNow()
	since := now.AddDate(0, 0, -days)
	ctx := c.Request.Context()

	byDay, err := store.GetContentViewsByDay(ctx, contentID, since, now)
	if err != nil {
		slog.Error("content insights: views by day", "content_id", contentID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute content insights", nil)
		return
	}
	sessions, err := store.GetContentSessionCoverage(ctx, contentID, since)
	if err != nil {
		slog.Error("content insights: sessions", "content_id", contentID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute content insights", nil)
		return
	}
	traffic, err := store.GetContentTrafficBySurface(ctx, contentID, since)
	if err != nil {
		slog.Error("content insights: traffic", "content_id", contentID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to compute content insights", nil)
		return
	}
	avgWatched, avgPercent := postgres.SessionAverages(sessions)
	if byDay == nil {
		byDay = []postgres.DayViews{}
	}
	if traffic == nil {
		traffic = []postgres.SurfaceViews{}
	}
	api.JSON(c.Writer, http.StatusOK, ContentInsights{
		ContentID:             contentID,
		Period:                period,
		ViewsByDay:            byDay,
		AverageViewDurationMS: avgWatched,
		AveragePercentViewed:  avgPercent,
		Retention:             postgres.RetentionBuckets(sessions, retentionBuckets),
		Traffic:               traffic,
	}, nil)
}
