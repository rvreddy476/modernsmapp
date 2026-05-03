package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UpsertMobilePlanInput is the inbound shape for a single Setu-sourced plan.
type UpsertMobilePlanInput struct {
	Operator        string
	Circle          string
	PlanAmountPaise int64
	ValidityDays    *int
	DataGBPerDay    *float64
	TalktimePaise   *int64
	SMSCountPerDay  *int
	Description     *string
	Category        *string
}

// UpsertMobilePlan inserts a single plan. The (operator, circle, amount,
// description) tuple is treated as the dedup key: same plan resync just
// bumps last_synced_at.
func (s *Store) UpsertMobilePlan(ctx context.Context, in UpsertMobilePlanInput) error {
	if in.Operator == "" || in.Circle == "" {
		return fmt.Errorf("upsert mobile plan: operator and circle required")
	}
	if in.PlanAmountPaise <= 0 {
		return fmt.Errorf("upsert mobile plan: amount must be positive")
	}
	const q = `
        INSERT INTO billpay.mobile_plans (
            operator, circle, plan_amount_paise, validity_days, data_gb_per_day,
            talktime_paise, sms_count_per_day, description, category, is_active
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true)`
	if _, err := s.db.Exec(ctx, q,
		in.Operator, in.Circle, in.PlanAmountPaise, in.ValidityDays, in.DataGBPerDay,
		in.TalktimePaise, in.SMSCountPerDay, in.Description, in.Category,
	); err != nil {
		return fmt.Errorf("insert mobile plan: %w", err)
	}
	return nil
}

// ReplaceMobilePlans deletes existing plans for (operator, circle) and
// inserts the new set. Atomic — used by the nightly sync to keep cached
// plans aligned with Setu without leaving stale rows.
func (s *Store) ReplaceMobilePlans(ctx context.Context, operator, circle string, plans []UpsertMobilePlanInput) error {
	if operator == "" || circle == "" {
		return fmt.Errorf("replace mobile plans: operator and circle required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin replace plans: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM billpay.mobile_plans WHERE operator = $1 AND circle = $2`,
		operator, circle,
	); err != nil {
		return fmt.Errorf("delete old plans: %w", err)
	}
	const ins = `
        INSERT INTO billpay.mobile_plans (
            operator, circle, plan_amount_paise, validity_days, data_gb_per_day,
            talktime_paise, sms_count_per_day, description, category, is_active
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true)`
	for _, p := range plans {
		if p.PlanAmountPaise <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, ins,
			operator, circle, p.PlanAmountPaise, p.ValidityDays, p.DataGBPerDay,
			p.TalktimePaise, p.SMSCountPerDay, p.Description, p.Category,
		); err != nil {
			return fmt.Errorf("insert plan: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replace plans: %w", err)
	}
	return nil
}

// ListMobilePlans returns active plans for an operator+circle, ordered by
// amount ascending.
func (s *Store) ListMobilePlans(ctx context.Context, operator, circle string) ([]MobilePlan, error) {
	if operator == "" || circle == "" {
		return nil, fmt.Errorf("list mobile plans: operator and circle required")
	}
	const q = `
        SELECT id, operator, circle, plan_amount_paise, validity_days, data_gb_per_day,
               talktime_paise, sms_count_per_day, description, category,
               is_active, last_synced_at
        FROM billpay.mobile_plans
        WHERE is_active = true AND operator = $1 AND circle = $2
        ORDER BY plan_amount_paise ASC`
	rows, err := s.db.Query(ctx, q, operator, circle)
	if err != nil {
		return nil, fmt.Errorf("list mobile plans: %w", err)
	}
	defer rows.Close()
	return scanMobilePlans(rows)
}

func scanMobilePlans(rows pgx.Rows) ([]MobilePlan, error) {
	var out []MobilePlan
	for rows.Next() {
		var p MobilePlan
		if err := rows.Scan(
			&p.ID, &p.Operator, &p.Circle, &p.PlanAmountPaise, &p.ValidityDays, &p.DataGBPerDay,
			&p.TalktimePaise, &p.SMSCountPerDay, &p.Description, &p.Category,
			&p.IsActive, &p.LastSyncedAt,
		); err != nil {
			return nil, fmt.Errorf("scan mobile plan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
