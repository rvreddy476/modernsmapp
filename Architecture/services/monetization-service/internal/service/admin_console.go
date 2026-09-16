package service

// Admin writes with their audit row (admin console, Wave 2 — Money).
//
// Every admin write goes through one of the Admin* methods below, whichever
// door it came in by: the admin-service token family (actor = the token's
// signed act claim) or the legacy /v1/monetization/admin routes (actor =
// X-User-Id behind the admin scope). Each commits its monetization_audit_log
// row in the SAME transaction as the change, so there is no change without
// its row and no row for a change that rolled back.
//
// The exception is a settlement run, which spans one transaction per
// creator: it is recorded as a start row written BEFORE anything is touched
// (a run whose start cannot be recorded does not run) and a finish row with
// the outcome, both carrying the same run_id.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// How an admin request reached the service, recorded in new_data.via.
const (
	ViaAdminService = "admin-service"
	ViaGateway      = "gateway"
)

// AdminActor is the human performing an admin write.
type AdminActor struct {
	ID  uuid.UUID
	IP  string
	Via string
}

var (
	ErrAdminActorRequired = errors.New("ADMIN_ACTOR_REQUIRED")
	ErrInvalidRefund      = errors.New("INVALID_REFUND_AMOUNT")
)

func (a AdminActor) check() error {
	if a.ID == uuid.Nil {
		return ErrAdminActorRequired
	}
	return nil
}

// entry starts an audit row for table/op; old and new data are filled in
// as the change learns them.
func (a AdminActor) entry(table, op string) *postgres.AuditLogEntry {
	return &postgres.AuditLogEntry{TableName: table, Operation: op, PerformerID: a.ID, IPAddress: a.IP}
}

// data marshals an audit payload, stamping how the request arrived.
func (a AdminActor) data(fields map[string]any) json.RawMessage {
	if a.Via != "" {
		fields["via"] = a.Via
	}
	b, _ := json.Marshal(fields)
	return b
}

func auditJSON(fields map[string]any) json.RawMessage {
	b, _ := json.Marshal(fields)
	return b
}

// ---------------------------------------------------------------------------
// Wallet (creator ledger)
// ---------------------------------------------------------------------------

// AdminFreezeWallet freezes a creator ledger, audited.
func (s *Service) AdminFreezeWallet(ctx context.Context, a AdminActor, userID uuid.UUID) error {
	return s.adminSetWalletFrozen(ctx, a, userID, true, "freeze")
}

// AdminUnfreezeWallet unfreezes a creator ledger, audited.
func (s *Service) AdminUnfreezeWallet(ctx context.Context, a AdminActor, userID uuid.UUID) error {
	return s.adminSetWalletFrozen(ctx, a, userID, false, "unfreeze")
}

func (s *Service) adminSetWalletFrozen(ctx context.Context, a AdminActor, userID uuid.UUID, frozen bool, op string) error {
	if err := a.check(); err != nil {
		return err
	}
	e := a.entry("creator_ledger", op)
	return s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, err := s.store.SetWalletFrozenTx(ctx, tx, userID, frozen)
		if err != nil {
			return err
		}
		e.OldData = auditJSON(map[string]any{"user_id": userID, "is_frozen": previous})
		e.NewData = a.data(map[string]any{"user_id": userID, "is_frozen": frozen})
		return nil
	})
}

// AdminRebuildWallet recomputes a ledger balance from the double-entry
// legs, audited with the balance before and after.
func (s *Service) AdminRebuildWallet(ctx context.Context, a AdminActor, userID uuid.UUID) (int64, error) {
	if err := a.check(); err != nil {
		return 0, err
	}
	var balance int64
	e := a.entry("creator_ledger", "rebuild")
	err := s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, b, err := s.store.RebuildWalletFromLedgerTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		balance = b
		e.OldData = auditJSON(map[string]any{"user_id": userID, "balance_paise": previous})
		e.NewData = a.data(map[string]any{"user_id": userID, "balance_paise": b})
		return nil
	})
	return balance, err
}

// ---------------------------------------------------------------------------
// Fraud reviews and disputes
// ---------------------------------------------------------------------------

// AdminResolveFraudReview decides a fraud review, audited.
func (s *Service) AdminResolveFraudReview(ctx context.Context, a AdminActor, reviewID uuid.UUID, status, notes string) error {
	if err := a.check(); err != nil {
		return err
	}
	e := a.entry("fraud_reviews", "decide")
	err := s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, err := s.store.ResolveFraudReviewTx(ctx, tx, reviewID, status, notes, a.ID)
		if err != nil {
			return err
		}
		e.OldData = auditJSON(map[string]any{"id": reviewID, "status": previous})
		e.NewData = a.data(map[string]any{"id": reviewID, "status": status, "notes": notes})
		return nil
	})
	if err == nil {
		slog.InfoContext(ctx, "fraud review resolved", "review_id", reviewID, "status", status, "reviewer_id", a.ID, "via", a.Via)
	}
	return err
}

// AdminResolveDispute updates a dispute, audited.
func (s *Service) AdminResolveDispute(ctx context.Context, a AdminActor, disputeID uuid.UUID, status, notes string) error {
	if err := a.check(); err != nil {
		return err
	}
	e := a.entry("disputes", "update")
	err := s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, err := s.store.ResolveDisputeTx(ctx, tx, disputeID, status, notes, a.ID)
		if err != nil {
			return err
		}
		e.OldData = auditJSON(map[string]any{"id": disputeID, "status": previous})
		e.NewData = a.data(map[string]any{"id": disputeID, "status": status, "resolution_notes": notes})
		return nil
	})
	if err == nil {
		slog.InfoContext(ctx, "dispute resolved", "dispute_id", disputeID, "status", status, "admin_id", a.ID, "via", a.Via)
	}
	return err
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

// AdminProcessRefund records a refund, credits the creator ledger and writes
// the refund transaction and the audit row — all in one transaction. (Before
// the console these were three separate writes, and a failed transaction
// insert was only logged.) The transaction row is locked, so two refunds of
// one transaction serialise and the second sees the first. The amount must
// be positive and no more than the refunded transaction.
func (s *Service) AdminProcessRefund(ctx context.Context, a AdminActor, transactionID uuid.UUID, amountPaise int64, reason string, disputeID *uuid.UUID) (*postgres.Refund, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if amountPaise <= 0 {
		return nil, fmt.Errorf("%w: amount_paise must be greater than zero", ErrInvalidRefund)
	}
	var out *postgres.Refund
	e := a.entry("refunds", "create")
	err := s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		txn, err := s.store.GetTransactionForUpdateTx(ctx, tx, transactionID)
		if err != nil {
			return fmt.Errorf("lookup transaction: %w", err)
		}
		if txn == nil {
			return fmt.Errorf("TRANSACTION_NOT_FOUND")
		}
		if amountPaise > txn.AmountPaise {
			return fmt.Errorf("%w: amount_paise %d exceeds the transaction's %d", ErrInvalidRefund, amountPaise, txn.AmountPaise)
		}
		existing, err := s.store.GetRefundByTransactionTx(ctx, tx, transactionID)
		if err != nil {
			return fmt.Errorf("check existing refund: %w", err)
		}
		if existing != nil {
			return fmt.Errorf("REFUND_ALREADY_EXISTS")
		}
		out, err = s.store.CreateRefundTx(ctx, tx, &postgres.Refund{
			TransactionID: transactionID,
			DisputeID:     disputeID,
			AmountPaise:   amountPaise,
			Reason:        reason,
			Status:        "pending",
		})
		if err != nil {
			return fmt.Errorf("create refund: %w", err)
		}
		if err := s.store.CreditWalletTx(ctx, tx, txn.WalletID, amountPaise); err != nil {
			return fmt.Errorf("credit wallet: %w", err)
		}
		if err := s.store.CreateTransactionTx(ctx, tx, &postgres.Transaction{
			WalletID:      txn.WalletID,
			Type:          "refund",
			AmountPaise:   amountPaise,
			Currency:      txn.Currency,
			Status:        "completed",
			ReferenceType: "refund",
			ReferenceID:   out.ID.String(),
			Description:   fmt.Sprintf("Refund: %s", reason),
		}); err != nil {
			return fmt.Errorf("create refund transaction: %w", err)
		}
		e.NewData = a.data(map[string]any{
			"refund_id": out.ID, "transaction_id": transactionID, "user_id": txn.WalletID,
			"amount_paise": amountPaise, "reason": reason, "dispute_id": disputeID,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "refund processed", "refund_id", out.ID, "transaction_id", transactionID, "amount_paise", amountPaise, "admin_id", a.ID, "via", a.Via)
	return out, nil
}

// ---------------------------------------------------------------------------
// Creator fund: suspension, rates, bands, budgets, reversals
// ---------------------------------------------------------------------------

// AdminSuspendCreatorFund suspends a creator from the fund, audited.
func (s *Service) AdminSuspendCreatorFund(ctx context.Context, a AdminActor, creatorID uuid.UUID, reason string) error {
	if err := a.check(); err != nil {
		return err
	}
	if reason == "" {
		reason = "admin action"
	}
	e := a.entry("creator_fund_eligibility", "suspend")
	return s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, err := s.store.CreatorFundStatusTx(ctx, tx, creatorID)
		if err != nil {
			return err
		}
		if err := s.store.SetCreatorFundSuspensionTx(ctx, tx, creatorID, reason); err != nil {
			return err
		}
		e.OldData = auditJSON(map[string]any{"creator_id": creatorID, "status": previous})
		e.NewData = a.data(map[string]any{"creator_id": creatorID, "status": "suspended", "reason": reason})
		return nil
	})
}

// AdminUnsuspendCreatorFund clears a creator's suspension, audited.
func (s *Service) AdminUnsuspendCreatorFund(ctx context.Context, a AdminActor, creatorID uuid.UUID) error {
	if err := a.check(); err != nil {
		return err
	}
	e := a.entry("creator_fund_eligibility", "unsuspend")
	return s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, err := s.store.CreatorFundStatusTx(ctx, tx, creatorID)
		if err != nil {
			return err
		}
		if err := s.store.ClearCreatorFundSuspensionTx(ctx, tx, creatorID); err != nil {
			return err
		}
		e.OldData = auditJSON(map[string]any{"creator_id": creatorID, "status": previous})
		e.NewData = a.data(map[string]any{"creator_id": creatorID, "status": "pending"})
		return nil
	})
}

// AdminSetRpmRate sets the active rate, audited with the rate it replaced.
func (s *Service) AdminSetRpmRate(ctx context.Context, a AdminActor, contentType, regionCode string, rpmPaise int64, notes string) (*postgres.RpmRate, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	regionCode, err := validateRpmRate(contentType, regionCode, rpmPaise)
	if err != nil {
		return nil, err
	}
	var rate *postgres.RpmRate
	e := a.entry("monetization_rpm_rates", "set")
	err = s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, err := s.store.ActiveRpmRateTx(ctx, tx, contentType, regionCode)
		if err != nil {
			return err
		}
		actor := a.ID
		rate, err = s.store.SetRpmRateTx(ctx, tx, contentType, regionCode, rpmPaise, notes, &actor)
		if err != nil {
			return err
		}
		if previous != nil {
			e.OldData = auditJSON(map[string]any{"id": previous.ID, "content_type": contentType, "region_code": regionCode, "rpm_paise": previous.RpmPaise})
		}
		e.NewData = a.data(map[string]any{"id": rate.ID, "content_type": contentType, "region_code": regionCode, "rpm_paise": rpmPaise, "notes": notes})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rate, nil
}

// AdminSetQualityBand sets the active quality band, audited.
func (s *Service) AdminSetQualityBand(ctx context.Context, a AdminActor, band postgres.QualityBandRow) (*postgres.QualityBandRow, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	if err := validateQualityBand(&band); err != nil {
		return nil, err
	}
	actor := a.ID
	band.CreatedBy = &actor
	e := a.entry("monetization_quality_bands", "set")
	err := s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		if _, err := s.store.SetQualityBandTx(ctx, tx, &band); err != nil {
			return err
		}
		e.NewData = a.data(map[string]any{
			"id": band.ID, "content_type": band.ContentType, "region_code": band.RegionCode,
			"floor_bps": band.FloorBps, "ceiling_bps": band.CeilingBps, "pivot_cqs": band.PivotCQS,
			"confidence_impressions": band.ConfidenceImpressions, "enabled": band.Enabled, "notes": band.Notes,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &band, nil
}

// AdminUpsertCreatorFundBudget creates or changes a period's cap, audited
// with the cap before and after.
func (s *Service) AdminUpsertCreatorFundBudget(ctx context.Context, a AdminActor, in BudgetInput) (*postgres.CreatorFundBudget, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	actor := a.ID
	row, err := s.budgetRow(in, &actor)
	if err != nil {
		return nil, err
	}
	var stored *postgres.CreatorFundBudget
	e := a.entry("creator_fund_budgets", "set")
	err = s.store.WithAuditTx(ctx, e, func(tx pgx.Tx) error {
		previous, b, err := s.store.UpsertCreatorFundBudgetTx(ctx, tx, row)
		if err != nil {
			return err
		}
		stored = b
		if previous != nil {
			e.OldData = auditJSON(map[string]any{"period_key": previous.PeriodKey, "region_code": previous.RegionCode, "cap_paise": previous.CapPaise, "accrued_paise": previous.AccruedPaise})
		}
		e.NewData = a.data(map[string]any{"period_key": b.PeriodKey, "region_code": b.RegionCode, "cap_paise": b.CapPaise, "accrued_paise": b.AccruedPaise, "notes": in.Notes})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// AdminReverseFundEarning reverses one accrual row. The audit row (table
// creator_fund_earnings, operation reverse — the names the remediation
// runbook counts) now commits inside the reversal's transaction; before
// the console it was written after commit and a failure only logged.
// Reversing an already-reversed row moves nothing and writes no row.
func (s *Service) AdminReverseFundEarning(ctx context.Context, a AdminActor, earningID uuid.UUID, reason string) (*ReversalResult, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	return s.reverseFundEarning(ctx, earningID, reason, func(tx pgx.Tx, out *ReversalResult) error {
		if out.AlreadyReversed {
			return nil
		}
		newData, err := json.Marshal(out)
		if err != nil {
			return err
		}
		e := a.entry(postgres.AuditTableEarnings, "reverse")
		e.NewData = newData
		return s.store.WriteAuditLogTx(ctx, tx, e)
	})
}

// ---------------------------------------------------------------------------
// Settlement runs
// ---------------------------------------------------------------------------

// adminRun records a start row, runs fn, and records a finish row with the
// outcome. A start row that cannot be written stops the run before it
// touches anything.
func (s *Service) adminRun(ctx context.Context, a AdminActor, table, startOp, finishOp string, fields map[string]any, fn func() (any, error)) error {
	if err := a.check(); err != nil {
		return err
	}
	runID := uuid.New()
	fields["run_id"] = runID
	start := a.entry(table, startOp)
	start.NewData = a.data(fields)
	if err := s.store.WriteAuditLog(ctx, start); err != nil {
		return fmt.Errorf("record %s: %w", startOp, err)
	}
	result, runErr := fn()
	finish := map[string]any{"run_id": runID, "status": postgres.SettlementRunStatusCompleted, "result": result}
	for k, v := range fields {
		if _, set := finish[k]; !set {
			finish[k] = v
		}
	}
	if runErr != nil {
		finish["status"] = postgres.SettlementRunStatusFailed
		finish["error"] = runErr.Error()
	}
	end := a.entry(table, finishOp)
	end.NewData = a.data(finish)
	if err := s.store.WriteAuditLog(ctx, end); err != nil {
		// The run already happened and is idempotent; the missing finish row
		// leaves the run reading "incomplete" on the dashboard.
		slog.ErrorContext(ctx, "admin settlement run: finish audit row not written", "run_id", runID, "operation", finishOp, "error", err)
	}
	return runErr
}

// AdminSettleCreatorFundPeriodForAll is the payment run fired by an admin.
func (s *Service) AdminSettleCreatorFundPeriodForAll(ctx context.Context, a AdminActor, period SettlementPeriod) (PeriodBatchResult, error) {
	var res PeriodBatchResult
	err := s.adminRun(ctx, a, postgres.AuditTableSettlements, postgres.AuditOpSettleStart, postgres.AuditOpSettleFinish,
		map[string]any{"period_key": period.Key}, func() (any, error) {
			var err error
			res, err = s.SettleCreatorFundPeriodForAll(ctx, period, nil)
			return res, err
		})
	return res, err
}

// AdminSettleCreatorFundPeriod settles one creator's period.
func (s *Service) AdminSettleCreatorFundPeriod(ctx context.Context, a AdminActor, creatorID uuid.UUID, period SettlementPeriod) (*PeriodStatement, error) {
	var st *PeriodStatement
	err := s.adminRun(ctx, a, postgres.AuditTableSettlements, postgres.AuditOpSettleCreatorStart, postgres.AuditOpSettleCreatorFinish,
		map[string]any{"period_key": period.Key, "creator_id": creatorID}, func() (any, error) {
			var err error
			st, err = s.SettleCreatorFundPeriod(ctx, creatorID, period)
			return st, err
		})
	return st, err
}

// AdminAccrueCreatorFundDay re-measures one day (accrual only).
func (s *Service) AdminAccrueCreatorFundDay(ctx context.Context, a AdminActor, day time.Time) (BatchAccrual, error) {
	var batch BatchAccrual
	err := s.adminRun(ctx, a, postgres.AuditTableEarnings, postgres.AuditOpAccrueDayStart, postgres.AuditOpAccrueDayFinish,
		map[string]any{"day": day.Format("2006-01-02")}, func() (any, error) {
			var err error
			batch, err = s.AccrueCreatorFundDayForAllEligible(ctx, day, nil)
			return batch, err
		})
	return batch, err
}

// ---------------------------------------------------------------------------
// Admin reads
// ---------------------------------------------------------------------------

// AdminStats is the Money dashboard's counts for the current settlement period.
func (s *Service) AdminStats(ctx context.Context) (*postgres.AdminStats, error) {
	now := time.Now().UTC()
	period := PeriodContaining(now, s.creatorFundCfg.SettlementCadence)
	return s.store.AdminStats(ctx, now, period.Key, period.Start, period.End)
}

// ListAuditLog reads monetization_audit_log, newest first.
func (s *Service) ListAuditLog(ctx context.Context, f postgres.AuditLogFilter) ([]postgres.AuditLogEntry, error) {
	return s.store.ListAuditLog(ctx, f)
}

// ListPayoutRequests reads payout requests (read-only; payouts stay off).
func (s *Service) ListPayoutRequests(ctx context.Context, status string, limit, offset int) ([]postgres.PayoutRequestRow, error) {
	return s.store.ListPayoutRequests(ctx, status, limit, offset)
}
