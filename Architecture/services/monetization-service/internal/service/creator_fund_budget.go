package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Budget cap (plan Phase 2C)
// ---------------------------------------------------------------------------
//
// The fund has a cap per settlement period and region, set by an admin.
// Accrual halts for the rest of the period once the cap is reached — a
// PROSPECTIVE stop. Nothing already earned is reduced, which is why a cap
// can never be lowered below what has accrued against it.
//
// The cap amount is a business decision the plan cannot take; the
// mechanism refuses nothing while no row exists, but logs once per period
// that it is accruing uncapped.

// ErrBudgetBelowAccrued is re-exported from the store: the refusal is
// enforced under the same row lock the accruals take.
var ErrBudgetBelowAccrued = postgres.ErrBudgetBelowAccrued

// ErrCadenceMismatch is returned when a period key is of a different
// cadence than the service is configured for. A cadence change mid-period
// would rename the period every accrual was counted against, so it is
// refused while any period holds accrued money.
var ErrCadenceMismatch = errors.New("CADENCE_MISMATCH")

// BudgetInput is the admin's request.
type BudgetInput struct {
	PeriodKey  string `json:"period_key"`
	RegionCode string `json:"region_code"`
	CapPaise   int64  `json:"cap_paise"`
	Notes      string `json:"notes"`
}

// UpsertCreatorFundBudget creates or changes a period's cap.
func (s *Service) UpsertCreatorFundBudget(ctx context.Context, in BudgetInput, adminID *uuid.UUID) (*postgres.CreatorFundBudget, error) {
	period, err := ParsePeriodKey(in.PeriodKey)
	if err != nil {
		return nil, err
	}
	configured := NormalizeCadence(s.creatorFundCfg.SettlementCadence)
	if period.Cadence != configured {
		return nil, fmt.Errorf("%w: period %s is %s but the service settles %s", ErrCadenceMismatch, period.Key, period.Cadence, configured)
	}
	if in.CapPaise < 0 {
		return nil, fmt.Errorf("INVALID_BUDGET: cap_paise must be >= 0")
	}
	region := strings.TrimSpace(in.RegionCode)
	if region == "" {
		region = defaultRegionCode
	}
	return s.store.UpsertCreatorFundBudget(ctx, &postgres.CreatorFundBudget{
		PeriodKey:  period.Key,
		RegionCode: region,
		CapPaise:   in.CapPaise,
		Notes:      in.Notes,
		CreatedBy:  adminID,
	})
}

// ListCreatorFundBudgets returns every budget row, newest period first.
func (s *Service) ListCreatorFundBudgets(ctx context.Context) ([]postgres.CreatorFundBudget, error) {
	return s.store.ListCreatorFundBudgets(ctx)
}

// CheckSettlementCadence refuses to run under a cadence that does not
// match a period that already holds accrued money. main.go calls it at
// boot and fails closed, because switching CF_SETTLEMENT_CADENCE with
// money in 2026-09 would have the next accrual look for 2026-09-H2.
func (s *Service) CheckSettlementCadence(ctx context.Context) error {
	configured := NormalizeCadence(s.creatorFundCfg.SettlementCadence)
	keys, err := s.store.ListBudgetPeriodKeysWithAccruals(ctx)
	if err != nil {
		return fmt.Errorf("list budgets: %w", err)
	}
	var mismatched []string
	for _, k := range keys {
		p, err := ParsePeriodKey(k)
		if err != nil {
			mismatched = append(mismatched, k+" (unparseable)")
			continue
		}
		if p.Cadence != configured {
			mismatched = append(mismatched, k)
		}
	}
	if len(mismatched) > 0 {
		return fmt.Errorf("%w: configured cadence is %s but these periods hold accrued fund money under another cadence: %s",
			ErrCadenceMismatch, configured, strings.Join(mismatched, ", "))
	}
	return nil
}
