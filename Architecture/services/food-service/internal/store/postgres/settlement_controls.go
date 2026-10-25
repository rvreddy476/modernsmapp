package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/settlement"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// All Food settlement writes take the same transaction lock. Generation and
// manual reconciliation therefore cannot race across periods or admin devices.
func lockSettlementsTx(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(78462139012)`)
	return err
}

func enqueueSettlementGeneratedTx(ctx context.Context, tx pgx.Tx, kind string, owner uuid.UUID, id string, payout int64, start, end string) error {
	var user uuid.UUID
	query := `SELECT owner_user_id FROM food.restaurants WHERE id=$1`
	if kind == "delivery" {
		query = `SELECT user_id FROM food.delivery_partners WHERE id=$1`
	}
	if err := tx.QueryRow(ctx, query, owner).Scan(&user); err != nil {
		return err
	}
	payload := map[string]any{"settlement_id": id, "status": "PENDING", "payout_paise": payout, "period_start": start, "period_end": end, "occurred_at": time.Now().UTC()}
	typ, topic := "food.restaurant.settlement_generated", "food:restaurant:"+owner.String()
	if kind == "restaurant" {
		payload["restaurant_id"], payload["restaurant_owner_user_id"] = owner, user
	} else {
		typ, topic = "food.delivery_partner.settlement_generated", "food:delivery:"+owner.String()
		payload["partner_id"], payload["partner_user_id"] = owner, user
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return foodOutbox.Enqueue(ctx, tx, typ, topic, body)
}

func restaurantFinanceSummary(id uuid.UUID, orders, delivered, refunded int, line settlement.Line, paid, pending int64) map[string]any {
	return map[string]any{
		"restaurant_id": id, "orders": orders, "delivered": delivered, "refunded": refunded,
		"period": "ALL_TIME", "currency": "INR", "timezone": "Asia/Kolkata", "payout_basis": "DELIVERED_SUPPLY_ESTIMATE", "payment_method": "MANUAL_RECONCILED",
		"gross_amount": rupees(line.NetSupplyPaise), "gross_amount_paise": line.NetSupplyPaise,
		"commission": rupees(line.CommissionPaise), "commission_paise": line.CommissionPaise,
		"refunds": rupees(line.RefundSharePaise), "refunds_paise": line.RefundSharePaise,
		"payout_amount": rupees(line.PayoutPaise), "payout_amount_paise": line.PayoutPaise,
		"commission_gst_paise": line.CommissionGSTPaise, "gst_passthrough_paise": line.GSTPassthroughPaise, "tcs_paise": line.TCSPaise,
		"paid_paise": paid, "pending_settlement_paise": pending, "unpaid_estimate_paise": line.PayoutPaise - paid,
		"needs_adviser_confirmation": line.NeedsAdviserConfirmation,
	}
}

func guardSettlementPeriodTx(ctx context.Context, tx pgx.Tx, kind string, owner uuid.UUID, start, end time.Time) error {
	table, column := "restaurant_settlements", "restaurant_id"
	if kind == "delivery" {
		table, column = "delivery_partner_settlements", "delivery_partner_id"
	}
	var conflict bool
	err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT EXISTS (
	 SELECT 1 FROM food.%s WHERE %s=$1
	 AND NOT (period_start=$2::date AND period_end=$3::date)
	 AND (status <> 'PAID' OR (period_start <= $3::date AND period_end >= $2::date)))`, table, column), owner, start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return fmt.Errorf("settlement period overlaps an existing period or another period is awaiting reconciliation")
	}
	return nil
}

// Paid rows never change. Compare their frozen value with their current
// economic value under their ORIGINAL rates. The net difference is carried to
// the next open period exactly once: already-paid carry-forwards cancel out in
// this sum. One open period per beneficiary is enforced above.
func (s *Store) restaurantCarryForwardTx(ctx context.Context, tx pgx.Tx, restaurantID uuid.UUID) (int64, error) {
	rows, err := tx.Query(ctx, `SELECT period_start, period_end, breakdown,
	 COALESCE(payout_paise, ROUND(payout_amount * 100)::bigint)
	 FROM food.restaurant_settlements WHERE restaurant_id=$1 AND status='PAID'`, restaurantID)
	if err != nil {
		return 0, err
	}
	type paid struct {
		start, end time.Time
		line       settlement.Line
		payout     int64
	}
	var periods []paid
	for rows.Next() {
		var p paid
		var raw []byte
		if err := rows.Scan(&p.start, &p.end, &raw, &p.payout); err != nil {
			rows.Close()
			return 0, err
		}
		if len(raw) == 0 {
			rows.Close()
			return 0, fmt.Errorf("legacy paid settlement requires finance reconciliation before another payout")
		}
		if err := json.Unmarshal(raw, &p.line); err != nil {
			rows.Close()
			return 0, err
		}
		periods = append(periods, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var adjustment int64
	for _, p := range periods {
		timezone := p.line.PeriodTimezone
		if timezone == "" {
			timezone = "UTC"
		}
		groups, err := loadSettlementOrdersWindowTx(ctx, tx, p.start, p.end, &restaurantID, timezone, true)
		if err != nil {
			return 0, err
		}
		var orders []settlement.Order
		for _, g := range groups {
			orders = append(orders, g.orders...)
		}
		current := settlement.ComputeRestaurant(orders, p.line.Rules)
		adjustment += p.payout - current.PayoutPaise
	}
	return adjustment, nil
}

// This records an externally reconciled transfer; it does not initiate one.
// Missing/failed transfers remain PENDING. Retrying the same confirmed reference
// is a read-only replay; conflicting references cannot overwrite history.
func (s *Store) markSettlementPaid(ctx context.Context, adminID, id uuid.UUID, reference, kind string) (map[string]any, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > 200 {
		return nil, fmt.Errorf("a confirmed external payment reference (1-200 characters) is required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if err = lockSettlementsTx(ctx, tx); err != nil {
		return nil, err
	}
	table, column, actorColumn, owners := "restaurant_settlements", "restaurant_id", "owner_user_id", "restaurants"
	if kind == "delivery" {
		table, column, actorColumn, owners = "delivery_partner_settlements", "delivery_partner_id", "user_id", "delivery_partners"
	}
	var status, oldRef string
	var owner, user uuid.UUID
	var payout int64
	var start, end time.Time
	var raw []byte
	query := fmt.Sprintf(`SELECT s.status::text, COALESCE(s.paid_reference,''), s.%s,
	 o.%s, COALESCE(s.payout_paise,ROUND(s.payout_amount*100)::bigint),s.period_start,s.period_end,
	 %s FROM food.%s s JOIN food.%s o ON o.id=s.%s WHERE s.id=$1 FOR UPDATE OF s`, column, actorColumn, "NULL::jsonb", table, owners, column)
	if kind == "restaurant" {
		query = strings.Replace(query, "NULL::jsonb", "s.breakdown", 1)
	}
	if err = tx.QueryRow(ctx, query, id).Scan(&status, &oldRef, &owner, &user, &payout, &start, &end, &raw); err != nil {
		return nil, err
	}
	result := map[string]any{"id": id, "status": "PAID", "paid_reference": reference, "payout_paise": payout, "payment_method": "MANUAL_RECONCILED"}
	if status == "PAID" {
		if oldRef != reference {
			return nil, fmt.Errorf("settlement is already paid with a different reference")
		}
		return result, nil
	}
	if status != "PENDING" && status != "FAILED" {
		return nil, fmt.Errorf("settlement must be pending or failed before recording payment")
	}
	if payout <= 0 {
		return nil, fmt.Errorf("non-positive settlement is a balance adjustment, not a payable transfer")
	}
	var duplicate bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.restaurant_settlements WHERE status='PAID' AND paid_reference=$1 UNION ALL SELECT 1 FROM food.delivery_partner_settlements WHERE status='PAID' AND paid_reference=$1)`, reference).Scan(&duplicate); err != nil {
		return nil, err
	}
	if duplicate {
		return nil, fmt.Errorf("external payment reference is already reconciled to another settlement")
	}
	if kind == "restaurant" {
		if len(raw) == 0 {
			return nil, fmt.Errorf("regenerate the legacy settlement before recording payment")
		}
		var stored settlement.Line
		if err = json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		timezone := stored.PeriodTimezone
		if timezone == "" {
			timezone = "UTC"
		}
		groups, err := loadSettlementOrdersWindowTx(ctx, tx, start, end, &owner, timezone, true)
		if err != nil {
			return nil, err
		}
		var orders []settlement.Order
		for _, g := range groups {
			orders = append(orders, g.orders...)
		}
		current := settlement.ComputeRestaurant(orders, stored.Rules)
		adjustment, err := s.restaurantCarryForwardTx(ctx, tx, owner)
		if err != nil {
			return nil, err
		}
		if current.PayoutPaise-adjustment != payout {
			return nil, fmt.Errorf("settlement changed after generation; regenerate and reconcile the updated amount")
		}
	} else {
		var current int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(ROUND(da.delivery_partner_payout*100)),0)::bigint FROM food.delivery_assignments da
		 WHERE da.delivery_partner_id=$1 AND da.status='DELIVERED'
		 AND (da.delivered_at AT TIME ZONE (SELECT period_timezone FROM food.delivery_partner_settlements WHERE id=$4))::date BETWEEN $2::date AND $3::date
		 AND NOT EXISTS (SELECT 1 FROM food.delivery_partner_settlements paid WHERE paid.delivery_partner_id=da.delivery_partner_id AND paid.status='PAID' AND paid.id<>$4
		 AND (da.delivered_at AT TIME ZONE paid.period_timezone)::date BETWEEN paid.period_start AND paid.period_end)`, owner, start, end, id).Scan(&current); err != nil {
			return nil, err
		}
		if current != payout {
			return nil, fmt.Errorf("settlement changed after generation; regenerate and reconcile the updated amount")
		}
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`UPDATE food.%s SET status='PAID',paid_reference=$2,paid_at=NOW(),created_by=COALESCE(created_by,$3) WHERE id=$1`, table), id, reference, adminID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO food.admin_audit_logs(actor_user_id,action,entity_type,entity_id,new_value) VALUES($1,$2,$3,$4,jsonb_build_object('status','PAID','reference',$5::text,'payout_paise',$6::bigint,'method','MANUAL_RECONCILED'))`, adminID, kind+"_settlement.mark_paid", kind+"_settlement", id, reference, payout); err != nil {
		return nil, err
	}
	payload := map[string]any{"settlement_id": id, "status": "PAID", "payout_paise": payout, "period_start": start.Format("2006-01-02"), "period_end": end.Format("2006-01-02"), "occurred_at": time.Now().UTC()}
	typ, topic := "food.restaurant.settlement_paid", "food:restaurant:"+owner.String()
	if kind == "restaurant" {
		payload["restaurant_id"], payload["restaurant_owner_user_id"] = owner, user
	} else {
		typ, topic = "food.delivery_partner.settlement_paid", "food:delivery:"+owner.String()
		payload["partner_id"], payload["partner_user_id"] = owner, user
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err = foodOutbox.Enqueue(ctx, tx, typ, topic, body); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
