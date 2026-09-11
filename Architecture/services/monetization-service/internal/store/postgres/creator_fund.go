package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreatorFundEligibility is the per-creator gate row that decides whether
// a creator's content earns through the daily fund settlement worker.
type CreatorFundEligibility struct {
	CreatorID                uuid.UUID  `json:"creator_id"`
	Status                   string     `json:"status"`
	ViewScore90D             float64    `json:"view_score_90d"`
	WatchTimeMs90D           int64      `json:"watch_time_ms_90d"`
	QualifyingContentCount   int        `json:"qualifying_content_count"`
	EligibleSince            *time.Time `json:"eligible_since,omitempty"`
	SuspendedAt              *time.Time `json:"suspended_at,omitempty"`
	SuspensionReason         string     `json:"suspension_reason,omitempty"`
	LastEvaluatedAt          time.Time  `json:"last_evaluated_at"`
}

// RpmRate is a single content-type rate row (paise per 1000 views) that
// applies between effective_from and effective_to. Active rate is the one
// with effective_to NULL or effective_to > now and the largest
// effective_from <= now.
type RpmRate struct {
	ID            uuid.UUID  `json:"id"`
	ContentType   string     `json:"content_type"`
	RegionCode    string     `json:"region_code"`
	RpmPaise      int64      `json:"rpm_paise"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	Notes         string     `json:"notes,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
}

// CreatorFundEarning is one settled day's earnings for a creator and
// content type. UNIQUE(creator, day, content_type, region) makes
// settlement re-runs idempotent.
type CreatorFundEarning struct {
	ID               uuid.UUID `json:"id"`
	CreatorID        uuid.UUID `json:"creator_id"`
	DayBucket        time.Time `json:"day_bucket"`
	ContentType      string    `json:"content_type"`
	RegionCode       string    `json:"region_code"`
	ViewCount        int64     `json:"view_count"`
	WatchTimeMs      int64     `json:"watch_time_ms"`
	RpmPaise         int64     `json:"rpm_paise"`
	GrossPaise       int64     `json:"gross_paise"`
	PlatformFeePaise int64     `json:"platform_fee_paise"`
	NetPaise         int64     `json:"net_paise"`
	Status           string    `json:"status"`
	SettledAt        time.Time `json:"settled_at"`

	// Quality audit trail. BaseGrossPaise is the historical
	// views x RPM / 1000 figure; GrossPaise above is what was actually
	// paid after the quality band was applied. When the band is disabled
	// the two are equal and QualityMultiplierBps is 10000 (1.0x), which
	// is the pre-quality behaviour, visibly recorded rather than implied.
	BaseGrossPaise       int64   `json:"base_gross_paise"`
	QualityCQS           float64 `json:"quality_cqs"`
	QualityEffectiveCQS  float64 `json:"quality_effective_cqs"`
	QualityImpressions   int64   `json:"quality_impressions"`
	QualityMultiplierBps int64   `json:"quality_multiplier_bps"`

	// Payment state (migration 017): flipped by the period settlement
	// inside the same transaction as the wallet credit.
	Credited     bool       `json:"credited"`
	CreditedAt   *time.Time `json:"credited_at,omitempty"`
	SettlementID *uuid.UUID `json:"settlement_id,omitempty"`

	// Reversal (migration 018). A reversed row keeps its UNIQUE slot and
	// its money columns; ReversalTransactionID names the adjustment that
	// gave the net back, nil when the row had never been credited.
	ReversedAt            *time.Time `json:"reversed_at,omitempty"`
	ReversalReason        string     `json:"reversal_reason,omitempty"`
	ReversalTransactionID *uuid.UUID `json:"reversal_transaction_id,omitempty"`

	// Versioning and rounding (migration 019). RateID and BandID name the
	// exact rows that priced the day; RuleVersion the formula;
	// InputRevision the sha256 of the analytics rows measured. Money is
	// computed in micro-paise and truncated once, at the carry boundary:
	// GrossMicroPaise + CarryIn = GrossPaise x 1e6 + CarryOut, except on a
	// budget-capped or exhausted day, where SkipReason says why not.
	RateID             *uuid.UUID `json:"rate_id,omitempty"`
	BandID             *uuid.UUID `json:"band_id,omitempty"`
	RuleVersion        string     `json:"rule_version"`
	InputRevision      string     `json:"input_revision,omitempty"`
	GrossMicroPaise    int64      `json:"gross_micro_paise"`
	CarryInMicroPaise  int64      `json:"carry_in_micro_paise"`
	CarryOutMicroPaise int64      `json:"carry_out_micro_paise"`
	SkipReason         string     `json:"skip_reason,omitempty"`
	// BuildSHA (migration 023) is the monetization-service build that wrote
	// the row, from internal/buildinfo. Empty on rows from before the column
	// existed; "unknown" on a binary built outside the release path.
	BuildSHA string `json:"build_sha,omitempty"`
}

// DailyInputRow is one analytics.v_creator_daily_metrics_v1 row as the accrual
// reads it: the measurement a day is priced from, with the row's
// updated_at so the input revision changes whenever the rollup rewrites
// the row.
type DailyInputRow struct {
	ContentID    uuid.UUID
	DayBucket    time.Time
	ContentType  string
	ViewsDisplay int64
	WatchTimeMs  int64
	Impressions  int64
	CQS          float64
	UpdatedAt    time.Time
}

// QualityBandRow is one versioned payout-multiplier band, the quality
// analogue of RpmRate. Same effective_from/effective_to versioning so a
// rate change never rewrites what an already-settled day was paid at.
type QualityBandRow struct {
	ID                    uuid.UUID  `json:"id"`
	ContentType           string     `json:"content_type"`
	RegionCode            string     `json:"region_code"`
	FloorBps              int64      `json:"floor_bps"`
	CeilingBps            int64      `json:"ceiling_bps"`
	PivotCQS              float64    `json:"pivot_cqs"`
	ConfidenceImpressions int64      `json:"confidence_impressions"`
	Enabled               bool       `json:"enabled"`
	EffectiveFrom         time.Time  `json:"effective_from"`
	EffectiveTo           *time.Time `json:"effective_to,omitempty"`
	Notes                 string     `json:"notes,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	CreatedBy             *uuid.UUID `json:"created_by,omitempty"`
}

// DailyContentMetric is one (content_type, view_count, watch_time)
// rollup row for a creator on a given day, sourced from analytics.
// Impressions and AvgCQS are the quality inputs the payout multiplier
// reads: the score itself, plus how much evidence stands behind it.
type DailyContentMetric struct {
	ContentType string
	ViewCount   int64
	WatchTimeMs int64
	Impressions int64
	AvgCQS      float64
}

// EarningsDailyBreakdown is one row for the dashboard view. The quality
// fields are additive: a creator must be able to see not just what they
// were paid but why, without a support ticket.
type EarningsDailyBreakdown struct {
	DayBucket   time.Time `json:"day_bucket"`
	ContentType string    `json:"content_type"`
	ViewCount   int64     `json:"view_count"`
	GrossPaise  int64     `json:"gross_paise"`
	NetPaise    int64     `json:"net_paise"`

	RpmPaise             int64   `json:"rpm_paise"`
	BaseGrossPaise       int64   `json:"base_gross_paise"`
	QualityCQS           float64 `json:"quality_cqs"`
	QualityEffectiveCQS  float64 `json:"quality_effective_cqs"`
	QualityImpressions   int64   `json:"quality_impressions"`
	QualityMultiplierBps int64   `json:"quality_multiplier_bps"`
	CarryInMicroPaise    int64   `json:"carry_in_micro_paise"`
	CarryOutMicroPaise   int64   `json:"carry_out_micro_paise"`
	SkipReason           string  `json:"skip_reason,omitempty"`
	// RuleVersion is the accrual formula that priced this day (migration
	// 019). Carried per row so a statement can see when its days were not
	// all priced the same way.
	RuleVersion          string  `json:"rule_version"`
	Explanation          string  `json:"explanation,omitempty"`
}

// EarningsSummary aggregates a creator's fund earnings over a time range.
type EarningsSummary struct {
	SinceDay        time.Time                `json:"since_day"`
	UntilDay        time.Time                `json:"until_day"`
	TotalGrossPaise int64                    `json:"total_gross_paise"`
	TotalNetPaise   int64                    `json:"total_net_paise"`
	TotalViews      int64                    `json:"total_views"`
	Breakdown       []EarningsDailyBreakdown `json:"breakdown"`

	// The beta label (plan Phase 3C). While payouts are off every figure
	// above is an ESTIMATE and none of it is withdrawable; the handler
	// stamps both so the label travels with the number.
	Estimate     bool `json:"estimate"`
	Withdrawable bool `json:"withdrawable"`
}

// ---------------------------------------------------------------------------
// Eligibility
// ---------------------------------------------------------------------------

// GetCreatorFundEligibility returns the row for a creator, or nil if no
// row exists yet (the creator has never been evaluated).
func (s *Store) GetCreatorFundEligibility(ctx context.Context, creatorID uuid.UUID) (*CreatorFundEligibility, error) {
	var e CreatorFundEligibility
	err := s.db.QueryRow(ctx, `
		SELECT creator_id, status, view_score_90d, watch_time_ms_90d,
		       qualifying_content_count, eligible_since, suspended_at,
		       COALESCE(suspension_reason, ''), last_evaluated_at
		FROM creator_fund_eligibility
		WHERE creator_id = $1
	`, creatorID).Scan(
		&e.CreatorID, &e.Status, &e.ViewScore90D, &e.WatchTimeMs90D,
		&e.QualifyingContentCount, &e.EligibleSince, &e.SuspendedAt,
		&e.SuspensionReason, &e.LastEvaluatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// UpsertCreatorFundEligibility writes the evaluation result. Suspension
// state is sticky — callers should pass the existing suspension fields
// through unless the admin is explicitly clearing them.
func (s *Store) UpsertCreatorFundEligibility(ctx context.Context, e *CreatorFundEligibility) error {
	now := time.Now()
	e.LastEvaluatedAt = now
	_, err := s.db.Exec(ctx, `
		INSERT INTO creator_fund_eligibility (
			creator_id, status, view_score_90d, watch_time_ms_90d,
			qualifying_content_count, eligible_since, suspended_at,
			suspension_reason, last_evaluated_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (creator_id) DO UPDATE SET
			status = EXCLUDED.status,
			view_score_90d = EXCLUDED.view_score_90d,
			watch_time_ms_90d = EXCLUDED.watch_time_ms_90d,
			qualifying_content_count = EXCLUDED.qualifying_content_count,
			eligible_since = EXCLUDED.eligible_since,
			suspended_at = EXCLUDED.suspended_at,
			suspension_reason = EXCLUDED.suspension_reason,
			last_evaluated_at = EXCLUDED.last_evaluated_at,
			updated_at = EXCLUDED.last_evaluated_at
	`,
		e.CreatorID, e.Status, e.ViewScore90D, e.WatchTimeMs90D,
		e.QualifyingContentCount, e.EligibleSince, e.SuspendedAt,
		nullableString(e.SuspensionReason), e.LastEvaluatedAt, now,
	)
	return err
}

// SetCreatorFundSuspension flips the row to suspended and stamps the
// admin's reason. Used by both the admin endpoint and any automated
// fraud rule that needs to take a creator out of the fund.
func (s *Store) SetCreatorFundSuspension(ctx context.Context, creatorID uuid.UUID, reason string) error {
	now := time.Now()
	tag, err := s.db.Exec(ctx, `
		UPDATE creator_fund_eligibility
		SET status = 'suspended',
		    suspended_at = $2,
		    suspension_reason = $3,
		    last_evaluated_at = $2,
		    updated_at = $2
		WHERE creator_id = $1
	`, creatorID, now, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// No row yet — create a suspended row so subsequent evaluations
		// see the sticky state.
		_, err := s.db.Exec(ctx, `
			INSERT INTO creator_fund_eligibility (
				creator_id, status, suspended_at, suspension_reason,
				last_evaluated_at, created_at, updated_at
			) VALUES ($1, 'suspended', $2, $3, $2, $2, $2)
			ON CONFLICT (creator_id) DO UPDATE SET
				status = 'suspended',
				suspended_at = EXCLUDED.suspended_at,
				suspension_reason = EXCLUDED.suspension_reason,
				last_evaluated_at = EXCLUDED.last_evaluated_at,
				updated_at = EXCLUDED.updated_at
		`, creatorID, now, reason)
		return err
	}
	return nil
}

// ClearCreatorFundSuspension drops the suspended state back to pending,
// so the next eligibility evaluation can re-rate the creator. Admin-only.
func (s *Store) ClearCreatorFundSuspension(ctx context.Context, creatorID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE creator_fund_eligibility
		SET status = 'pending',
		    suspended_at = NULL,
		    suspension_reason = NULL,
		    updated_at = NOW()
		WHERE creator_id = $1
	`, creatorID)
	return err
}

// ListEligibleCreators returns every creator currently in 'eligible'
// status — these are the rows the daily settlement worker iterates over.
// Ordered by creator_id so that, under a budget cap, which creator's day
// reaches the cap is the same on every run.
func (s *Store) ListEligibleCreators(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT creator_id FROM creator_fund_eligibility WHERE status = 'eligible'
		ORDER BY creator_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListCreatorsForEligibilitySweep returns rows whose last_evaluated_at is
// older than the cutoff, plus any creator who has analytics rows but no
// eligibility row yet. Used by the nightly evaluator to keep status
// fresh without thundering through every account every night.
func (s *Store) ListCreatorsForEligibilitySweep(ctx context.Context, olderThan time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		(
			SELECT creator_id FROM creator_fund_eligibility
			WHERE last_evaluated_at < $1
			ORDER BY last_evaluated_at ASC
			LIMIT $2
		)
		UNION
		(
			SELECT DISTINCT creator_id FROM analytics.v_creator_daily_metrics_v1
			WHERE day_bucket >= (CURRENT_DATE - INTERVAL '90 days')
			  AND creator_id NOT IN (SELECT creator_id FROM creator_fund_eligibility)
			LIMIT $2
		)
	`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// RPM rates
// ---------------------------------------------------------------------------

// GetActiveRpmRate returns the rate effective at the given moment for a
// (content_type, region) pair. Latest effective_from wins among rows whose
// window contains asOf. Returns nil if no rate is configured.
func (s *Store) GetActiveRpmRate(ctx context.Context, contentType, regionCode string, asOf time.Time) (*RpmRate, error) {
	var r RpmRate
	err := s.db.QueryRow(ctx, `
		SELECT id, content_type, region_code, rpm_paise, effective_from,
		       effective_to, COALESCE(notes, ''), created_at, created_by
		FROM monetization_rpm_rates
		WHERE content_type = $1 AND region_code = $2
		  AND effective_from <= $3
		  AND (effective_to IS NULL OR effective_to > $3)
		-- created_at, then id, break a tie on effective_from. Without them
		-- Postgres may return either row, and a re-settlement of the same
		-- period can price it differently. See GetActiveQualityBand below
		-- for the full reasoning; the exposure is identical here.
		ORDER BY effective_from DESC, created_at DESC, id DESC
		LIMIT 1
	`, contentType, regionCode, asOf).Scan(
		&r.ID, &r.ContentType, &r.RegionCode, &r.RpmPaise, &r.EffectiveFrom,
		&r.EffectiveTo, &r.Notes, &r.CreatedAt, &r.CreatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// ListActiveRpmRates returns the active rate per (content_type, region)
// at the given moment. Used by the admin UI and the dashboard rate sheet.
func (s *Store) ListActiveRpmRates(ctx context.Context, asOf time.Time) ([]RpmRate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (content_type, region_code)
		       id, content_type, region_code, rpm_paise, effective_from,
		       effective_to, COALESCE(notes, ''), created_at, created_by
		FROM monetization_rpm_rates
		WHERE effective_from <= $1
		  AND (effective_to IS NULL OR effective_to > $1)
		ORDER BY content_type, region_code, effective_from DESC, created_at DESC, id DESC
	`, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RpmRate
	for rows.Next() {
		var r RpmRate
		if err := rows.Scan(
			&r.ID, &r.ContentType, &r.RegionCode, &r.RpmPaise, &r.EffectiveFrom,
			&r.EffectiveTo, &r.Notes, &r.CreatedAt, &r.CreatedBy,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRpmRate inserts a new rate row and closes off (effective_to) any
// previously-active row for the same (content_type, region). All in one
// transaction so the active-rate query never sees an overlap.
func (s *Store) SetRpmRate(ctx context.Context, contentType, regionCode string, rpmPaise int64, notes string, createdBy *uuid.UUID) (*RpmRate, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now()
	if _, err := tx.Exec(ctx, `
		UPDATE monetization_rpm_rates
		SET effective_to = $3
		WHERE content_type = $1 AND region_code = $2 AND effective_to IS NULL
	`, contentType, regionCode, now); err != nil {
		return nil, err
	}
	r := &RpmRate{
		ID:            uuid.New(),
		ContentType:   contentType,
		RegionCode:    regionCode,
		RpmPaise:      rpmPaise,
		EffectiveFrom: now,
		Notes:         notes,
		CreatedAt:     now,
		CreatedBy:     createdBy,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO monetization_rpm_rates (
			id, content_type, region_code, rpm_paise,
			effective_from, notes, created_at, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, r.ID, r.ContentType, r.RegionCode, r.RpmPaise,
		r.EffectiveFrom, nullableString(r.Notes), r.CreatedAt, r.CreatedBy); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Daily metrics + earnings
// ---------------------------------------------------------------------------

// QueryCreatorDailyInputs returns the analytics.v_creator_daily_metrics_v1
// rows for one creator on one day, in a fixed order, exactly as the
// accrual prices and hashes them. Aggregation to content type happens in
// the service (AggregateDailyInputs) from the same rows the revision is
// computed over, so the number and its fingerprint cannot drift apart.
func (s *Store) QueryCreatorDailyInputs(ctx context.Context, creatorID uuid.UUID, day time.Time) ([]DailyInputRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT content_id, day_bucket, content_type,
		       COALESCE(views_display, 0)::BIGINT,
		       COALESCE(watch_time_total_ms, 0)::BIGINT,
		       COALESCE(impressions, 0)::BIGINT,
		       COALESCE(content_quality_score, 0)::DOUBLE PRECISION,
		       updated_at
		FROM analytics.v_creator_daily_metrics_v1
		WHERE creator_id = $1 AND day_bucket = $2
		ORDER BY content_type, content_id
	`, creatorID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyInputRow
	for rows.Next() {
		var r DailyInputRow
		if err := rows.Scan(&r.ContentID, &r.DayBucket, &r.ContentType, &r.ViewsDisplay, &r.WatchTimeMs,
			&r.Impressions, &r.CQS, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryCreator90DayStats returns the inputs the eligibility evaluator
// needs: total view score, total watch time, count of distinct contents
// with non-zero views over the last 90 days. Single SQL pass.
func (s *Store) QueryCreator90DayStats(ctx context.Context, creatorID uuid.UUID, now time.Time) (viewScore float64, watchMs int64, contentCount int, err error) {
	cutoff := now.AddDate(0, 0, -90)
	err = s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(view_score_total), 0)::DOUBLE PRECISION,
			COALESCE(SUM(watch_time_total_ms), 0)::BIGINT,
			COUNT(DISTINCT content_id)::INTEGER
		FROM analytics.v_creator_daily_metrics_v1
		WHERE creator_id = $1
		  AND day_bucket >= $2
		  AND views_display > 0
	`, creatorID, cutoff).Scan(&viewScore, &watchMs, &contentCount)
	return
}

// HasCreatorFundEarning reports whether a settlement row already exists
// for (creator, day, content_type, region). The settlement worker uses
// this to short-circuit before re-doing the wallet/ledger writes that a
// failed mid-flight settlement may have already done.
func (s *Store) HasCreatorFundEarning(ctx context.Context, creatorID uuid.UUID, day time.Time, contentType, regionCode string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM creator_fund_earnings
			WHERE creator_id = $1 AND day_bucket = $2
			  AND content_type = $3 AND region_code = $4
		)
	`, creatorID, day, contentType, regionCode).Scan(&exists)
	return exists, err
}

// InsertCreatorFundEarning records the accrual row. ON CONFLICT DO
// NOTHING enforces (creator, day, content_type, region) uniqueness so a
// re-run after a partial failure does not double-credit. Production
// accruals go through AccrueDayTx, which calls the same insert inside
// its transaction; this autocommit form remains for fixtures and tools.
func (s *Store) InsertCreatorFundEarning(ctx context.Context, e *CreatorFundEarning) (bool, error) {
	return insertCreatorFundEarning(ctx, s.db, e)
}

func insertCreatorFundEarning(ctx context.Context, q DBTX, e *CreatorFundEarning) (bool, error) {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.SettledAt.IsZero() {
		e.SettledAt = time.Now()
	}
	if e.RuleVersion == "" {
		e.RuleVersion = "cf-1"
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO creator_fund_earnings (
			id, creator_id, day_bucket, content_type, region_code,
			view_count, watch_time_ms, rpm_paise, gross_paise,
			platform_fee_paise, net_paise, status, settled_at,
			base_gross_paise, quality_cqs, quality_effective_cqs,
			quality_impressions, quality_multiplier_bps,
			rate_id, band_id, rule_version, input_revision,
			gross_micro_paise, carry_in_micro_paise, carry_out_micro_paise, skip_reason, build_sha
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
		          $14, $15, $16, $17, $18,
		          $19, $20, $21, $22, $23, $24, $25, $26, $27)
		ON CONFLICT (creator_id, day_bucket, content_type, region_code) DO NOTHING
	`,
		e.ID, e.CreatorID, e.DayBucket, e.ContentType, e.RegionCode,
		e.ViewCount, e.WatchTimeMs, e.RpmPaise, e.GrossPaise,
		e.PlatformFeePaise, e.NetPaise, e.Status, e.SettledAt,
		e.BaseGrossPaise, e.QualityCQS, e.QualityEffectiveCQS,
		e.QualityImpressions, e.QualityMultiplierBps,
		e.RateID, e.BandID, e.RuleVersion, nullableString(e.InputRevision),
		e.GrossMicroPaise, e.CarryInMicroPaise, e.CarryOutMicroPaise, nullableString(e.SkipReason), nullableString(e.BuildSHA),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const creatorFundEarningSelect = `
	SELECT id, creator_id, day_bucket, content_type, region_code,
	       view_count, watch_time_ms, rpm_paise, gross_paise,
	       platform_fee_paise, net_paise, status, settled_at,
	       base_gross_paise, quality_cqs, quality_effective_cqs,
	       quality_impressions, quality_multiplier_bps,
	       credited, credited_at, settlement_id,
	       reversed_at, COALESCE(reversal_reason, ''), reversal_transaction_id,
	       rate_id, band_id, rule_version, COALESCE(input_revision, ''),
	       gross_micro_paise, carry_in_micro_paise, carry_out_micro_paise, COALESCE(skip_reason, ''),
	       COALESCE(build_sha, '')
	FROM creator_fund_earnings`

func scanCreatorFundEarning(r rowScanner) (*CreatorFundEarning, error) {
	var e CreatorFundEarning
	if err := r.Scan(
		&e.ID, &e.CreatorID, &e.DayBucket, &e.ContentType, &e.RegionCode,
		&e.ViewCount, &e.WatchTimeMs, &e.RpmPaise, &e.GrossPaise,
		&e.PlatformFeePaise, &e.NetPaise, &e.Status, &e.SettledAt,
		&e.BaseGrossPaise, &e.QualityCQS, &e.QualityEffectiveCQS,
		&e.QualityImpressions, &e.QualityMultiplierBps,
		&e.Credited, &e.CreditedAt, &e.SettlementID,
		&e.ReversedAt, &e.ReversalReason, &e.ReversalTransactionID,
		&e.RateID, &e.BandID, &e.RuleVersion, &e.InputRevision,
		&e.GrossMicroPaise, &e.CarryInMicroPaise, &e.CarryOutMicroPaise, &e.SkipReason, &e.BuildSHA,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// findCreatorFundEarningSlot returns whatever row occupies the
// (creator, day, content_type, region) slot — settled or reversed — or nil.
func findCreatorFundEarningSlot(ctx context.Context, q DBTX, creatorID uuid.UUID, day time.Time, contentType, regionCode string) (*CreatorFundEarning, error) {
	row := q.QueryRow(ctx, creatorFundEarningSelect+`
		WHERE creator_id = $1 AND day_bucket = $2 AND content_type = $3 AND region_code = $4
	`, creatorID, day, contentType, regionCode)
	e, err := scanCreatorFundEarning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// FindCreatorFundEarningSlot is the autocommit form of the slot check,
// used by the accrual as a cheap pre-check before it takes any lock.
func (s *Store) FindCreatorFundEarningSlot(ctx context.Context, creatorID uuid.UUID, day time.Time, contentType, regionCode string) (*CreatorFundEarning, error) {
	return findCreatorFundEarningSlot(ctx, s.db, creatorID, day, contentType, regionCode)
}

// GetCreatorFundEarning returns one accrual row by id, or nil.
func (s *Store) GetCreatorFundEarning(ctx context.Context, id uuid.UUID) (*CreatorFundEarning, error) {
	row := s.db.QueryRow(ctx, creatorFundEarningSelect+` WHERE id = $1`, id)
	e, err := scanCreatorFundEarning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// GetCreatorFundEarningForUpdateTx locks one accrual row for the rest of
// the transaction, or returns nil if it does not exist.
func (s *Store) GetCreatorFundEarningForUpdateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*CreatorFundEarning, error) {
	row := tx.QueryRow(ctx, creatorFundEarningSelect+` WHERE id = $1 FOR UPDATE`, id)
	e, err := scanCreatorFundEarning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// MarkCreatorFundEarningReversedTx flips a settled row to reversed and
// records when, why and which adjustment (if any) gave the money back.
// The money columns are untouched: a reversed row still says what it was
// settled at. Zero rows affected means the row was not settled.
func (s *Store) MarkCreatorFundEarningReversedTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string, reversalTransactionID *uuid.UUID, at time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE creator_fund_earnings
		SET status = 'reversed',
		    reversed_at = $3,
		    reversal_reason = $4,
		    reversal_transaction_id = $5
		WHERE id = $1 AND status = $2
	`, id, "settled", at, reason, reversalTransactionID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CreditCreatorFundEarning credits the creator wallet inside the same
// transaction that records the wallet-side `creator_fund_earning`
// transaction row. The double-entry ledger entry is written separately
// by the service layer through CreateLedgerEntry.
func (s *Store) CreditCreatorFundEarning(ctx context.Context, creatorID uuid.UUID, netPaise int64, currency string, earningID uuid.UUID, description string) error {
	if netPaise <= 0 {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE creator_ledger
		SET balance = balance + $2,
		    lifetime_earnings = lifetime_earnings + $2,
		    updated_at = NOW()
		WHERE user_id = $1
	`, creatorID, netPaise); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO transactions (
			id, wallet_id, type, amount, currency, status,
			reference_type, reference_id, description, created_at
		) VALUES ($1, $2, 'creator_fund_earning', $3, $4, 'completed',
		          'creator_fund_earning', $5, $6, NOW())
	`, uuid.New(), creatorID, netPaise, currency, earningID.String(), description); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetCreatorFundEarningsSummary returns the totals + per-day breakdown
// for the creator's dashboard view. Range is half-open [since, until).
func (s *Store) GetCreatorFundEarningsSummary(ctx context.Context, creatorID uuid.UUID, since, until time.Time) (*EarningsSummary, error) {
	var summary EarningsSummary
	summary.SinceDay = since
	summary.UntilDay = until

	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(gross_paise), 0)::BIGINT,
		       COALESCE(SUM(net_paise), 0)::BIGINT,
		       COALESCE(SUM(view_count), 0)::BIGINT
		FROM creator_fund_earnings
		WHERE creator_id = $1
		  AND day_bucket >= $2
		  AND day_bucket < $3
		  AND status = 'settled'
	`, creatorID, since, until).Scan(
		&summary.TotalGrossPaise, &summary.TotalNetPaise, &summary.TotalViews,
	)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT day_bucket, content_type, view_count, gross_paise, net_paise,
		       rpm_paise, base_gross_paise, quality_cqs, quality_effective_cqs,
		       quality_impressions, quality_multiplier_bps,
		       carry_in_micro_paise, carry_out_micro_paise, COALESCE(skip_reason, ''), rule_version
		FROM creator_fund_earnings
		WHERE creator_id = $1
		  AND day_bucket >= $2
		  AND day_bucket < $3
		  AND status = 'settled'
		ORDER BY day_bucket DESC, content_type
	`, creatorID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b EarningsDailyBreakdown
		if err := rows.Scan(&b.DayBucket, &b.ContentType, &b.ViewCount,
			&b.GrossPaise, &b.NetPaise, &b.RpmPaise, &b.BaseGrossPaise,
			&b.QualityCQS, &b.QualityEffectiveCQS, &b.QualityImpressions,
			&b.QualityMultiplierBps, &b.CarryInMicroPaise, &b.CarryOutMicroPaise,
			&b.SkipReason, &b.RuleVersion); err != nil {
			return nil, err
		}
		summary.Breakdown = append(summary.Breakdown, b)
	}
	return &summary, rows.Err()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------------------
// Quality multiplier bands
// ---------------------------------------------------------------------------

// GetActiveQualityBand returns the payout-multiplier band effective at
// asOf for a (content_type, region). Returns nil when none is
// configured, which the service reads as "use the launch default".
func (s *Store) GetActiveQualityBand(ctx context.Context, contentType, regionCode string, asOf time.Time) (*QualityBandRow, error) {
	var b QualityBandRow
	err := s.db.QueryRow(ctx, `
		SELECT id, content_type, region_code, floor_bps, ceiling_bps,
		       pivot_cqs, confidence_impressions, enabled, effective_from,
		       effective_to, COALESCE(notes, ''), created_at, created_by
		FROM monetization_quality_bands
		WHERE content_type = $1 AND region_code = $2
		  AND effective_from <= $3
		  AND (effective_to IS NULL OR effective_to > $3)
		-- THE TIEBREAKER IS LOAD-BEARING.
		--
		-- Two rows can legitimately share an effective_from: SetQualityBand
		-- stamps time.Now() and closes the previous row in the same
		-- transaction, so two writes in one instant collide, as does any
		-- row inserted outside that path — this dev database has 26
		-- long_video rows, four of them tying on 2025-03-05, left by
		-- integration fixtures.
		--
		-- With ORDER BY effective_from alone, Postgres may return either.
		-- Settlement runs on a lag and periods are re-settled, so an
		-- arbitrary winner means the same period can be priced two ways by
		-- two runs, or by two pods, with nothing in the data to say which
		-- was right. created_at then id makes the most recently written
		-- row win, and win every time.
		ORDER BY effective_from DESC, created_at DESC, id DESC
		LIMIT 1
	`, contentType, regionCode, asOf).Scan(
		&b.ID, &b.ContentType, &b.RegionCode, &b.FloorBps, &b.CeilingBps,
		&b.PivotCQS, &b.ConfidenceImpressions, &b.Enabled, &b.EffectiveFrom,
		&b.EffectiveTo, &b.Notes, &b.CreatedAt, &b.CreatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// ListActiveQualityBands returns the current band per (content_type,
// region). Creator-visible: a creator is entitled to know the curve
// their pay is scaled by before they are paid by it.
func (s *Store) ListActiveQualityBands(ctx context.Context, asOf time.Time) ([]QualityBandRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (content_type, region_code)
		       id, content_type, region_code, floor_bps, ceiling_bps,
		       pivot_cqs, confidence_impressions, enabled, effective_from,
		       effective_to, COALESCE(notes, ''), created_at, created_by
		FROM monetization_quality_bands
		WHERE effective_from <= $1
		  AND (effective_to IS NULL OR effective_to > $1)
		ORDER BY content_type, region_code, effective_from DESC, created_at DESC, id DESC
	`, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QualityBandRow
	for rows.Next() {
		var b QualityBandRow
		if err := rows.Scan(
			&b.ID, &b.ContentType, &b.RegionCode, &b.FloorBps, &b.CeilingBps,
			&b.PivotCQS, &b.ConfidenceImpressions, &b.Enabled, &b.EffectiveFrom,
			&b.EffectiveTo, &b.Notes, &b.CreatedAt, &b.CreatedBy,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetQualityBand inserts a new band and closes off the previously-active
// one for the same (content_type, region), in one transaction. Mirrors
// SetRpmRate exactly, so the multiplier is configurable through the same
// admin flow and the same audit trail as the rate it multiplies.
func (s *Store) SetQualityBand(ctx context.Context, b *QualityBandRow) (*QualityBandRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now()
	if _, err := tx.Exec(ctx, `
		UPDATE monetization_quality_bands
		SET effective_to = $3
		WHERE content_type = $1 AND region_code = $2 AND effective_to IS NULL
	`, b.ContentType, b.RegionCode, now); err != nil {
		return nil, err
	}
	b.ID = uuid.New()
	b.EffectiveFrom = now
	b.CreatedAt = now
	if _, err := tx.Exec(ctx, `
		INSERT INTO monetization_quality_bands (
			id, content_type, region_code, floor_bps, ceiling_bps,
			pivot_cqs, confidence_impressions, enabled, effective_from,
			notes, created_at, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, b.ID, b.ContentType, b.RegionCode, b.FloorBps, b.CeilingBps,
		b.PivotCQS, b.ConfidenceImpressions, b.Enabled, b.EffectiveFrom,
		nullableString(b.Notes), b.CreatedAt, b.CreatedBy); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return b, nil
}
