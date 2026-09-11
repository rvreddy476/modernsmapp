package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PeriodSettlement is one creator's statement-and-payment for one
// settlement period. UNIQUE (creator_id, period_key, region_code) is the
// idempotency key that replaced the old per-day one: a period can only be
// settled once.
type PeriodSettlement struct {
	ID          uuid.UUID `json:"id"`
	CreatorID   uuid.UUID `json:"creator_id"`
	PeriodKey   string    `json:"period_key"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	RegionCode  string    `json:"region_code"`
	Currency    string    `json:"currency"`

	FundRows             int64 `json:"fund_rows"`
	FundViews            int64 `json:"fund_views"`
	FundWatchTimeMs      int64 `json:"fund_watch_time_ms"`
	FundGrossPaise       int64 `json:"fund_gross_paise"`
	FundPlatformFeePaise int64 `json:"fund_platform_fee_paise"`
	FundNetPaise         int64 `json:"fund_net_paise"`
	FundPlatformFeeBps   int64 `json:"fund_platform_fee_bps"`

	TipsCount            int64 `json:"tips_count"`
	TipsGrossPaise       int64 `json:"tips_gross_paise"`
	TipsPlatformFeePaise int64 `json:"tips_platform_fee_paise"`
	TipsNetPaise         int64 `json:"tips_net_paise"`
	TipsPlatformFeeBps   int64 `json:"tips_platform_fee_bps"`

	SubsCount            int64 `json:"subs_count"`
	SubsGrossPaise       int64 `json:"subs_gross_paise"`
	SubsPlatformFeePaise int64 `json:"subs_platform_fee_paise"`
	SubsNetPaise         int64 `json:"subs_net_paise"`
	SubsPlatformFeeBps   int64 `json:"subs_platform_fee_bps"`

	GrossPaise           int64 `json:"gross_paise"`
	PlatformFeePaise     int64 `json:"platform_fee_paise"`
	NetPaise             int64 `json:"net_paise"`
	CreditedPaise        int64 `json:"credited_paise"`
	AlreadyCreditedPaise int64 `json:"already_credited_paise"`
	PendingPaise         int64 `json:"pending_paise"`

	// Memo lines (migration 018). ReversedPaise is the net of fund rows in
	// this period that were later reversed; they are off the fund line, so
	// the totals above already exclude them. AdjustmentsPaise is the signed
	// sum of adjustments that LANDED in this period.
	ReversedPaise    int64 `json:"reversed_paise"`
	FundReversedRows int64 `json:"fund_reversed_rows"`
	AdjustmentsPaise int64 `json:"adjustments_paise"`
	AdjustmentsCount int64 `json:"adjustments_count"`

	// Budget (migration 019). Nil cap means the period was uncapped.
	BudgetCapPaise       *int64     `json:"budget_cap_paise,omitempty"`
	BudgetExhaustedOnDay *time.Time `json:"budget_exhausted_on_day,omitempty"`
	FundRowsSkipped      int64      `json:"fund_rows_skipped"`

	Status    string    `json:"status"`
	SettledAt time.Time `json:"settled_at"`
}

// FundAccrualTotals is the fund stream for a period: the sum of the
// per-day, per-content-type accrual rows. Deliberately read back from
// creator_fund_earnings rather than recomputed from analytics, so the
// statement can never disagree with what was accrued.
type FundAccrualTotals struct {
	Rows             int64
	Views            int64
	WatchTimeMs      int64
	GrossPaise       int64
	PlatformFeePaise int64
	NetPaise         int64
}

// PeriodClaim is the input to ClaimAndCreditPeriodAccruals. The three
// account IDs are resolved by the caller (Service.EnsureAccount) so the
// claim transaction can write the double-entry legs itself — the ledger
// must commit or roll back with the wallet credit, not after it.
type PeriodClaim struct {
	SettlementID uuid.UUID
	CreatorID    uuid.UUID
	PeriodStart  time.Time
	PeriodEnd    time.Time
	RegionCode   string
	Currency     string
	Description  string

	PlatformRevenueAccountID uuid.UUID
	CreatorWalletAccountID   uuid.UUID
	PlatformFeeAccountID     uuid.UUID
	ReferenceType            string
}

// ClaimedAccruals is what one settlement run actually took ownership of.
type ClaimedAccruals struct {
	Rows             int64
	GrossPaise       int64
	PlatformFeePaise int64
	NetPaise         int64
}

// SumCreatorFundAccruals totals the settled fund accrual rows in
// [start, end) for one creator and region.
func (s *Store) SumCreatorFundAccruals(ctx context.Context, creatorID uuid.UUID, start, end time.Time, regionCode string) (FundAccrualTotals, error) {
	var t FundAccrualTotals
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)::BIGINT,
		       COALESCE(SUM(view_count), 0)::BIGINT,
		       COALESCE(SUM(watch_time_ms), 0)::BIGINT,
		       COALESCE(SUM(gross_paise), 0)::BIGINT,
		       COALESCE(SUM(platform_fee_paise), 0)::BIGINT,
		       COALESCE(SUM(net_paise), 0)::BIGINT
		FROM creator_fund_earnings
		WHERE creator_id = $1
		  AND day_bucket >= $2 AND day_bucket < $3
		  AND region_code = $4
		  AND status = 'settled'
	`, creatorID, start, end, regionCode).Scan(
		&t.Rows, &t.Views, &t.WatchTimeMs, &t.GrossPaise, &t.PlatformFeePaise, &t.NetPaise,
	)
	return t, err
}

// SumReversedFundAccruals totals the REVERSED fund rows in [start, end)
// for one creator and region: the memo line under the fund total. These
// rows are excluded from SumCreatorFundAccruals, so this is additive
// information, not a term in the statement's arithmetic.
func (s *Store) SumReversedFundAccruals(ctx context.Context, creatorID uuid.UUID, start, end time.Time, regionCode string) (rows int64, netPaise int64, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*)::BIGINT, COALESCE(SUM(net_paise), 0)::BIGINT
		FROM creator_fund_earnings
		WHERE creator_id = $1
		  AND day_bucket >= $2 AND day_bucket < $3
		  AND region_code = $4
		  AND status = 'reversed'
	`, creatorID, start, end, regionCode).Scan(&rows, &netPaise)
	return
}

// CountSkippedFundAccruals counts the zero rows the budget cap produced
// in [start, end): days that were measured and paid nothing because the
// period's fund was already spent.
func (s *Store) CountSkippedFundAccruals(ctx context.Context, creatorID uuid.UUID, start, end time.Time, regionCode string) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)::BIGINT
		FROM creator_fund_earnings
		WHERE creator_id = $1
		  AND day_bucket >= $2 AND day_bucket < $3
		  AND region_code = $4
		  AND status = 'settled'
		  AND skip_reason = 'budget_exhausted'
	`, creatorID, start, end, regionCode).Scan(&n)
	return n, err
}

// FundCreditSplit says where a period's fund money currently stands
// relative to ONE settlement row: what that settlement paid, what some
// other settlement already paid (an overlapping period, or the pre-017
// per-day path, whose rows carry credited = TRUE and no settlement id),
// and what is still owed.
//
// It exists because "credited by this run + already in the wallet = net"
// has to hold even when the days in this window were paid by a different
// period. Settling 2026-09-H1 after 2026-09 has already paid the same
// days is not an error and must not look like missing money; it is a
// statement whose fund line was paid elsewhere.
type FundCreditSplit struct {
	CreditedByThisSettlement int64
	CreditedElsewhere        int64
	Uncredited               int64
}

// SplitCreatorFundAccrualCredit computes that three-way split.
func (s *Store) SplitCreatorFundAccrualCredit(ctx context.Context, creatorID uuid.UUID, start, end time.Time, regionCode string, settlementID uuid.UUID) (FundCreditSplit, error) {
	var out FundCreditSplit
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(net_paise) FILTER (WHERE credited AND settlement_id = $5), 0)::BIGINT,
			COALESCE(SUM(net_paise) FILTER (WHERE credited AND (settlement_id IS NULL OR settlement_id <> $5)), 0)::BIGINT,
			COALESCE(SUM(net_paise) FILTER (WHERE NOT credited), 0)::BIGINT
		FROM creator_fund_earnings
		WHERE creator_id = $1
		  AND day_bucket >= $2 AND day_bucket < $3
		  AND region_code = $4
		  AND status = 'settled'
	`, creatorID, start, end, regionCode, settlementID).Scan(
		&out.CreditedByThisSettlement, &out.CreditedElsewhere, &out.Uncredited,
	)
	return out, err
}

// ClaimAndCreditPeriodAccruals is the money-moving step, and the only one.
//
// The UPDATE ... WHERE credited = false is the concurrency control: it is
// a single atomic statement, so of two pods settling the same period one
// claims every uncredited row and the other claims none. Whatever comes
// back from RETURNING is exactly what this run is responsible for, and the
// wallet credit for precisely that amount happens in the same transaction.
// If the transaction rolls back, the rows go back to uncredited and the
// next run picks them up — there is no window in which a row is marked
// paid but the wallet did not move.
func (s *Store) ClaimAndCreditPeriodAccruals(ctx context.Context, in PeriodClaim) (ClaimedAccruals, error) {
	var out ClaimedAccruals
	if in.Currency == "" {
		in.Currency = "INR"
	}
	if in.RegionCode == "" {
		in.RegionCode = "IN"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		WITH claimed AS (
			UPDATE creator_fund_earnings
			SET credited = TRUE,
			    credited_at = NOW(),
			    settlement_id = $1
			WHERE creator_id = $2
			  AND day_bucket >= $3 AND day_bucket < $4
			  AND region_code = $5
			  AND status = 'settled'
			  AND credited = FALSE
			RETURNING gross_paise, platform_fee_paise, net_paise
		)
		SELECT COUNT(*)::BIGINT,
		       COALESCE(SUM(gross_paise), 0)::BIGINT,
		       COALESCE(SUM(platform_fee_paise), 0)::BIGINT,
		       COALESCE(SUM(net_paise), 0)::BIGINT
		FROM claimed
	`, in.SettlementID, in.CreatorID, in.PeriodStart, in.PeriodEnd, in.RegionCode).Scan(
		&out.Rows, &out.GrossPaise, &out.PlatformFeePaise, &out.NetPaise,
	)
	if err != nil {
		return ClaimedAccruals{}, err
	}
	if out.Rows == 0 || out.NetPaise <= 0 {
		// Nothing to pay. Commit anyway so a zero-row claim is still a
		// clean transaction rather than a rollback that looks like a
		// failure in the logs.
		if err := tx.Commit(ctx); err != nil {
			return ClaimedAccruals{}, err
		}
		return out, nil
	}

	// The wallet row must exist before we add to it — a creator whose
	// only prior activity was accruing has no creator_ledger row yet.
	if _, err := tx.Exec(ctx, `
		INSERT INTO creator_ledger (user_id, balance, currency)
		VALUES ($1, 0, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, in.CreatorID, in.Currency); err != nil {
		return ClaimedAccruals{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE creator_ledger
		SET balance = balance + $2,
		    lifetime_earnings = lifetime_earnings + $2,
		    updated_at = NOW()
		WHERE user_id = $1
	`, in.CreatorID, out.NetPaise); err != nil {
		return ClaimedAccruals{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO transactions (
			id, wallet_id, type, amount, currency, status,
			reference_type, reference_id, description, created_at
		) VALUES ($1, $2, 'creator_fund_period_settlement', $3, $4, 'completed',
		          'creator_fund_period', $5, $6, NOW())
	`, uuid.New(), in.CreatorID, out.NetPaise, in.Currency,
		in.SettlementID.String(), in.Description); err != nil {
		return ClaimedAccruals{}, err
	}

	// Double-entry legs, in the SAME transaction as the wallet credit.
	// claimID is this claim's identity: a settled period that later gains
	// a late accrual produces a second, separate claim with its own key,
	// so topping up cannot collide with the original entry and cannot be
	// silently swallowed either.
	claimID := uuid.New()
	if in.PlatformRevenueAccountID != uuid.Nil && in.CreatorWalletAccountID != uuid.Nil {
		if err := insertLedgerLeg(ctx, tx,
			in.PlatformRevenueAccountID, in.CreatorWalletAccountID,
			out.NetPaise, in.Currency, in.ReferenceType, in.SettlementID,
			"cfp_net:"+claimID.String(), "creator_fund period net credit",
		); err != nil {
			return ClaimedAccruals{}, err
		}
	}
	if out.PlatformFeePaise > 0 && in.PlatformRevenueAccountID != uuid.Nil && in.PlatformFeeAccountID != uuid.Nil {
		if err := insertLedgerLeg(ctx, tx,
			in.PlatformRevenueAccountID, in.PlatformFeeAccountID,
			out.PlatformFeePaise, in.Currency, in.ReferenceType, in.SettlementID,
			"cfp_fee:"+claimID.String(), "creator_fund period platform fee",
		); err != nil {
			return ClaimedAccruals{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ClaimedAccruals{}, err
	}
	return out, nil
}

func insertLedgerLeg(ctx context.Context, tx pgx.Tx, debitAccountID, creditAccountID uuid.UUID, amountPaise int64, currency, refType string, refID uuid.UUID, idempotencyKey, description string) error {
	if amountPaise <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, debit_account_id, credit_account_id, amount_paise, currency,
			reference_type, reference_id, idempotency_key, description, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
	`, uuid.New(), debitAccountID, creditAccountID, amountPaise, currency,
		refType, refID, idempotencyKey, description); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET balance_paise = balance_paise - $2 WHERE id = $1`,
		debitAccountID, amountPaise); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET balance_paise = balance_paise + $2 WHERE id = $1`,
		creditAccountID, amountPaise); err != nil {
		return err
	}
	return nil
}

// GetPeriodSettlement returns one stored statement, or nil.
func (s *Store) GetPeriodSettlement(ctx context.Context, creatorID uuid.UUID, periodKey, regionCode string) (*PeriodSettlement, error) {
	row := s.db.QueryRow(ctx, periodSettlementSelect+`
		WHERE creator_id = $1 AND period_key = $2 AND region_code = $3
	`, creatorID, periodKey, regionCode)
	ps, err := scanPeriodSettlement(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return ps, nil
}

// UpsertPeriodSettlement inserts the statement. ON CONFLICT DO NOTHING on
// (creator, period, region) means a second settlement of the same period
// cannot write a second row; the caller is handed the existing one and
// `inserted=false` so it can report the no-op rather than pretend.
func (s *Store) UpsertPeriodSettlement(ctx context.Context, p *PeriodSettlement) (*PeriodSettlement, bool, error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Status == "" {
		p.Status = "settled"
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO creator_fund_period_settlements (
			id, creator_id, period_key, period_start, period_end, region_code, currency,
			fund_rows, fund_views, fund_watch_time_ms, fund_gross_paise,
			fund_platform_fee_paise, fund_net_paise, fund_platform_fee_bps,
			tips_count, tips_gross_paise, tips_platform_fee_paise, tips_net_paise, tips_platform_fee_bps,
			subs_count, subs_gross_paise, subs_platform_fee_paise, subs_net_paise, subs_platform_fee_bps,
			gross_paise, platform_fee_paise, net_paise,
			credited_paise, already_credited_paise, pending_paise,
			status, settled_at, updated_at,
			reversed_paise, fund_reversed_rows, adjustments_paise, adjustments_count,
			budget_cap_paise, budget_exhausted_on_day, fund_rows_skipped
		) VALUES ($1,$2,$3,$4,$5,$6,$7,
		          $8,$9,$10,$11,$12,$13,$14,
		          $15,$16,$17,$18,$19,
		          $20,$21,$22,$23,$24,
		          $25,$26,$27,
		          $28,$29,$30,
		          $31, NOW(), NOW(),
		          $32, $33, $34, $35, $36, $37, $38)
		ON CONFLICT (creator_id, period_key, region_code) DO NOTHING
	`,
		p.ID, p.CreatorID, p.PeriodKey, p.PeriodStart, p.PeriodEnd, p.RegionCode, p.Currency,
		p.FundRows, p.FundViews, p.FundWatchTimeMs, p.FundGrossPaise,
		p.FundPlatformFeePaise, p.FundNetPaise, p.FundPlatformFeeBps,
		p.TipsCount, p.TipsGrossPaise, p.TipsPlatformFeePaise, p.TipsNetPaise, p.TipsPlatformFeeBps,
		p.SubsCount, p.SubsGrossPaise, p.SubsPlatformFeePaise, p.SubsNetPaise, p.SubsPlatformFeeBps,
		p.GrossPaise, p.PlatformFeePaise, p.NetPaise,
		p.CreditedPaise, p.AlreadyCreditedPaise, p.PendingPaise,
		p.Status,
		p.ReversedPaise, p.FundReversedRows, p.AdjustmentsPaise, p.AdjustmentsCount,
		p.BudgetCapPaise, p.BudgetExhaustedOnDay, p.FundRowsSkipped,
	)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		stored, err := s.GetPeriodSettlement(ctx, p.CreatorID, p.PeriodKey, p.RegionCode)
		return stored, true, err
	}
	stored, err := s.GetPeriodSettlement(ctx, p.CreatorID, p.PeriodKey, p.RegionCode)
	return stored, false, err
}

// RefreshPeriodSettlement updates the statement figures on an existing
// row. Used when a re-run finds late-arriving tips or a late accrual: the
// statement stays truthful without the settled_at moving, and
// credited_paise is passed in by the caller as (stored + newly claimed)
// so it can only ever go up by money that actually moved.
func (s *Store) RefreshPeriodSettlement(ctx context.Context, id uuid.UUID, p *PeriodSettlement) error {
	_, err := s.db.Exec(ctx, `
		UPDATE creator_fund_period_settlements SET
			fund_rows = $2, fund_views = $3, fund_watch_time_ms = $4,
			fund_gross_paise = $5, fund_platform_fee_paise = $6, fund_net_paise = $7,
			tips_count = $8, tips_gross_paise = $9, tips_platform_fee_paise = $10, tips_net_paise = $11,
			subs_count = $12, subs_gross_paise = $13, subs_platform_fee_paise = $14, subs_net_paise = $15,
			gross_paise = $16, platform_fee_paise = $17, net_paise = $18,
			credited_paise = $19, already_credited_paise = $20, pending_paise = $21,
			reversed_paise = $22, fund_reversed_rows = $23, adjustments_paise = $24, adjustments_count = $25,
			budget_cap_paise = $26, budget_exhausted_on_day = $27, fund_rows_skipped = $28,
			updated_at = NOW()
		WHERE id = $1
	`, id,
		p.FundRows, p.FundViews, p.FundWatchTimeMs,
		p.FundGrossPaise, p.FundPlatformFeePaise, p.FundNetPaise,
		p.TipsCount, p.TipsGrossPaise, p.TipsPlatformFeePaise, p.TipsNetPaise,
		p.SubsCount, p.SubsGrossPaise, p.SubsPlatformFeePaise, p.SubsNetPaise,
		p.GrossPaise, p.PlatformFeePaise, p.NetPaise,
		p.CreditedPaise, p.AlreadyCreditedPaise, p.PendingPaise,
		p.ReversedPaise, p.FundReversedRows, p.AdjustmentsPaise, p.AdjustmentsCount,
		p.BudgetCapPaise, p.BudgetExhaustedOnDay, p.FundRowsSkipped,
	)
	return err
}

// ListPeriodSettlements returns a creator's statements, newest first.
func (s *Store) ListPeriodSettlements(ctx context.Context, creatorID uuid.UUID, limit int) ([]PeriodSettlement, error) {
	if limit <= 0 || limit > 120 {
		limit = 12
	}
	rows, err := s.db.Query(ctx, periodSettlementSelect+`
		WHERE creator_id = $1
		ORDER BY period_start DESC, period_key DESC
		LIMIT $2
	`, creatorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PeriodSettlement
	for rows.Next() {
		p, err := scanPeriodSettlement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// SumTipsToRecipientBetween totals completed tips received in [from, to).
// The `tips` rows are the source of truth — the same rows the per-pair
// daily cap and the fraud checks were applied to on the way in. The
// monthly statement reads them; it never re-derives a tip from a wallet
// balance and never creates one.
func (s *Store) SumTipsToRecipientBetween(ctx context.Context, recipientID uuid.UUID, from, to time.Time) (count int64, totalPaise int64, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*)::BIGINT, COALESCE(SUM(amount_paise), 0)::BIGINT
		FROM tips
		WHERE recipient_id = $1
		  AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3
	`, recipientID, from, to).Scan(&count, &totalPaise)
	return
}

// SumSubscriptionEarningsBetween totals the creator's subscription
// revenue in [from, to) from the creator-side earning transaction rows
// written by Store.Subscribe and by the renewal worker. Those rows carry
// reference_type='subscription', which is what separates them from tip
// credits (reference_type=”) on the same wallet — so the two streams
// cannot double count each other.
func (s *Store) SumSubscriptionEarningsBetween(ctx context.Context, creatorID uuid.UUID, from, to time.Time) (count int64, totalPaise int64, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*)::BIGINT, COALESCE(SUM(amount), 0)::BIGINT
		FROM transactions
		WHERE wallet_id = $1
		  AND type = 'earning'
		  AND reference_type = 'subscription'
		  AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3
	`, creatorID, from, to).Scan(&count, &totalPaise)
	return
}

// ListCreatorsWithPeriodActivity returns every creator who could have a
// statement for [from, to): fund accruals, OR tips received, OR
// subscription revenue. Deliberately wider than "currently eligible" —
// a creator who was tipped but published nothing still gets a statement,
// and a creator whose eligibility lapsed still gets one for the days they
// accrued while eligible.
func (s *Store) ListCreatorsWithPeriodActivity(ctx context.Context, from, to time.Time) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT creator_id FROM creator_fund_eligibility WHERE status = 'eligible'
		UNION
		SELECT creator_id FROM creator_fund_earnings
		 WHERE day_bucket >= $1 AND day_bucket < $2
		UNION
		SELECT recipient_id FROM tips
		 WHERE status = 'completed' AND created_at >= $1 AND created_at < $2
		UNION
		SELECT wallet_id FROM transactions
		 WHERE type = 'earning' AND reference_type = 'subscription'
		   AND status = 'completed' AND created_at >= $1 AND created_at < $2
		UNION
		SELECT wallet_id FROM transactions
		 WHERE type = 'adjustment'
		   AND status = 'completed' AND created_at >= $1 AND created_at < $2
		ORDER BY 1
	`, from, to)
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

// TryAdvisorySettlementLock takes a session-scoped advisory lock for a
// settlement period so several instances do not all grind through the
// same batch. It is an optimisation, not the safety property: the safety
// property is the per-day `credited` claim plus the UNIQUE settlement
// key, both of which hold whether or not this lock was taken.
func (s *Store) TryAdvisorySettlementLock(ctx context.Context, key int64) (bool, func(), error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return false, func() {}, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		conn.Release()
		return false, func() {}, err
	}
	if !locked {
		conn.Release()
		return false, func() {}, nil
	}
	release := func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		conn.Release()
	}
	return true, release, nil
}

// ---------------------------------------------------------------------------
// scanning
// ---------------------------------------------------------------------------

const periodSettlementSelect = `
	SELECT id, creator_id, period_key, period_start, period_end, region_code, currency,
	       fund_rows, fund_views, fund_watch_time_ms, fund_gross_paise,
	       fund_platform_fee_paise, fund_net_paise, fund_platform_fee_bps,
	       tips_count, tips_gross_paise, tips_platform_fee_paise, tips_net_paise, tips_platform_fee_bps,
	       subs_count, subs_gross_paise, subs_platform_fee_paise, subs_net_paise, subs_platform_fee_bps,
	       gross_paise, platform_fee_paise, net_paise, credited_paise, already_credited_paise, pending_paise,
	       reversed_paise, fund_reversed_rows, adjustments_paise, adjustments_count,
	       budget_cap_paise, budget_exhausted_on_day, fund_rows_skipped,
	       status, settled_at
	FROM creator_fund_period_settlements
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPeriodSettlement(r rowScanner) (*PeriodSettlement, error) {
	var p PeriodSettlement
	err := r.Scan(
		&p.ID, &p.CreatorID, &p.PeriodKey, &p.PeriodStart, &p.PeriodEnd, &p.RegionCode, &p.Currency,
		&p.FundRows, &p.FundViews, &p.FundWatchTimeMs, &p.FundGrossPaise,
		&p.FundPlatformFeePaise, &p.FundNetPaise, &p.FundPlatformFeeBps,
		&p.TipsCount, &p.TipsGrossPaise, &p.TipsPlatformFeePaise, &p.TipsNetPaise, &p.TipsPlatformFeeBps,
		&p.SubsCount, &p.SubsGrossPaise, &p.SubsPlatformFeePaise, &p.SubsNetPaise, &p.SubsPlatformFeeBps,
		&p.GrossPaise, &p.PlatformFeePaise, &p.NetPaise, &p.CreditedPaise, &p.AlreadyCreditedPaise, &p.PendingPaise,
		&p.ReversedPaise, &p.FundReversedRows, &p.AdjustmentsPaise, &p.AdjustmentsCount,
		&p.BudgetCapPaise, &p.BudgetExhaustedOnDay, &p.FundRowsSkipped,
		&p.Status, &p.SettledAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
