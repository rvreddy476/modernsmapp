package http

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/atpost/analytics-service/internal/aggregation"
	"github.com/gin-gonic/gin"
)

// The aggregation pipeline's operator surface.
//
// Aggregation runs on two timers: the hourly aggregator rebuilds the
// recent hours every five minutes, and the daily rollup walks every open
// day from its watermark to yesterday every fifteen minutes. That is
// right for production and leaves two things impossible:
//
//   - rebuilding a specific day now, after an outage or after a bad
//     hour was corrected, without waiting for the next pass;
//   - measuring today at all, which the creator-fund settlement needs
//     to do when an operator settles a period by hand.
//
// Both are just "run the same idempotent recompute for the range I name".
// The rollup is delete-then-insert inside one transaction and the hourly
// pass recomputes each bucket under an advisory lock, so calling these
// repeatedly converges rather than accumulating. Nothing here invents a
// number: it only re-derives what the timers would have derived on
// their own schedule.
//
// Except for one thing the timers will never do: rewrite a frozen day.
// A day older than the 48-hour window is refused with 409 DAY_FROZEN,
// because money may have settled on its numbers. ?force=1 is the only
// way through; it is logged loudly, and any settlement on the previous
// numbers has to be reconciled through monetization's reversal path.
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
//	?force=1          rewrite even if the day is frozen. Loud.
//
// Synchronous, because the caller's next action is to read the numbers.
func (h *Handler) RunAggregation(c *gin.Context) {
	if h.hourlyAgg == nil || h.dailyRollup == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code": "SERVICE_UNAVAILABLE", "message": "aggregation ops not configured"}})
		return
	}
	ctx := c.Request.Context()
	force := c.Query("force") == "1"
	now := time.Now().UTC()

	if hourStr := c.Query("hour"); hourStr != "" {
		hour, err := time.Parse(time.RFC3339, hourStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"code": "BAD_REQUEST", "message": "hour must be RFC3339"}})
			return
		}
		hour = hour.UTC().Truncate(time.Hour)
		if aggregation.IsFrozen(hour, now) && !force {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{
				"code":    "DAY_FROZEN",
				"message": "the hour's day is outside the 48h reprocessing window; pass force=1 to rewrite it"}})
			return
		}
		if aggregation.IsFrozen(hour, now) {
			log.Printf("[AggregationOps] FORCED hourly rebuild of frozen bucket %s", hour.Format(time.RFC3339))
		}
		h.hourlyAgg.AggregateHour(ctx, hour)
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"hour":          hour.Format(time.RFC3339),
			"rolled_up":     false,
			"hours_rebuilt": 1,
			"forced":        force,
		}})
		return
	}

	day := now.Truncate(24 * time.Hour)
	if dayStr := c.Query("day"); dayStr != "" {
		parsed, err := time.Parse("2006-01-02", dayStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"code": "BAD_REQUEST", "message": "day must be YYYY-MM-DD"}})
			return
		}
		day = parsed.UTC().Truncate(24 * time.Hour)
	}
	frozen := aggregation.IsFrozen(day, now)
	if frozen && !force {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{
			"code":    "DAY_FROZEN",
			"message": "day " + day.Format("2006-01-02") + " is outside the 48h reprocessing window; pass force=1 to rewrite it"}})
		return
	}
	if frozen {
		log.Printf("[AggregationOps] FORCED rebuild of frozen day %s requested via internal aggregate route", day.Format("2006-01-02"))
	}

	// Rebuild every hour of the day, not only the ones the timer would
	// have touched — the point of naming a day is that its hours may be
	// stale or missing entirely.
	hours := 0
	for hr := day; hr.Before(day.AddDate(0, 0, 1)); hr = hr.Add(time.Hour) {
		h.hourlyAgg.AggregateHour(ctx, hr)
		hours++
	}
	var err error
	if force {
		err = h.dailyRollup.ForceRollupDay(ctx, day)
	} else {
		err = h.dailyRollup.RollupDay(ctx, day)
	}
	if errors.Is(err, aggregation.ErrDayFrozen) {
		// The day froze between the check above and the rollup.
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{
			"code": "DAY_FROZEN", "message": err.Error()}})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "ROLLUP_FAILED", "message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"day":           day.Format("2006-01-02"),
		"hours_rebuilt": hours,
		"rolled_up":     true,
		"forced":        force,
	}})
}
