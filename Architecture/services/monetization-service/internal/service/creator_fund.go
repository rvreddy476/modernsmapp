package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tier 3a — Creator Fund.
//
// Eligibility is evaluated nightly using the last 90 days of analytics:
// total view-score, total watch-time, count of distinct earning contents.
// A creator who clears any one threshold is moved to 'eligible'; once
// suspended (manually or by fraud workflow) they stay suspended until an
// admin clears the flag.
//
// Daily earnings are computed once per UTC day, per creator, per content
// type. View counts are pulled from analytics.content_daily_summary,
// multiplied by the active RPM rate, and split with the platform.
// Per-row uniqueness on (creator, day, content_type, region) makes a
// re-run of yesterday's settlement a no-op.

// Eligibility thresholds. Any single one being met flips the creator to
// 'eligible'. Tuned for an early-stage launch — small but non-trivial.
// Tweak via env (CF_*) without recompile.
const (
	defaultEligibilityViewScore     = 1000.0           // 1k aggregated view-score points
	defaultEligibilityWatchTimeMs   = int64(36_000_000) // 10 hours of watch time
	defaultEligibilityContentCount  = 3                 // at least 3 earning videos
	defaultPlatformFeeBps           = int64(3000)       // 30% — creator keeps 70%
	defaultEligibilitySweepBatch    = 200
	defaultEligibilityStaleAfter    = 24 * time.Hour
	defaultEarningsCurrency         = "INR"
	defaultRegionCode               = "IN"
	creatorWalletAccountType        = "user_wallet"
	platformRevenueAccountType      = "platform_revenue"
	creatorFundReferenceType        = "creator_fund"
)

// platformOwnerID is the synthetic account owner the platform fee is
// credited to. Using a fixed UUID lets reconciliation always find the
// account; the ledger is the source of truth for revenue.
var platformOwnerID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// CreatorFundConfig is the runtime knob set the workers consult.
// Defaults match the constants above; main.go can override from env.
type CreatorFundConfig struct {
	EligibilityViewScore    float64
	EligibilityWatchTimeMs  int64
	EligibilityContentCount int
	PlatformFeeBps          int64
	SweepBatchSize          int
	EligibilityStaleAfter   time.Duration
}

// DefaultCreatorFundConfig returns the launch-baseline configuration.
func DefaultCreatorFundConfig() CreatorFundConfig {
	return CreatorFundConfig{
		EligibilityViewScore:    defaultEligibilityViewScore,
		EligibilityWatchTimeMs:  defaultEligibilityWatchTimeMs,
		EligibilityContentCount: defaultEligibilityContentCount,
		PlatformFeeBps:          defaultPlatformFeeBps,
		SweepBatchSize:          defaultEligibilitySweepBatch,
		EligibilityStaleAfter:   defaultEligibilityStaleAfter,
	}
}

// EligibilityDecision describes the outcome of an evaluator pass; useful
// for tests (so the formula stays asserted directly) and for the
// `creator-fund/status` endpoint to surface why a creator isn't yet in.
type EligibilityDecision struct {
	Status                  string  `json:"status"`
	ViewScore90D            float64 `json:"view_score_90d"`
	WatchTimeMs90D          int64   `json:"watch_time_ms_90d"`
	QualifyingContentCount  int     `json:"qualifying_content_count"`
	MetViewScoreThreshold   bool    `json:"met_view_score_threshold"`
	MetWatchTimeThreshold   bool    `json:"met_watch_time_threshold"`
	MetContentCountThreshold bool   `json:"met_content_count_threshold"`
	ConfigViewScore         float64 `json:"threshold_view_score"`
	ConfigWatchTimeMs       int64   `json:"threshold_watch_time_ms"`
	ConfigContentCount      int     `json:"threshold_content_count"`
}

// DecideEligibility is the pure-function decision: given a creator's
// 90-day stats and the active config, return the would-be status. Kept
// out of the DB layer so tests can drive it directly.
func DecideEligibility(viewScore float64, watchMs int64, contentCount int, cfg CreatorFundConfig) EligibilityDecision {
	d := EligibilityDecision{
		ViewScore90D:             viewScore,
		WatchTimeMs90D:           watchMs,
		QualifyingContentCount:   contentCount,
		MetViewScoreThreshold:    viewScore >= cfg.EligibilityViewScore,
		MetWatchTimeThreshold:    watchMs >= cfg.EligibilityWatchTimeMs,
		MetContentCountThreshold: contentCount >= cfg.EligibilityContentCount,
		ConfigViewScore:          cfg.EligibilityViewScore,
		ConfigWatchTimeMs:        cfg.EligibilityWatchTimeMs,
		ConfigContentCount:       cfg.EligibilityContentCount,
	}
	// Gate model: the content-count threshold is a floor (anti-spam, must
	// have at least N earning videos). Above that floor, *either* the
	// view-score *or* the watch-time threshold is enough.
	if d.MetContentCountThreshold && (d.MetViewScoreThreshold || d.MetWatchTimeThreshold) {
		d.Status = "eligible"
	} else {
		d.Status = "ineligible"
	}
	return d
}

// SplitEarnings is the pure 70/30 (or whatever feeBps says) split,
// rounded so net+fee==gross. Tests assert paise precision.
func SplitEarnings(grossPaise, platformFeeBps int64) (netPaise, platformFeePaise int64) {
	if grossPaise <= 0 {
		return 0, 0
	}
	platformFeePaise = grossPaise * platformFeeBps / 10_000
	netPaise = grossPaise - platformFeePaise
	return netPaise, platformFeePaise
}

// ComputeGrossPaise returns the gross earnings for `views` at `rpmPaise`
// per 1000 views. Integer arithmetic so we never round paise away by
// mistake.
func ComputeGrossPaise(views, rpmPaise int64) int64 {
	if views <= 0 || rpmPaise <= 0 {
		return 0
	}
	return views * rpmPaise / 1000
}

// ---------------------------------------------------------------------------
// Eligibility flow
// ---------------------------------------------------------------------------

// EvaluateEligibility runs one creator through the threshold gate using
// fresh analytics numbers. Sticky rules:
//   - 'suspended' rows stay suspended (only ClearSuspension lifts them).
//   - 'eligible_since' is preserved once set.
func (s *Service) EvaluateEligibility(ctx context.Context, creatorID uuid.UUID) (*postgres.CreatorFundEligibility, error) {
	cfg := s.creatorFundCfg

	existing, err := s.store.GetCreatorFundEligibility(ctx, creatorID)
	if err != nil {
		return nil, fmt.Errorf("get eligibility: %w", err)
	}
	if existing != nil && existing.Status == "suspended" {
		return existing, nil
	}

	now := time.Now()
	viewScore, watchMs, contentCount, err := s.store.QueryCreator90DayStats(ctx, creatorID, now)
	if err != nil {
		return nil, fmt.Errorf("query 90d stats: %w", err)
	}

	decision := DecideEligibility(viewScore, watchMs, contentCount, cfg)

	row := &postgres.CreatorFundEligibility{
		CreatorID:              creatorID,
		Status:                 decision.Status,
		ViewScore90D:           viewScore,
		WatchTimeMs90D:         watchMs,
		QualifyingContentCount: contentCount,
	}
	if existing != nil {
		row.EligibleSince = existing.EligibleSince
	}
	if decision.Status == "eligible" && row.EligibleSince == nil {
		t := now
		row.EligibleSince = &t
	}

	if err := s.store.UpsertCreatorFundEligibility(ctx, row); err != nil {
		return nil, fmt.Errorf("upsert eligibility: %w", err)
	}
	return s.store.GetCreatorFundEligibility(ctx, creatorID)
}

// GetCreatorFundStatus returns the current row plus a fresh decision
// envelope (without writing) so the dashboard can show "you need X more
// watch hours" even on creators who haven't been evaluated yet.
type CreatorFundStatus struct {
	Row      *postgres.CreatorFundEligibility `json:"row,omitempty"`
	Decision EligibilityDecision              `json:"decision"`
}

func (s *Service) GetCreatorFundStatus(ctx context.Context, creatorID uuid.UUID) (*CreatorFundStatus, error) {
	row, err := s.store.GetCreatorFundEligibility(ctx, creatorID)
	if err != nil {
		return nil, err
	}
	cfg := s.creatorFundCfg
	viewScore, watchMs, contentCount, err := s.store.QueryCreator90DayStats(ctx, creatorID, time.Now())
	if err != nil {
		return nil, err
	}
	d := DecideEligibility(viewScore, watchMs, contentCount, cfg)
	if row != nil && row.Status == "suspended" {
		d.Status = "suspended"
	}
	return &CreatorFundStatus{Row: row, Decision: d}, nil
}

// SuspendCreatorFund flips the row to suspended, blocking future fund
// earnings. Past settled earnings stay in the wallet — admin can reverse
// individually via the dispute pathway if needed.
func (s *Service) SuspendCreatorFund(ctx context.Context, creatorID uuid.UUID, reason string) error {
	if reason == "" {
		reason = "admin action"
	}
	return s.store.SetCreatorFundSuspension(ctx, creatorID, reason)
}

// ClearCreatorFundSuspension drops the suspended state back to pending
// so the next nightly evaluator can re-rate the creator.
func (s *Service) ClearCreatorFundSuspension(ctx context.Context, creatorID uuid.UUID) error {
	return s.store.ClearCreatorFundSuspension(ctx, creatorID)
}

// ---------------------------------------------------------------------------
// Earnings settlement
// ---------------------------------------------------------------------------

// SettleCreatorFundDay pays one creator for one UTC day across all the
// content types they uploaded views for. Each (content_type) row is
// idempotent on (creator, day, content_type, region); a partial-failure
// re-run will skip the rows already written.
//
// Returns the number of rows actually credited (may be 0 if the day was
// already settled, the creator has no qualifying views, or rates are
// missing).
func (s *Service) SettleCreatorFundDay(ctx context.Context, creatorID uuid.UUID, day time.Time) (int, error) {
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)

	row, err := s.store.GetCreatorFundEligibility(ctx, creatorID)
	if err != nil {
		return 0, err
	}
	if row == nil || row.Status != "eligible" {
		return 0, nil
	}

	metrics, err := s.store.QueryCreatorDailyMetrics(ctx, creatorID, day)
	if err != nil {
		return 0, fmt.Errorf("query daily metrics: %w", err)
	}
	if len(metrics) == 0 {
		return 0, nil
	}

	cfg := s.creatorFundCfg
	credited := 0
	for _, m := range metrics {
		if m.ViewCount <= 0 {
			continue
		}
		if m.ContentType != "long_video" && m.ContentType != "flick" {
			continue
		}
		exists, err := s.store.HasCreatorFundEarning(ctx, creatorID, day, m.ContentType, defaultRegionCode)
		if err != nil {
			return credited, fmt.Errorf("idempotency check: %w", err)
		}
		if exists {
			continue
		}
		rate, err := s.store.GetActiveRpmRate(ctx, m.ContentType, defaultRegionCode, day)
		if err != nil {
			return credited, fmt.Errorf("fetch rpm rate: %w", err)
		}
		if rate == nil || rate.RpmPaise <= 0 {
			// No rate configured for this content type — skip silently.
			// Admin can backfill once a rate is set.
			continue
		}
		gross := ComputeGrossPaise(m.ViewCount, rate.RpmPaise)
		if gross <= 0 {
			continue
		}
		net, fee := SplitEarnings(gross, cfg.PlatformFeeBps)

		earning := &postgres.CreatorFundEarning{
			CreatorID:        creatorID,
			DayBucket:        day,
			ContentType:      m.ContentType,
			RegionCode:       defaultRegionCode,
			ViewCount:        m.ViewCount,
			WatchTimeMs:      m.WatchTimeMs,
			RpmPaise:         rate.RpmPaise,
			GrossPaise:       gross,
			PlatformFeePaise: fee,
			NetPaise:         net,
			Status:           "settled",
			SettledAt:        time.Now(),
		}
		inserted, err := s.store.InsertCreatorFundEarning(ctx, earning)
		if err != nil {
			return credited, fmt.Errorf("insert earning: %w", err)
		}
		if !inserted {
			// Lost the race with a parallel run; row exists, our work
			// is already done.
			continue
		}

		if _, err := s.store.EnsureWallet(ctx, creatorID); err != nil {
			return credited, fmt.Errorf("ensure wallet: %w", err)
		}

		desc := fmt.Sprintf("Creator fund: %s, %d views @ %d paise/1000 (%s)",
			m.ContentType, m.ViewCount, rate.RpmPaise, day.Format("2006-01-02"))

		// Wallet credit + wallet-side transaction row.
		if err := s.store.CreditCreatorFundEarning(ctx, creatorID, net, defaultEarningsCurrency, earning.ID, desc); err != nil {
			return credited, fmt.Errorf("credit wallet: %w", err)
		}

		// Double-entry: platform_revenue debited, creator wallet credited
		// for the *gross*; the platform fee is captured separately as a
		// platform_revenue → platform_revenue self-entry would be a no-op,
		// so we book net-creator and fee-creator-to-platform as two
		// distinct entries that sum to gross.
		earningRef := earning.ID
		if err := s.CreateLedgerEntry(
			ctx,
			platformOwnerID, platformRevenueAccountType,
			creatorID, creatorWalletAccountType,
			net, defaultEarningsCurrency,
			creatorFundReferenceType, &earningRef,
			fmt.Sprintf("cf_net:%s:%s", earning.ID, m.ContentType),
			"creator_fund net credit",
		); err != nil {
			return credited, fmt.Errorf("ledger net credit: %w", err)
		}
		if fee > 0 {
			if err := s.CreateLedgerEntry(
				ctx,
				platformOwnerID, platformRevenueAccountType,
				platformOwnerID, platformRevenueAccountType+":fees",
				fee, defaultEarningsCurrency,
				creatorFundReferenceType, &earningRef,
				fmt.Sprintf("cf_fee:%s:%s", earning.ID, m.ContentType),
				"creator_fund platform fee",
			); err != nil {
				return credited, fmt.Errorf("ledger fee entry: %w", err)
			}
		}
		credited++
	}
	return credited, nil
}

// SettleCreatorFundDayForAllEligible drives the settlement worker. Walks
// every eligible creator and settles `day`. Errors on a single creator
// are logged and skipped so one bad row doesn't stall the batch.
func (s *Service) SettleCreatorFundDayForAllEligible(ctx context.Context, day time.Time, log func(creatorID uuid.UUID, credited int, err error)) (int, error) {
	creators, err := s.store.ListEligibleCreators(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range creators {
		credited, err := s.SettleCreatorFundDay(ctx, id, day)
		if log != nil {
			log(id, credited, err)
		}
		if err != nil {
			continue
		}
		total += credited
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// Earnings dashboard
// ---------------------------------------------------------------------------

// GetCreatorFundEarnings returns the creator's settled fund earnings
// over the last `days` days. days is clamped to [1, 365].
func (s *Service) GetCreatorFundEarnings(ctx context.Context, creatorID uuid.UUID, days int) (*postgres.EarningsSummary, error) {
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	now := time.Now().UTC()
	until := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	since := until.AddDate(0, 0, -days)
	return s.store.GetCreatorFundEarningsSummary(ctx, creatorID, since, until)
}

// ---------------------------------------------------------------------------
// Rates
// ---------------------------------------------------------------------------

// ListActiveRpmRates returns the current rate sheet.
func (s *Service) ListActiveRpmRates(ctx context.Context) ([]postgres.RpmRate, error) {
	return s.store.ListActiveRpmRates(ctx, time.Now())
}

// SetRpmRate configures a new active rate (closing the previous one).
// Caller is responsible for admin authorisation upstream.
func (s *Service) SetRpmRate(ctx context.Context, contentType, regionCode string, rpmPaise int64, notes string, adminID *uuid.UUID) (*postgres.RpmRate, error) {
	switch contentType {
	case "long_video", "flick":
	default:
		return nil, fmt.Errorf("INVALID_CONTENT_TYPE: %q (expected long_video|flick)", contentType)
	}
	if regionCode == "" {
		regionCode = defaultRegionCode
	}
	if rpmPaise < 0 {
		return nil, fmt.Errorf("INVALID_RATE: rpm_paise must be >= 0")
	}
	return s.store.SetRpmRate(ctx, contentType, regionCode, rpmPaise, notes, adminID)
}

// ---------------------------------------------------------------------------
// Worker drivers
// ---------------------------------------------------------------------------

// SweepEligibility re-evaluates a batch of creators whose last
// evaluation has gone stale. Used by the nightly worker.
func (s *Service) SweepEligibility(ctx context.Context, log func(creatorID uuid.UUID, status string, err error)) (int, error) {
	cfg := s.creatorFundCfg
	cutoff := time.Now().Add(-cfg.EligibilityStaleAfter)
	ids, err := s.store.ListCreatorsForEligibilitySweep(ctx, cutoff, cfg.SweepBatchSize)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		row, err := s.EvaluateEligibility(ctx, id)
		if log != nil {
			status := ""
			if row != nil {
				status = row.Status
			}
			log(id, status, err)
		}
	}
	return len(ids), nil
}
