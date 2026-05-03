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
}

// DailyContentMetric is one (content_type, view_count, watch_time)
// rollup row for a creator on a given day, sourced from analytics.
type DailyContentMetric struct {
	ContentType string
	ViewCount   int64
	WatchTimeMs int64
}

// EarningsDailyBreakdown is one row for the dashboard view.
type EarningsDailyBreakdown struct {
	DayBucket   time.Time `json:"day_bucket"`
	ContentType string    `json:"content_type"`
	ViewCount   int64     `json:"view_count"`
	GrossPaise  int64     `json:"gross_paise"`
	NetPaise    int64     `json:"net_paise"`
}

// EarningsSummary aggregates a creator's fund earnings over a time range.
type EarningsSummary struct {
	SinceDay        time.Time                `json:"since_day"`
	UntilDay        time.Time                `json:"until_day"`
	TotalGrossPaise int64                    `json:"total_gross_paise"`
	TotalNetPaise   int64                    `json:"total_net_paise"`
	TotalViews      int64                    `json:"total_views"`
	Breakdown       []EarningsDailyBreakdown `json:"breakdown"`
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
func (s *Store) ListEligibleCreators(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT creator_id FROM creator_fund_eligibility WHERE status = 'eligible'
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
			SELECT DISTINCT creator_id FROM analytics.content_daily_summary
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
		ORDER BY effective_from DESC
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
		ORDER BY content_type, region_code, effective_from DESC
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

// QueryCreatorDailyMetrics rolls analytics.content_daily_summary up to
// (content_type) for one creator on one day. Returns one row per content
// type the creator published views for that day.
func (s *Store) QueryCreatorDailyMetrics(ctx context.Context, creatorID uuid.UUID, day time.Time) ([]DailyContentMetric, error) {
	rows, err := s.db.Query(ctx, `
		SELECT content_type,
		       COALESCE(SUM(views_display), 0)::BIGINT,
		       COALESCE(SUM(watch_time_total_ms), 0)::BIGINT
		FROM analytics.content_daily_summary
		WHERE creator_id = $1 AND day_bucket = $2
		GROUP BY content_type
	`, creatorID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyContentMetric
	for rows.Next() {
		var m DailyContentMetric
		if err := rows.Scan(&m.ContentType, &m.ViewCount, &m.WatchTimeMs); err != nil {
			return nil, err
		}
		out = append(out, m)
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
		FROM analytics.content_daily_summary
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

// InsertCreatorFundEarning records the settlement row. ON CONFLICT DO
// NOTHING enforces (creator, day, content_type, region) uniqueness so a
// re-run after a partial failure does not double-credit.
func (s *Store) InsertCreatorFundEarning(ctx context.Context, e *CreatorFundEarning) (bool, error) {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.SettledAt.IsZero() {
		e.SettledAt = time.Now()
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO creator_fund_earnings (
			id, creator_id, day_bucket, content_type, region_code,
			view_count, watch_time_ms, rpm_paise, gross_paise,
			platform_fee_paise, net_paise, status, settled_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (creator_id, day_bucket, content_type, region_code) DO NOTHING
	`,
		e.ID, e.CreatorID, e.DayBucket, e.ContentType, e.RegionCode,
		e.ViewCount, e.WatchTimeMs, e.RpmPaise, e.GrossPaise,
		e.PlatformFeePaise, e.NetPaise, e.Status, e.SettledAt,
	)
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
		SELECT day_bucket, content_type, view_count, gross_paise, net_paise
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
		if err := rows.Scan(&b.DayBucket, &b.ContentType, &b.ViewCount, &b.GrossPaise, &b.NetPaise); err != nil {
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
