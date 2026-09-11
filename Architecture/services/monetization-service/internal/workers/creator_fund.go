package workers

import (
	"context"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// runCreatorFundAccrual measures yesterday's fund earnings every day at
// 03:00 UTC and records them as accruals. It moves NO money — that is the
// whole point of the change: capture stays continuous, payment is
// periodic. The 3-hour offset gives the analytics rollup worker (which
// runs at midnight) time to land yesterday's content_daily_summary rows
// before we read them. Re-running is safe because each (creator, day,
// content_type, region) row is uniquely keyed.
//
// The accrual is not load-bearing for correctness: the period settlement
// accrues every day in its window itself before paying. This worker just
// means a creator's dashboard fills in daily instead of jumping once a
// month, and that the monthly run has almost nothing left to compute.
func runCreatorFundAccrual(ctx context.Context, svc *service.Service) {
	timer := time.NewTimer(durationUntilNext(3, 0))
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			accrueCreatorFundYesterday(ctx, svc)
			timer.Reset(durationUntilNext(3, 0))
		}
	}
}

func accrueCreatorFundYesterday(ctx context.Context, svc *service.Service) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	day := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)

	slog.Info("creator-fund accrual: starting", "day", day.Format("2006-01-02"))

	logRow := func(creatorID uuid.UUID, res service.DayAccrual, err error) {
		if err != nil {
			// ErrInputRevisionChanged lands here: an accrued day whose
			// analytics rows moved. It is not retried; it is the trigger
			// for a correction through the admin reversal route.
			slog.Warn("creator-fund accrual: creator failed",
				"creator_id", creatorID, "error", err)
			return
		}
		if res.Accrued > 0 || len(res.Skipped) > 0 {
			slog.Info("creator-fund accrual: recorded",
				"creator_id", creatorID, "rows_accrued", res.Accrued, "skipped", res.Skipped)
		}
	}

	batch, err := svc.AccrueCreatorFundDayForAllEligible(ctx, day, logRow)
	if err != nil {
		slog.Error("creator-fund accrual: batch failed", "error", err)
		return
	}
	slog.Info("creator-fund accrual: completed",
		"day", day.Format("2006-01-02"), "rows_accrued", batch.Accrued,
		"creators", batch.Creators, "failed", batch.Failed, "skipped", batch.Skipped)
}

// runCreatorFundPeriodSettlement is the payment run — the thing the
// founder asked for and the thing that did not exist. It wakes hourly and
// asks one question: has the most recently closed settlement period been
// paid?
//
// Why hourly-and-idempotent rather than a cron firing once:
//
//   - A pod that was down at 04:00 on the 1st would, with a one-shot
//     schedule, simply never pay that month. Re-asking every hour means
//     the run happens as soon as *some* pod is alive.
//   - Re-asking is free once the period is settled: SettleCreatorFundPeriod
//     finds no uncredited accruals, credits zero, and returns the stored
//     statement. The scheduled run and the admin re-run are the same call.
//
// Safety with several instances is layered, and the lock is the least
// important layer:
//
//  1. pg_try_advisory_lock on a hash of the period key — stops two pods
//     doing the same work at the same time. An optimisation.
//  2. UNIQUE (creator_id, period_key, region_code) on the settlement row —
//     two pods cannot create two settlements for one period.
//  3. creator_fund_earnings.credited flipped false->true inside the same
//     transaction as the wallet credit — this is the one that actually
//     prevents a double pay. Even with the lock lost, the clock skewed and
//     both pods past the UNIQUE check, exactly one UPDATE claims each day's
//     row and only that pod credits it.
func runCreatorFundPeriodSettlement(ctx context.Context, store *postgres.Store, svc *service.Service) {
	cfg := svc.CreatorFundConfigSnapshot()
	cadence := service.NormalizeCadence(cfg.SettlementCadence)
	slog.Info("creator-fund period settlement worker started",
		"cadence", cadence, "lag_days", cfg.SettlementLagDays, "hour_utc", cfg.SettlementHourUTC)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			maybeSettleClosedPeriod(ctx, store, svc)
		}
	}
}

func maybeSettleClosedPeriod(ctx context.Context, store *postgres.Store, svc *service.Service) {
	cfg := svc.CreatorFundConfigSnapshot()
	now := time.Now().UTC()
	period := service.PreviousPeriod(now, cfg.SettlementCadence)

	// Wait for the lag: analytics for the period's last day has to have
	// been rolled up before we price it.
	if now.Before(settlementReadyAt(period, cfg.SettlementLagDays, cfg.SettlementHourUTC)) {
		return
	}

	locked, release, err := store.TryAdvisorySettlementLock(ctx, settlementLockKey(period.Key))
	if err != nil {
		slog.Warn("creator-fund settlement: advisory lock failed", "error", err)
		return
	}
	if !locked {
		slog.Debug("creator-fund settlement: another instance holds the period lock", "period", period.Key)
		return
	}
	defer release()

	res, err := svc.SettleCreatorFundPeriodForAll(ctx, period, func(creatorID uuid.UUID, st *service.PeriodStatement, err error) {
		if err != nil {
			slog.Warn("creator-fund settlement: creator failed",
				"creator_id", creatorID, "period", period.Key, "error", err)
			return
		}
		if st != nil && st.NewlyCredited {
			slog.Info("creator-fund settlement: credited",
				"creator_id", creatorID, "period", period.Key,
				"credited_paise", st.CreditedPaise,
				"gross_paise", st.GrossPaise)
		}
	})
	if err != nil {
		slog.Error("creator-fund settlement: batch failed", "period", period.Key, "error", err)
		return
	}
	if res.NewlyCreditedCreators == 0 {
		// The steady state after the first successful run of a period.
		slog.Debug("creator-fund settlement: period already settled",
			"period", period.Key, "creators", res.Considered)
		return
	}
	slog.Info("creator-fund settlement: completed",
		"period", period.Key,
		"creators_considered", res.Considered,
		"creators_credited", res.NewlyCreditedCreators,
		"credited_paise", res.CreditedPaise,
		"period_gross_paise", res.GrossPaise)
}

// settlementReadyAt is the earliest moment a closed period may be paid:
// lagDays after it ends, at hourUTC. The lag exists because the last
// day's analytics has to be rolled up before it can be priced — paying
// at 00:00 on the 1st would settle the 31st against an empty rollup.
func settlementReadyAt(period service.SettlementPeriod, lagDays, hourUTC int) time.Time {
	return period.End.AddDate(0, 0, lagDays).Add(time.Duration(hourUTC) * time.Hour)
}

// settlementLockKey hashes the period key into the int64 advisory-lock
// namespace. Collisions across periods would only ever cost a delayed
// run, never a wrong payment.
func settlementLockKey(periodKey string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("creator_fund_settlement:" + periodKey))
	return int64(h.Sum64() >> 1)
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
