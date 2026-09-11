package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Accrual under one transaction: budget, then carry, then earnings
// ---------------------------------------------------------------------------
//
// AccrueDayTx is the single place a fund accrual row is written from. It
// takes its locks in a fixed order — the period's budget row, then the
// creator's carry row, then the earnings insert — so two accruals can
// never wait on each other in a cycle, and so the budget's remaining
// amount is read under the same lock that later raises accrued_paise.
// The order is traced (SetAccrualLockTraceForTest) so a test can assert
// it rather than trust a comment.

const (
	LockStepBudget   = "budget"
	LockStepCarry    = "carry"
	LockStepEarnings = "earnings"
)

var accrualLockTrace func(step string)

// SetAccrualLockTraceForTest installs a hook that is called with each lock
// step as AccrueDayTx takes it. Test-only; nil disables it.
func SetAccrualLockTraceForTest(fn func(step string)) { accrualLockTrace = fn }

func traceLock(step string) {
	if accrualLockTrace != nil {
		accrualLockTrace(step)
	}
}

// ErrBudgetBelowAccrued is returned when a cap would be set below what has
// already accrued against it. Nothing already earned is reduced.
var ErrBudgetBelowAccrued = errors.New("BUDGET_BELOW_ACCRUED: cap_paise cannot be lowered below accrued_paise")

// CreatorFundBudget is one period's cap and how much of it is used.
type CreatorFundBudget struct {
	PeriodKey      string     `json:"period_key"`
	RegionCode     string     `json:"region_code"`
	CapPaise       int64      `json:"cap_paise"`
	AccruedPaise   int64      `json:"accrued_paise"`
	ExhaustedAt    *time.Time `json:"exhausted_at,omitempty"`
	ExhaustedOnDay *time.Time `json:"exhausted_on_day,omitempty"`
	Notes          string     `json:"notes,omitempty"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// RemainingPaise is what can still accrue under the cap: zero once the
// period is marked exhausted, whatever the arithmetic says.
func (b *CreatorFundBudget) RemainingPaise() int64 {
	if b == nil {
		return 0
	}
	if b.ExhaustedAt != nil {
		return 0
	}
	if r := b.CapPaise - b.AccruedPaise; r > 0 {
		return r
	}
	return 0
}

// PricedDay is what the caller's pricing function returns for one
// (creator, day, content_type): the money after the carry boundary and,
// when a budget applies, after the cap.
type PricedDay struct {
	GrossPaise         int64
	PlatformFeePaise   int64
	NetPaise           int64
	CarryOutMicroPaise int64
	// SkipReason is empty for a full accrual, 'budget_capped' when the day
	// took exactly what remained under the cap, 'budget_exhausted' for a
	// zero row.
	SkipReason string
}

// AccrueDayInput carries the pre-priced row and the pricing function.
// Earning must arrive with identity, views, rate, band and multiplier
// filled; the money, carry and skip fields are set here from what Price
// returns under the locks.
type AccrueDayInput struct {
	Earning   *CreatorFundEarning
	PeriodKey string
	// Price computes the day's money given the carry-in and the paise still
	// available under the period's cap (nil when the period is uncapped).
	Price func(carryInMicroPaise int64, budgetRemainingPaise *int64) PricedDay
}

// AccrueDayOutcome says what AccrueDayTx did.
type AccrueDayOutcome struct {
	// Inserted is true when a new row was written.
	Inserted bool
	// Existing is the row already occupying the (creator, day, type, region)
	// slot when Inserted is false — settled or reversed.
	Existing *CreatorFundEarning
	// Budget is the period's budget row as it stood before this accrual,
	// nil when the period is uncapped.
	Budget *CreatorFundBudget
}

// AccrueDayTx writes one accrual row inside tx. See the file comment for
// the lock order and why it is fixed.
func (s *Store) AccrueDayTx(ctx context.Context, tx pgx.Tx, in AccrueDayInput) (AccrueDayOutcome, error) {
	var out AccrueDayOutcome
	e := in.Earning
	if e == nil || in.Price == nil {
		return out, errors.New("accrue day: earning and price function are required")
	}
	if e.RegionCode == "" {
		e.RegionCode = "IN"
	}

	// 1. Budget. FOR UPDATE serialises every accrual in the period, which
	//    is what makes "exactly the remaining amount" and "who hits the cap
	//    first" deterministic.
	traceLock(LockStepBudget)
	budget, err := lockBudget(ctx, tx, in.PeriodKey, e.RegionCode)
	if err != nil {
		return out, fmt.Errorf("lock budget: %w", err)
	}
	out.Budget = budget

	// 2. Carry. Upserted so the first day for a (creator, type, region)
	//    has a row to lock; then locked.
	traceLock(LockStepCarry)
	if _, err := tx.Exec(ctx, `
		INSERT INTO creator_fund_carry (creator_id, content_type, region_code, carry_micro_paise)
		VALUES ($1, $2, $3, 0)
		ON CONFLICT (creator_id, content_type, region_code) DO NOTHING
	`, e.CreatorID, e.ContentType, e.RegionCode); err != nil {
		return out, fmt.Errorf("ensure carry row: %w", err)
	}
	var carryIn int64
	if err := tx.QueryRow(ctx, `
		SELECT carry_micro_paise FROM creator_fund_carry
		WHERE creator_id = $1 AND content_type = $2 AND region_code = $3
		FOR UPDATE
	`, e.CreatorID, e.ContentType, e.RegionCode).Scan(&carryIn); err != nil {
		return out, fmt.Errorf("lock carry row: %w", err)
	}

	// 3. Earnings. The slot check runs under the carry lock, so it is
	//    authoritative for this (creator, type, region); the insert below
	//    is still ON CONFLICT DO NOTHING as belt and braces.
	traceLock(LockStepEarnings)
	existing, err := findCreatorFundEarningSlot(ctx, tx, e.CreatorID, e.DayBucket, e.ContentType, e.RegionCode)
	if err != nil {
		return out, fmt.Errorf("slot check: %w", err)
	}
	if existing != nil {
		out.Existing = existing
		return out, nil
	}

	var remaining *int64
	if budget != nil {
		r := budget.RemainingPaise()
		remaining = &r
	}
	priced := in.Price(carryIn, remaining)
	e.GrossPaise = priced.GrossPaise
	e.PlatformFeePaise = priced.PlatformFeePaise
	e.NetPaise = priced.NetPaise
	e.CarryInMicroPaise = carryIn
	e.CarryOutMicroPaise = priced.CarryOutMicroPaise
	e.SkipReason = priced.SkipReason
	if e.Status == "" {
		e.Status = "settled"
	}

	inserted, err := insertCreatorFundEarning(ctx, tx, e)
	if err != nil {
		return out, fmt.Errorf("insert earning: %w", err)
	}
	if !inserted {
		existing, err := findCreatorFundEarningSlot(ctx, tx, e.CreatorID, e.DayBucket, e.ContentType, e.RegionCode)
		if err != nil {
			return out, err
		}
		out.Existing = existing
		return out, nil
	}
	out.Inserted = true

	if _, err := tx.Exec(ctx, `
		UPDATE creator_fund_carry
		SET carry_micro_paise = $4, last_day_bucket = $5, updated_at = NOW()
		WHERE creator_id = $1 AND content_type = $2 AND region_code = $3
	`, e.CreatorID, e.ContentType, e.RegionCode, e.CarryOutMicroPaise, e.DayBucket); err != nil {
		return out, fmt.Errorf("update carry: %w", err)
	}

	if budget != nil && e.GrossPaise > 0 {
		// accrued_paise + gross reaching cap_paise marks the period
		// exhausted whether or not this row was itself capped: the next
		// day has nothing left either way. The table CHECK (accrued <=
		// cap) makes an overspend impossible to commit.
		if _, err := tx.Exec(ctx, `
			UPDATE creator_fund_budgets
			SET accrued_paise    = accrued_paise + $3,
			    exhausted_at     = CASE WHEN accrued_paise + $3 >= cap_paise THEN COALESCE(exhausted_at, NOW()) ELSE exhausted_at END,
			    exhausted_on_day = CASE WHEN accrued_paise + $3 >= cap_paise THEN COALESCE(exhausted_on_day, $4::date) ELSE exhausted_on_day END,
			    updated_at       = NOW()
			WHERE period_key = $1 AND region_code = $2
		`, in.PeriodKey, e.RegionCode, e.GrossPaise, e.DayBucket); err != nil {
			return out, fmt.Errorf("debit budget: %w", err)
		}
	}
	return out, nil
}

func lockBudget(ctx context.Context, tx pgx.Tx, periodKey, regionCode string) (*CreatorFundBudget, error) {
	row := tx.QueryRow(ctx, budgetSelect+` WHERE period_key = $1 AND region_code = $2 FOR UPDATE`, periodKey, regionCode)
	b, err := scanBudget(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// ---------------------------------------------------------------------------
// Carry
// ---------------------------------------------------------------------------

// CreatorFundCarry is the sub-paise remainder waiting to be paid.
type CreatorFundCarry struct {
	CreatorID       uuid.UUID  `json:"creator_id"`
	ContentType     string     `json:"content_type"`
	RegionCode      string     `json:"region_code"`
	CarryMicroPaise int64      `json:"carry_micro_paise"`
	LastDayBucket   *time.Time `json:"last_day_bucket,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GetCreatorFundCarry returns the carry row, or nil if the creator has
// never accrued that content type.
func (s *Store) GetCreatorFundCarry(ctx context.Context, creatorID uuid.UUID, contentType, regionCode string) (*CreatorFundCarry, error) {
	var c CreatorFundCarry
	err := s.db.QueryRow(ctx, `
		SELECT creator_id, content_type, region_code, carry_micro_paise, last_day_bucket, updated_at
		FROM creator_fund_carry
		WHERE creator_id = $1 AND content_type = $2 AND region_code = $3
	`, creatorID, contentType, regionCode).Scan(
		&c.CreatorID, &c.ContentType, &c.RegionCode, &c.CarryMicroPaise, &c.LastDayBucket, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// ---------------------------------------------------------------------------
// Budgets
// ---------------------------------------------------------------------------

const budgetSelect = `
	SELECT period_key, region_code, cap_paise, accrued_paise, exhausted_at, exhausted_on_day,
	       COALESCE(notes, ''), created_by, created_at, updated_at
	FROM creator_fund_budgets`

func scanBudget(r rowScanner) (*CreatorFundBudget, error) {
	var b CreatorFundBudget
	if err := r.Scan(&b.PeriodKey, &b.RegionCode, &b.CapPaise, &b.AccruedPaise, &b.ExhaustedAt,
		&b.ExhaustedOnDay, &b.Notes, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

// GetCreatorFundBudget returns the budget row for a period, or nil when
// the period is uncapped.
func (s *Store) GetCreatorFundBudget(ctx context.Context, periodKey, regionCode string) (*CreatorFundBudget, error) {
	row := s.db.QueryRow(ctx, budgetSelect+` WHERE period_key = $1 AND region_code = $2`, periodKey, regionCode)
	b, err := scanBudget(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// ListCreatorFundBudgets returns every budget row, newest period first.
func (s *Store) ListCreatorFundBudgets(ctx context.Context) ([]CreatorFundBudget, error) {
	rows, err := s.db.Query(ctx, budgetSelect+` ORDER BY period_key DESC, region_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CreatorFundBudget
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// ListBudgetPeriodKeysWithAccruals returns the keys of every period that
// has money accrued against its cap. A settlement cadence change would
// rename these periods, so it is refused while any exist.
func (s *Store) ListBudgetPeriodKeysWithAccruals(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT period_key FROM creator_fund_budgets WHERE accrued_paise > 0 ORDER BY period_key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// UpsertCreatorFundBudget creates or changes a period's cap under the
// row lock the accruals also take, so a cap change and an accrual cannot
// interleave.
//
//   - Lowering below accrued_paise is refused (ErrBudgetBelowAccrued):
//     nothing already earned is reduced.
//   - Raising clears the exhaustion only when accrued is below the new cap;
//     a cap set exactly to accrued stays exhausted.
func (s *Store) UpsertCreatorFundBudget(ctx context.Context, b *CreatorFundBudget) (*CreatorFundBudget, error) {
	if b.RegionCode == "" {
		b.RegionCode = "IN"
	}
	if b.CapPaise < 0 {
		return nil, errors.New("INVALID_BUDGET: cap_paise must be >= 0")
	}
	var stored *CreatorFundBudget
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		existing, err := lockBudget(ctx, tx, b.PeriodKey, b.RegionCode)
		if err != nil {
			return err
		}
		if existing == nil {
			row := tx.QueryRow(ctx, `
				INSERT INTO creator_fund_budgets (period_key, region_code, cap_paise, accrued_paise, notes, created_by)
				VALUES ($1, $2, $3, 0, $4, $5)
				RETURNING period_key, region_code, cap_paise, accrued_paise, exhausted_at, exhausted_on_day,
				          COALESCE(notes, ''), created_by, created_at, updated_at
			`, b.PeriodKey, b.RegionCode, b.CapPaise, nullableString(b.Notes), b.CreatedBy)
			stored, err = scanBudget(row)
			return err
		}
		if b.CapPaise < existing.AccruedPaise {
			return fmt.Errorf("%w: cap %d, accrued %d for %s/%s", ErrBudgetBelowAccrued,
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
		return err
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}
