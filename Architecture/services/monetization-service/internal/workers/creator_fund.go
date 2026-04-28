package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/google/uuid"
)

// runCreatorFundEarnings settles yesterday's earnings every day at
// 03:00 UTC. The 3-hour offset gives the analytics rollup worker (which
// runs at midnight) time to land yesterday's content_daily_summary rows
// before we read them. A re-run after a crash is safe because each
// (creator, day, content_type, region) row is uniquely keyed.
func runCreatorFundEarnings(ctx context.Context, svc *service.Service) {
	timer := time.NewTimer(durationUntilNext(3, 0))
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			settleCreatorFundYesterday(ctx, svc)
			timer.Reset(durationUntilNext(3, 0))
		}
	}
}

func settleCreatorFundYesterday(ctx context.Context, svc *service.Service) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	day := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)

	slog.Info("creator-fund settlement: starting", "day", day.Format("2006-01-02"))

	logRow := func(creatorID uuid.UUID, credited int, err error) {
		if err != nil {
			slog.Warn("creator-fund settlement: creator failed",
				"creator_id", creatorID, "error", err)
			return
		}
		if credited > 0 {
			slog.Info("creator-fund settlement: credited",
				"creator_id", creatorID, "rows_credited", credited)
		}
	}

	total, err := svc.SettleCreatorFundDayForAllEligible(ctx, day, logRow)
	if err != nil {
		slog.Error("creator-fund settlement: batch failed", "error", err)
		return
	}
	slog.Info("creator-fund settlement: completed",
		"day", day.Format("2006-01-02"), "rows_credited", total)
}

// runEligibilityEvaluator re-rates creators whose evaluation has gone
// stale every day at 02:00 UTC. Sweep size is bounded so a one-time
// backlog doesn't blow out a single tick.
func runEligibilityEvaluator(ctx context.Context, svc *service.Service) {
	timer := time.NewTimer(durationUntilNext(2, 0))
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			sweepEligibility(ctx, svc)
			timer.Reset(durationUntilNext(2, 0))
		}
	}
}

func sweepEligibility(ctx context.Context, svc *service.Service) {
	slog.Info("creator-fund eligibility sweep: starting")
	logRow := func(creatorID uuid.UUID, status string, err error) {
		if err != nil {
			slog.Warn("eligibility sweep: creator failed",
				"creator_id", creatorID, "error", err)
			return
		}
		slog.Debug("eligibility sweep: evaluated",
			"creator_id", creatorID, "status", status)
	}
	count, err := svc.SweepEligibility(ctx, logRow)
	if err != nil {
		slog.Error("eligibility sweep: failed", "error", err)
		return
	}
	slog.Info("creator-fund eligibility sweep: completed", "evaluated", count)
}

// durationUntilNext returns the time.Duration from now until the next
// occurrence of the given UTC hour:minute. Used so workers fire on a
// predictable wall-clock schedule rather than drifting from process
// start time.
func durationUntilNext(hour, minute int) time.Duration {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return time.Until(next)
}
