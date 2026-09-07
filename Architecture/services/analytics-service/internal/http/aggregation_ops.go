package http

import (
	"net/http"
	"time"

	"github.com/atpost/analytics-service/internal/aggregation"
	"github.com/gin-gonic/gin"
)

// The aggregation pipeline's operator surface.
//
// Aggregation runs on two timers: the hourly aggregator rebuilds the
// current and previous hour every five minutes, and the daily rollup
// folds the *previous* day into analytics.content_daily_summary in the
// first hour of each UTC day. That is right for production and leaves
// two things impossible:
//
//   - rebuilding a day that is not yesterday, after an outage or after a
//     bad hour was corrected;
//   - measuring today at all, which the creator-fund settlement now
//     needs to do when an operator settles a period by hand.
//
// Both are just "run the same idempotent recompute for the range I name".
// The rollup is INSERT ... ON CONFLICT DO UPDATE and the hourly pass
// recomputes each bucket from events_raw under an advisory lock, so
// calling these repeatedly converges rather than accumulating. Nothing
// here invents a number: it only re-derives what the timers would have
// derived on their own schedule.
//
// Under /internal/ for the same reason as the personalization routes: the
// gateway refuses /internal/ paths without an admin scope, and every /v1
// route here already requires the shared internal-service key.

// WithAggregationOps wires the two aggregators for on-demand runs.
// Optional: without them the routes are not registered at all.
func (h *Handler) WithAggregationOps(hourly *aggregation.HourlyAggregator, daily *aggregation.DailyRollup) *Handler {
	h.hourlyAgg = hourly
	h.dailyRollup = daily
	return h
}

// RunAggregation is POST /v1/analytics/internal/aggregate.
//
//	?day=YYYY-MM-DD   rebuild every hour of that UTC day, then roll the
//	                  day up into content_daily_summary. Defaults to today.
//	?hour=RFC3339     rebuild just that one hour and skip the rollup.
//
// Synchronous, because the caller's next action is to read the numbers.
func (h *Handler) RunAggregation(c *gin.Context) {
	if h.hourlyAgg == nil || h.dailyRollup == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code": "SERVICE_UNAVAILABLE", "message": "aggregation ops not configured"}})
		return
	}
	ctx := c.Request.Context()

	if hourStr := c.Query("hour"); hourStr != "" {
		hour, err := time.Parse(time.RFC3339, hourStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"code": "BAD_REQUEST", "message": "hour must be RFC3339"}})
			return
		}
		h.hourlyAgg.AggregateHour(ctx, hour)
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"hour":          hour.UTC().Truncate(time.Hour).Format(time.RFC3339),
			"rolled_up":     false,
			"hours_rebuilt": 1,
		}})
		return
	}

	day := time.Now().UTC().Truncate(24 * time.Hour)
	if dayStr := c.Query("day"); dayStr != "" {
		parsed, err := time.Parse("2006-01-02", dayStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"code": "BAD_REQUEST", "message": "day must be YYYY-MM-DD"}})
			return
		}
		day = parsed.UTC().Truncate(24 * time.Hour)
	}
	// Rebuild every hour of the day, not only the ones the timer would
	// have touched — the point of naming a day is that its hours may be
	// stale or missing entirely.
	hours := 0
	for hr := day; hr.Before(day.AddDate(0, 0, 1)); hr = hr.Add(time.Hour) {
		h.hourlyAgg.AggregateHour(ctx, hr)
		hours++
	}
	if err := h.dailyRollup.RollupDay(ctx, day); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "ROLLUP_FAILED", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"day":           day.Format("2006-01-02"),
		"hours_rebuilt": hours,
		"rolled_up":     true,
	}})
}
