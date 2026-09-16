package postgres

// Admin console (Wave 2 — Money). Two things live here:
//
//  1. The transactional forms of every admin write, so the service can
//     commit the change and its monetization_audit_log row together
//     (WithAuditTx). The pool forms elsewhere in this package delegate to
//     them, so there is one copy of each statement.
//  2. The read-only admin queries: the audit trail, payout requests, and
//     the dashboard counts (AdminStats).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Audit in the same transaction
// ---------------------------------------------------------------------------

// insertAuditLog appends one monetization_audit_log row on db — the pool,
// or the caller's transaction so the row commits with the change it
// records.
func insertAuditLog(ctx context.Context, db DBTX, entry *AuditLogEntry) error {
	entry.CreatedAt = time.Now()
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	_, err := db.Exec(ctx, `
		INSERT INTO monetization_audit_log (id, table_name, operation, old_data, new_data, performer_id, ip_address, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, entry.ID, entry.TableName, entry.Operation, nullableJSON(entry.OldData), nullableJSON(entry.NewData),
		entry.PerformerID, entry.IPAddress, entry.CreatedAt)
	return err
}

func nullableJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// WriteAuditLogTx is WriteAuditLog on the caller's transaction.
func (s *Store) WriteAuditLogTx(ctx context.Context, db DBTX, entry *AuditLogEntry) error {
	return insertAuditLog(ctx, db, entry)
}

// WithAuditTx runs fn and then appends entry, in one transaction: the
// change and its audit row commit together or not at all. fn may fill in
// entry (old and new data) as it learns them. A refused change (fn
// returns an error) writes no row.
func (s *Store) WithAuditTx(ctx context.Context, entry *AuditLogEntry, fn func(tx pgx.Tx) error) error {
	if entry == nil || entry.PerformerID == uuid.Nil {
		return errors.New("AUDIT_ACTOR_REQUIRED")
	}
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return insertAuditLog(ctx, tx, entry)
	})
}

// ---------------------------------------------------------------------------
// Wallet (creator ledger) freeze, unfreeze, rebuild
// ---------------------------------------------------------------------------

// SetWalletFrozenTx sets is_frozen for a user's ledger row and returns the
// previous value. WALLET_NOT_FOUND when the user has no ledger row.
func (s *Store) SetWalletFrozenTx(ctx context.Context, db DBTX, userID uuid.UUID, frozen bool) (bool, error) {
	var previous bool
	err := db.QueryRow(ctx, `SELECT is_frozen FROM creator_ledger WHERE user_id = $1 FOR UPDATE`, userID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("WALLET_NOT_FOUND")
	}
	if err != nil {
		return false, err
	}
	if _, err := db.Exec(ctx, `
		UPDATE creator_ledger SET is_frozen = $2, updated_at = NOW() WHERE user_id = $1
	`, userID, frozen); err != nil {
		return false, err
	}
	return previous, nil
}

// RebuildWalletFromLedgerTx recomputes a ledger balance from the
// double-entry legs and writes it. previous is nil when the user has no
// ledger row (the UPDATE then touches nothing, as it always has).
func (s *Store) RebuildWalletFromLedgerTx(ctx context.Context, db DBTX, userID uuid.UUID) (previous *int64, balance int64, err error) {
	var old int64
	switch err := db.QueryRow(ctx, `SELECT balance FROM creator_ledger WHERE user_id = $1 FOR UPDATE`, userID).Scan(&old); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, 0, err
	default:
		previous = &old
	}
	err = db.QueryRow(ctx, `
		WITH user_accounts AS (
			SELECT id FROM accounts WHERE owner_id = $1 AND account_type = 'user_wallet'
		)
		SELECT COALESCE(
			(SELECT COALESCE(SUM(le.amount_paise), 0) FROM ledger_entries le JOIN user_accounts ua ON le.credit_account_id = ua.id), 0
		) - COALESCE(
			(SELECT COALESCE(SUM(le.amount_paise), 0) FROM ledger_entries le JOIN user_accounts ua ON le.debit_account_id = ua.id), 0
		)
	`, userID).Scan(&balance)
	if err != nil {
		return nil, 0, err
	}
	if _, err := db.Exec(ctx, `
		UPDATE creator_ledger SET balance = $2, updated_at = NOW() WHERE user_id = $1
	`, userID, balance); err != nil {
		return nil, 0, err
	}
	return previous, balance, nil
}

// ---------------------------------------------------------------------------
// Fraud reviews and disputes
// ---------------------------------------------------------------------------

// ResolveFraudReviewTx decides one fraud review and returns its previous
// status. FRAUD_REVIEW_NOT_FOUND when there is no such review.
func (s *Store) ResolveFraudReviewTx(ctx context.Context, db DBTX, reviewID uuid.UUID, status, notes string, reviewerID uuid.UUID) (string, error) {
	var previous string
	err := db.QueryRow(ctx, `SELECT status FROM fraud_reviews WHERE id = $1 FOR UPDATE`, reviewID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("FRAUD_REVIEW_NOT_FOUND")
	}
	if err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `
		UPDATE fraud_reviews
		SET status = $2, notes = $3, reviewer_id = $4, resolved_at = NOW()
		WHERE id = $1
	`, reviewID, status, notes, reviewerID); err != nil {
		return "", err
	}
	return previous, nil
}

// ResolveDisputeTx updates one dispute and returns its previous status.
// DISPUTE_NOT_FOUND when there is no such dispute.
func (s *Store) ResolveDisputeTx(ctx context.Context, db DBTX, disputeID uuid.UUID, status, notes string, resolvedBy uuid.UUID) (string, error) {
	var previous string
	err := db.QueryRow(ctx, `SELECT status FROM disputes WHERE id = $1 FOR UPDATE`, disputeID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("DISPUTE_NOT_FOUND")
	}
	if err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `
		UPDATE disputes
		SET status = $2, resolution_notes = $3, resolved_by = $4, resolved_at = NOW()
		WHERE id = $1
	`, disputeID, status, notes, resolvedBy); err != nil {
		return "", err
	}
	return previous, nil
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

// GetTransactionForUpdateTx reads one transaction under a row lock, so two
// refunds of the same transaction serialise on it. nil when absent.
func (s *Store) GetTransactionForUpdateTx(ctx context.Context, db DBTX, txnID uuid.UUID) (*Transaction, error) {
	var t Transaction
	err := db.QueryRow(ctx, `
		SELECT id, wallet_id, type, amount, currency, status, reference_type, reference_id, COALESCE(description, ''), created_at
		FROM transactions
		WHERE id = $1
		FOR UPDATE
	`, txnID).Scan(
		&t.ID, &t.WalletID, &t.Type, &t.AmountPaise, &t.Currency,
		&t.Status, &t.ReferenceType, &t.ReferenceID, &t.Description, &t.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetRefundByTransactionTx is GetRefundByTransaction on db.
func (s *Store) GetRefundByTransactionTx(ctx context.Context, db DBTX, txnID uuid.UUID) (*Refund, error) {
	var r Refund
	err := db.QueryRow(ctx, `
		SELECT id, transaction_id, dispute_id, amount_paise, reason, status, processed_at, created_at
		FROM refunds
		WHERE transaction_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, txnID).Scan(
		&r.ID, &r.TransactionID, &r.DisputeID, &r.AmountPaise, &r.Reason,
		&r.Status, &r.ProcessedAt, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateRefundTx is CreateRefund on db.
func (s *Store) CreateRefundTx(ctx context.Context, db DBTX, refund *Refund) (*Refund, error) {
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	refund.CreatedAt = time.Now()
	if _, err := db.Exec(ctx, `
		INSERT INTO refunds (id, transaction_id, dispute_id, amount_paise, reason, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, refund.ID, refund.TransactionID, refund.DisputeID, refund.AmountPaise, refund.Reason, refund.Status, refund.CreatedAt); err != nil {
		return nil, err
	}
	return refund, nil
}

// CreditWalletTx is CreditWallet on db.
func (s *Store) CreditWalletTx(ctx context.Context, db DBTX, userID uuid.UUID, amountPaise int64) error {
	_, err := db.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance + $2, updated_at = NOW() WHERE user_id = $1
	`, userID, amountPaise)
	return err
}

// CreateTransactionTx is CreateTransaction on db.
func (s *Store) CreateTransactionTx(ctx context.Context, db DBTX, t *Transaction) error {
	t.CreatedAt = time.Now()
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	_, err := db.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, t.ID, t.WalletID, t.Type, t.AmountPaise, t.Currency, t.Status,
		t.ReferenceType, t.ReferenceID, t.Description, t.CreatedAt)
	return err
}

// ---------------------------------------------------------------------------
// Creator fund: suspension, rates, bands, budgets
// ---------------------------------------------------------------------------

// CreatorFundStatusTx returns a creator's eligibility status under a row
// lock, or "" when the creator has no eligibility row.
func (s *Store) CreatorFundStatusTx(ctx context.Context, db DBTX, creatorID uuid.UUID) (string, error) {
	var status string
	err := db.QueryRow(ctx, `SELECT status FROM creator_fund_eligibility WHERE creator_id = $1 FOR UPDATE`, creatorID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return status, err
}

// SetCreatorFundSuspensionTx is SetCreatorFundSuspension on db.
func (s *Store) SetCreatorFundSuspensionTx(ctx context.Context, db DBTX, creatorID uuid.UUID, reason string) error {
	now := time.Now()
	_, err := db.Exec(ctx, `
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

// SetRpmRateTx is SetRpmRate on the caller's transaction.
func (s *Store) SetRpmRateTx(ctx context.Context, tx pgx.Tx, contentType, regionCode string, rpmPaise int64, notes string, createdBy *uuid.UUID) (*RpmRate, error) {
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
	return r, nil
}

// ActiveRpmRateTx returns the active rate for (content type, region) under
// a row lock, or nil.
func (s *Store) ActiveRpmRateTx(ctx context.Context, tx pgx.Tx, contentType, regionCode string) (*RpmRate, error) {
	var r RpmRate
	err := tx.QueryRow(ctx, `
		SELECT id, rpm_paise, effective_from FROM monetization_rpm_rates
		WHERE content_type = $1 AND region_code = $2 AND effective_to IS NULL
		ORDER BY effective_from DESC LIMIT 1
		FOR UPDATE
	`, contentType, regionCode).Scan(&r.ID, &r.RpmPaise, &r.EffectiveFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ContentType, r.RegionCode = contentType, regionCode
	return &r, nil
}

// SetQualityBandTx is SetQualityBand on the caller's transaction.
func (s *Store) SetQualityBandTx(ctx context.Context, tx pgx.Tx, b *QualityBandRow) (*QualityBandRow, error) {
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
	return b, nil
}

// UpsertCreatorFundBudgetTx is UpsertCreatorFundBudget on the caller's
// transaction. It returns the row as it was before the change (nil when
// the period had no cap) and as stored.
func (s *Store) UpsertCreatorFundBudgetTx(ctx context.Context, tx pgx.Tx, b *CreatorFundBudget) (previous, stored *CreatorFundBudget, err error) {
	if b.RegionCode == "" {
		b.RegionCode = "IN"
	}
	if b.CapPaise < 0 {
		return nil, nil, errors.New("INVALID_BUDGET: cap_paise must be >= 0")
	}
	existing, err := lockBudget(ctx, tx, b.PeriodKey, b.RegionCode)
	if err != nil {
		return nil, nil, err
	}
	if existing == nil {
		row := tx.QueryRow(ctx, `
			INSERT INTO creator_fund_budgets (period_key, region_code, cap_paise, accrued_paise, notes, created_by)
			VALUES ($1, $2, $3, 0, $4, $5)
			RETURNING period_key, region_code, cap_paise, accrued_paise, exhausted_at, exhausted_on_day,
			          COALESCE(notes, ''), created_by, created_at, updated_at
		`, b.PeriodKey, b.RegionCode, b.CapPaise, nullableString(b.Notes), b.CreatedBy)
		stored, err = scanBudget(row)
		return nil, stored, err
	}
	if b.CapPaise < existing.AccruedPaise {
		return nil, nil, fmt.Errorf("%w: cap %d, accrued %d for %s/%s", ErrBudgetBelowAccrued,
			b.CapPaise, existing.AccruedPaise, b.PeriodKey, b.RegionCode)
	}
	row := tx.QueryRow(ctx, `
		UPDATE creator_fund_budgets
		SET cap_paise        = $3,
		    exhausted_at     = CASE WHEN accrued_paise < $3 THEN NULL ELSE COALESCE(exhausted_at, NOW()) END,
		    exhausted_on_day = CASE WHEN accrued_paise < $3 THEN NULL ELSE exhausted_on_day END,
		    notes            = COALESCE($4, notes),
		    created_by       = COALESCE($5, created_by),
		    updated_at       = NOW()
		WHERE period_key = $1 AND region_code = $2
		RETURNING period_key, region_code, cap_paise, accrued_paise, exhausted_at, exhausted_on_day,
		          COALESCE(notes, ''), created_by, created_at, updated_at
	`, b.PeriodKey, b.RegionCode, b.CapPaise, nullableString(b.Notes), b.CreatedBy)
	stored, err = scanBudget(row)
	return existing, stored, err
}

// ---------------------------------------------------------------------------
// Admin reads
// ---------------------------------------------------------------------------

// AuditLogFilter narrows ListAuditLog. Zero fields do not filter.
type AuditLogFilter struct {
	TableName   string
	Operation   string
	PerformerID uuid.UUID
	Before      time.Time
	Limit       int
}

// ListAuditLog returns monetization_audit_log rows, newest first. Limit is
// clamped to 1..200 (default 50); page with Before = the last created_at.
func (s *Store) ListAuditLog(ctx context.Context, f AuditLogFilter) ([]AuditLogEntry, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var performer any
	if f.PerformerID != uuid.Nil {
		performer = f.PerformerID
	}
	var before any
	if !f.Before.IsZero() {
		before = f.Before
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, table_name, operation, old_data, new_data, COALESCE(performer_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(ip_address, ''), created_at
		FROM monetization_audit_log
		WHERE ($1 = '' OR table_name = $1)
		  AND ($2 = '' OR operation = $2)
		  AND ($3::uuid IS NULL OR performer_id = $3::uuid)
		  AND ($4::timestamptz IS NULL OR created_at < $4::timestamptz)
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`, f.TableName, f.Operation, performer, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditLogEntry{}
	for rows.Next() {
		var e AuditLogEntry
		var oldData, newData []byte
		if err := rows.Scan(&e.ID, &e.TableName, &e.Operation, &oldData, &newData, &e.PerformerID, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.OldData, e.NewData = oldData, newData
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListPayoutRequests returns payout requests, oldest first, optionally of
// one status. Read-only: the console's payout queue while payouts are off.
func (s *Store) ListPayoutRequests(ctx context.Context, status string, limit, offset int) ([]PayoutRequestRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+payoutRequestColumns+`
		FROM payout_requests
		WHERE ($1 = '' OR status = $1)
		ORDER BY requested_at ASC, id ASC
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PayoutRequestRow{}
	for rows.Next() {
		r, err := scanPayoutRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// PendingPayoutStatuses are the payout_requests states that are waiting on
// someone: not yet submitted to the provider (requested, reserved) or held
// for review.
var PendingPayoutStatuses = []string{"requested", "reserved", "held"}

// Settlement-run audit operations (service.AdminSettle*). A run spans many
// transactions, so it is recorded as a start row before anything is
// touched and a finish row carrying the outcome; both share new_data.run_id.
const (
	AuditTableSettlements        = "creator_fund_period_settlements"
	AuditOpSettleStart           = "settle_period.start"
	AuditOpSettleFinish          = "settle_period.finish"
	AuditOpSettleCreatorStart    = "settle_period_creator.start"
	AuditOpSettleCreatorFinish   = "settle_period_creator.finish"
	AuditTableEarnings           = "creator_fund_earnings"
	AuditOpAccrueDayStart        = "accrue_day.start"
	AuditOpAccrueDayFinish       = "accrue_day.finish"
	SettlementRunStatusCompleted = "completed"
	SettlementRunStatusFailed    = "failed"
	SettlementRunStatusRunning   = "incomplete"
)

// SettlementRunStat is the last admin-triggered settlement run.
type SettlementRunStat struct {
	RunID     string     `json:"run_id"`
	Operation string     `json:"operation"`
	PeriodKey string     `json:"period_key"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Status: completed | failed | incomplete (a start with no finish:
	// still running, or the process died mid-run).
	Status  string    `json:"status"`
	ActorID uuid.UUID `json:"actor_id"`
}

// AdminPeriodBudget is the current settlement period's accrual against its cap.
type AdminPeriodBudget struct {
	PeriodKey    string `json:"period_key"`
	AccruedPaise int64  `json:"accrued_paise"`
	// CapPaise is nil when no budget row exists for the period (uncapped).
	CapPaise *int64 `json:"cap_paise"`
	Capped   bool   `json:"capped"`
}

// AdminStats is the Money dashboard's counts. Money in integer paise.
type AdminStats struct {
	OpenFraudReviews      int64              `json:"open_fraud_reviews"`
	FrozenWallets         int64              `json:"frozen_wallets"`
	PendingPayoutRequests int64              `json:"pending_payout_requests"`
	PendingPayoutPaise    int64              `json:"pending_payout_paise"`
	CurrentPeriod         AdminPeriodBudget  `json:"current_period"`
	ReversalsLast7Days    int64              `json:"reversals_last_7_days"`
	OpenDisputes          int64              `json:"open_disputes"`
	LastSettlementRun     *SettlementRunStat `json:"last_settlement_run"`
	// LastSettlementWriteAt is the newest creator_fund_period_settlements
	// row from any source, the scheduled worker included (its runs write no
	// audit row); nil when nothing was ever settled.
	LastSettlementWriteAt *time.Time `json:"last_settlement_write_at"`
	GeneratedAt           time.Time  `json:"generated_at"`
}

// AdminStats reads the dashboard counts. periodKey and [start, end) are the
// current settlement period (the service knows the cadence).
func (s *Store) AdminStats(ctx context.Context, now time.Time, periodKey string, start, end time.Time) (*AdminStats, error) {
	st := &AdminStats{GeneratedAt: now}
	st.CurrentPeriod.PeriodKey = periodKey
	if err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM fraud_reviews WHERE status IN ('pending', 'investigating')),
			(SELECT count(*) FROM creator_ledger WHERE is_frozen),
			(SELECT count(*) FROM payout_requests WHERE status = ANY($1)),
			(SELECT COALESCE(SUM(amount), 0)::BIGINT FROM payout_requests WHERE status = ANY($1)),
			(SELECT count(*) FROM creator_fund_earnings WHERE reversed_at >= $2),
			(SELECT count(*) FROM disputes WHERE status IN ('open', 'investigating')),
			(SELECT max(updated_at) FROM creator_fund_period_settlements)
	`, PendingPayoutStatuses, now.Add(-7*24*time.Hour)).Scan(
		&st.OpenFraudReviews, &st.FrozenWallets, &st.PendingPayoutRequests, &st.PendingPayoutPaise,
		&st.ReversalsLast7Days, &st.OpenDisputes, &st.LastSettlementWriteAt,
	); err != nil {
		return nil, err
	}

	var budgets int64
	var capSum, accruedSum int64
	if err := s.db.QueryRow(ctx, `
		SELECT count(*), COALESCE(SUM(cap_paise), 0)::BIGINT, COALESCE(SUM(accrued_paise), 0)::BIGINT
		FROM creator_fund_budgets WHERE period_key = $1
	`, periodKey).Scan(&budgets, &capSum, &accruedSum); err != nil {
		return nil, err
	}
	if budgets > 0 {
		st.CurrentPeriod.Capped = true
		st.CurrentPeriod.CapPaise = &capSum
		st.CurrentPeriod.AccruedPaise = accruedSum
	} else if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(gross_paise), 0)::BIGINT FROM creator_fund_earnings
		WHERE day_bucket >= $1 AND day_bucket < $2 AND reversed_at IS NULL
	`, start, end).Scan(&st.CurrentPeriod.AccruedPaise); err != nil {
		return nil, err
	}

	run, err := s.lastSettlementRun(ctx)
	if err != nil {
		return nil, err
	}
	st.LastSettlementRun = run
	return st, nil
}

func (s *Store) lastSettlementRun(ctx context.Context) (*SettlementRunStat, error) {
	var run SettlementRunStat
	var actor *uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(new_data->>'run_id', ''), operation, COALESCE(new_data->>'period_key', ''), created_at, performer_id
		FROM monetization_audit_log
		WHERE table_name = $1 AND operation IN ($2, $3)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, AuditTableSettlements, AuditOpSettleStart, AuditOpSettleCreatorStart).Scan(
		&run.RunID, &run.Operation, &run.PeriodKey, &run.StartedAt, &actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if actor != nil {
		run.ActorID = *actor
	}
	run.Status = SettlementRunStatusRunning
	var status string
	var ended time.Time
	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(new_data->>'status', ''), created_at
		FROM monetization_audit_log
		WHERE table_name = $1 AND operation IN ($2, $3) AND new_data->>'run_id' = $4
		ORDER BY created_at DESC LIMIT 1
	`, AuditTableSettlements, AuditOpSettleFinish, AuditOpSettleCreatorFinish, run.RunID).Scan(&status, &ended)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		run.Status = status
		run.EndedAt = &ended
	}
	return &run, nil
}
