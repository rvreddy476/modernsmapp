package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/settlement"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Settlement generation (Wave 1 B3). The arithmetic is internal/settlement;
// this file reads the period's orders and writes the rows. Status stays
// PENDING and nothing transfers money.

// RestaurantSettlementRecord is one stored restaurant_settlements row.
type RestaurantSettlementRecord struct {
	ID              uuid.UUID
	RestaurantID    uuid.UUID
	RestaurantName  string
	PeriodStart     string
	PeriodEnd       string
	Status          string
	PaidReference   string
	PaidAt          string
	CreatedAt       string
	GrossOrderPaise int64
	PenaltyPaise    int64
	Line            settlement.Line
	// HasBreakdown is false for a row generated before B3.
	HasBreakdown bool
}

// RestaurantSettlementRowMap is the admin settlement row. The NUMERIC-era keys
// are kept (derived from paise) and the paise keys and breakdown are added.
func RestaurantSettlementRowMap(r RestaurantSettlementRecord) map[string]any {
	l := r.Line
	row := map[string]any{
		"id": r.ID.String(), "restaurant_id": r.RestaurantID.String(), "restaurant_name": r.RestaurantName,
		"period_start": r.PeriodStart, "period_end": r.PeriodEnd, "status": r.Status,
		"paid_reference": r.PaidReference, "paid_at": r.PaidAt, "created_at": r.CreatedAt,
		"gross_order_amount": rupees(r.GrossOrderPaise), "commission_amount": rupees(l.CommissionPaise),
		"refund_adjustment": rupees(l.RefundSharePaise), "penalty_amount": rupees(r.PenaltyPaise),
		"payout_amount":     rupees(l.PayoutPaise),
		"gross_order_paise": r.GrossOrderPaise, "net_supply_paise": l.NetSupplyPaise, "commission_paise": l.CommissionPaise,
		"commission_gst_paise": l.CommissionGSTPaise, "gst_passthrough_paise": l.GSTPassthroughPaise, "tcs_paise": l.TCSPaise,
		"refund_share_paise": l.RefundSharePaise, "payout_paise": l.PayoutPaise,
		"needs_adviser_confirmation": r.HasBreakdown && l.NeedsAdviserConfirmation,
		"breakdown":                  nil,
	}
	if r.HasBreakdown {
		summary := l
		summary.Orders = nil
		row["breakdown"] = summary
	}
	return row
}

func (s *Store) AdminListRestaurantSettlements(ctx context.Context, page Pagination) ([]map[string]any, error) {
	page = normalizePagination(page)
	rows, err := s.db.Query(ctx, `
		SELECT rs.id, rs.restaurant_id, r.name, rs.period_start::text, rs.period_end::text,
			rs.status::text, COALESCE(rs.paid_reference, ''), COALESCE(rs.paid_at::text, ''), rs.created_at::text,
			COALESCE(rs.gross_order_paise, ROUND(rs.gross_order_amount * 100)::bigint),
			COALESCE(rs.commission_paise, ROUND(rs.commission_amount * 100)::bigint),
			COALESCE(rs.refund_share_paise, ROUND(rs.refund_adjustment * 100)::bigint),
			ROUND(rs.penalty_amount * 100)::bigint,
			COALESCE(rs.payout_paise, ROUND(rs.payout_amount * 100)::bigint),
			rs.breakdown
		FROM food.restaurant_settlements rs
		JOIN food.restaurants r ON r.id = rs.restaurant_id
		ORDER BY rs.created_at DESC
		LIMIT $1 OFFSET $2
	`, page.Limit, page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settlements := []map[string]any{}
	for rows.Next() {
		var rec RestaurantSettlementRecord
		var commission, refund, payout int64
		var raw []byte
		if err := rows.Scan(&rec.ID, &rec.RestaurantID, &rec.RestaurantName, &rec.PeriodStart, &rec.PeriodEnd,
			&rec.Status, &rec.PaidReference, &rec.PaidAt, &rec.CreatedAt, &rec.GrossOrderPaise,
			&commission, &refund, &rec.PenaltyPaise, &payout, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rec.Line); err != nil {
				return nil, fmt.Errorf("decode settlement breakdown: %w", err)
			}
			rec.HasBreakdown = true
		} else {
			rec.Line = settlement.Line{CommissionPaise: commission, RefundSharePaise: refund, PayoutPaise: payout}
		}
		settlements = append(settlements, RestaurantSettlementRowMap(rec))
	}
	return settlements, rows.Err()
}

type restaurantSettlementGroup struct {
	restaurantID uuid.UUID
	grossPaise   int64
	orders       []settlement.Order
}

// loadSettlementOrdersTx reads the period's orders per restaurant. An order
// counts once it was DELIVERED (delivered_at set), including one refunded
// after delivery; an order cancelled or rejected before delivery was never
// supplied and is not settled.
func loadSettlementOrdersTx(ctx context.Context, tx pgx.Tx, start, end time.Time, restaurantID *uuid.UUID) ([]restaurantSettlementGroup, error) {
	rows, err := tx.Query(ctx, `
		SELECT o.restaurant_id, o.id,
			COALESCE(o.item_subtotal_paise, ROUND(o.item_subtotal * 100)::bigint),
			COALESCE(o.addon_total_paise, ROUND(o.addon_total * 100)::bigint),
			COALESCE(o.packaging_fee_paise, ROUND(o.packaging_fee * 100)::bigint),
			COALESCE(o.discount_total_paise, ROUND((o.restaurant_discount + o.coupon_discount) * 100)::bigint),
			COALESCE(o.platform_fee_paise, ROUND(o.platform_fee * 100)::bigint),
			COALESCE(o.delivery_fee_paise, ROUND(o.delivery_fee * 100)::bigint),
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint),
			ROUND(o.commission_percentage_snapshot * 100)::bigint,
			COALESCE((
				SELECT SUM(ROUND(rf.amount * 100)::bigint) FROM food.refunds rf
				WHERE rf.order_id = o.id AND rf.status = 'PROCESSED'
			), 0)::bigint,
			o.tax_breakdown
		FROM food.orders o
		WHERE o.delivered_at IS NOT NULL
			AND o.status IN ('DELIVERED', 'REFUND_PENDING', 'REFUNDED')
			AND o.placed_at::date BETWEEN $1::date AND $2::date
			AND ($3::uuid IS NULL OR o.restaurant_id = $3)
		ORDER BY o.restaurant_id, o.placed_at, o.id
	`, start.Format("2006-01-02"), end.Format("2006-01-02"), restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []restaurantSettlementGroup
	for rows.Next() {
		var rid, oid uuid.UUID
		var o settlement.Order
		var raw []byte
		if err := rows.Scan(&rid, &oid, &o.ItemSubtotalPaise, &o.AddonTotalPaise, &o.PackagingFeePaise,
			&o.RestaurantDiscountPaise, &o.PlatformFeePaise, &o.DeliveryFeePaise, &o.FinalAmountPaise,
			&o.CommissionBP, &o.ProcessedRefundPaise, &raw); err != nil {
			return nil, err
		}
		o.OrderID = oid.String()
		if len(raw) > 0 {
			var b pricing.Breakdown
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, fmt.Errorf("decode tax breakdown of order %s: %w", oid, err)
			}
			o.Breakdown = &b
		}
		if len(groups) == 0 || groups[len(groups)-1].restaurantID != rid {
			groups = append(groups, restaurantSettlementGroup{restaurantID: rid})
		}
		g := &groups[len(groups)-1]
		g.orders = append(g.orders, o)
		g.grossPaise += o.FinalAmountPaise
	}
	return groups, rows.Err()
}

func (s *Store) generateRestaurantSettlementsTx(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, start, end time.Time, restaurantID *uuid.UUID) ([]map[string]any, error) {
	groups, err := loadSettlementOrdersTx(ctx, tx, start, end, restaurantID)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, g := range groups {
		line := settlement.ComputeRestaurant(g.orders, s.settlementRules)
		item, err := upsertRestaurantSettlementTx(ctx, tx, adminID, g.restaurantID, start, end, g.grossPaise, line)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func upsertRestaurantSettlementTx(ctx context.Context, tx pgx.Tx, adminID, restaurantID uuid.UUID, start, end time.Time, grossPaise int64, line settlement.Line) (map[string]any, error) {
	startDate := start.Format("2006-01-02")
	endDate := end.Format("2006-01-02")
	breakdown, err := json.Marshal(line)
	if err != nil {
		return nil, err
	}
	args := []any{restaurantID, startDate, endDate, grossPaise, line.CommissionPaise, line.RefundSharePaise, line.PayoutPaise,
		line.NetSupplyPaise, line.CommissionGSTPaise, line.GSTPassthroughPaise, line.TCSPaise, breakdown, adminID}
	var id, status string
	err = tx.QueryRow(ctx, `
		UPDATE food.restaurant_settlements
		SET gross_order_amount = ($4::bigint)::numeric / 100,
			commission_amount = ($5::bigint)::numeric / 100,
			refund_adjustment = ($6::bigint)::numeric / 100,
			payout_amount = ($7::bigint)::numeric / 100,
			gross_order_paise = $4::bigint,
			commission_paise = $5::bigint,
			refund_share_paise = $6::bigint,
			payout_paise = $7::bigint,
			net_supply_paise = $8::bigint,
			commission_gst_paise = $9::bigint,
			gst_passthrough_paise = $10::bigint,
			tcs_paise = $11::bigint,
			breakdown = $12::jsonb,
			created_by = COALESCE(created_by, $13)
		WHERE restaurant_id = $1
			AND period_start = $2::date
			AND period_end = $3::date
			AND status <> 'PAID'
		RETURNING id::text, status::text
	`, args...).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO food.restaurant_settlements (
				restaurant_id, period_start, period_end, gross_order_amount, commission_amount,
				refund_adjustment, payout_amount, gross_order_paise, commission_paise, refund_share_paise,
				payout_paise, net_supply_paise, commission_gst_paise, gst_passthrough_paise, tcs_paise,
				breakdown, created_by
			)
			SELECT $1, $2::date, $3::date, ($4::bigint)::numeric / 100, ($5::bigint)::numeric / 100,
				($6::bigint)::numeric / 100, ($7::bigint)::numeric / 100, $4::bigint, $5::bigint, $6::bigint,
				$7::bigint, $8::bigint, $9::bigint, $10::bigint, $11::bigint, $12::jsonb, $13
			WHERE NOT EXISTS (
				SELECT 1 FROM food.restaurant_settlements
				WHERE restaurant_id = $1 AND period_start = $2::date AND period_end = $3::date
			)
			RETURNING id::text, status::text
		`, args...).Scan(&id, &status)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Already PAID: left as it is.
		err = tx.QueryRow(ctx, `
			SELECT id::text, status::text
			FROM food.restaurant_settlements
			WHERE restaurant_id = $1 AND period_start = $2::date AND period_end = $3::date
			ORDER BY created_at DESC
			LIMIT 1
		`, restaurantID, startDate, endDate).Scan(&id, &status)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "restaurant_id": restaurantID.String(), "period_start": startDate, "period_end": endDate,
		"gross_amount": rupees(grossPaise), "commission": rupees(line.CommissionPaise),
		"refund_adjustment": rupees(line.RefundSharePaise), "payout_amount": rupees(line.PayoutPaise), "status": status,
		"gross_order_paise": grossPaise, "net_supply_paise": line.NetSupplyPaise, "commission_paise": line.CommissionPaise,
		"commission_gst_paise": line.CommissionGSTPaise, "gst_passthrough_paise": line.GSTPassthroughPaise,
		"tcs_paise": line.TCSPaise, "refund_share_paise": line.RefundSharePaise, "payout_paise": line.PayoutPaise,
		"needs_adviser_confirmation": line.NeedsAdviserConfirmation,
	}, nil
}

// generateDeliverySettlementsTx settles delivery partners: the sum of the
// stored rider payouts of their delivered assignments, in paise.
func (s *Store) generateDeliverySettlementsTx(ctx context.Context, tx pgx.Tx, adminID uuid.UUID, start, end time.Time, partnerID *uuid.UUID) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, `
		SELECT da.delivery_partner_id, ROUND(da.delivery_partner_payout * 100)::bigint
		FROM food.delivery_assignments da
		WHERE da.status = 'DELIVERED'
			AND da.delivery_partner_id IS NOT NULL
			AND da.delivered_at::date BETWEEN $1::date AND $2::date
			AND ($3::uuid IS NULL OR da.delivery_partner_id = $3)
		ORDER BY da.delivery_partner_id, da.delivered_at, da.id
	`, start.Format("2006-01-02"), end.Format("2006-01-02"), partnerID)
	if err != nil {
		return nil, err
	}
	type group struct {
		partnerID uuid.UUID
		payouts   []int64
	}
	var groups []group
	for rows.Next() {
		var pid uuid.UUID
		var payout int64
		if err := rows.Scan(&pid, &payout); err != nil {
			rows.Close()
			return nil, err
		}
		if len(groups) == 0 || groups[len(groups)-1].partnerID != pid {
			groups = append(groups, group{partnerID: pid})
		}
		groups[len(groups)-1].payouts = append(groups[len(groups)-1].payouts, payout)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, g := range groups {
		item, err := upsertDeliverySettlementTx(ctx, tx, adminID, g.partnerID, start, end, settlement.ComputeDeliveryPartner(g.payouts))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func upsertDeliverySettlementTx(ctx context.Context, tx pgx.Tx, adminID, partnerID uuid.UUID, start, end time.Time, d settlement.DeliveryPartner) (map[string]any, error) {
	startDate := start.Format("2006-01-02")
	endDate := end.Format("2006-01-02")
	var id, status string
	err := tx.QueryRow(ctx, `
		UPDATE food.delivery_partner_settlements
		SET delivery_count = $4::integer,
			gross_earning_amount = ($5::bigint)::numeric / 100,
			payout_amount = ($6::bigint)::numeric / 100,
			gross_earning_paise = $5::bigint,
			payout_paise = $6::bigint,
			created_by = COALESCE(created_by, $7)
		WHERE delivery_partner_id = $1
			AND period_start = $2::date
			AND period_end = $3::date
			AND status <> 'PAID'
		RETURNING id::text, status::text
	`, partnerID, startDate, endDate, d.DeliveryCount, d.GrossEarningPaise, d.PayoutPaise, adminID).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO food.delivery_partner_settlements (
				delivery_partner_id, period_start, period_end, delivery_count,
				gross_earning_amount, payout_amount, gross_earning_paise, payout_paise, created_by
			)
			SELECT $1, $2::date, $3::date, $4::integer, ($5::bigint)::numeric / 100, ($6::bigint)::numeric / 100,
				$5::bigint, $6::bigint, $7
			WHERE NOT EXISTS (
				SELECT 1 FROM food.delivery_partner_settlements
				WHERE delivery_partner_id = $1 AND period_start = $2::date AND period_end = $3::date
			)
			RETURNING id::text, status::text
		`, partnerID, startDate, endDate, d.DeliveryCount, d.GrossEarningPaise, d.PayoutPaise, adminID).Scan(&id, &status)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			SELECT id::text, status::text
			FROM food.delivery_partner_settlements
			WHERE delivery_partner_id = $1 AND period_start = $2::date AND period_end = $3::date
			ORDER BY created_at DESC
			LIMIT 1
		`, partnerID, startDate, endDate).Scan(&id, &status)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "delivery_partner_id": partnerID.String(), "period_start": startDate,
		"period_end": endDate, "delivery_count": d.DeliveryCount, "gross_amount": rupees(d.GrossEarningPaise),
		"incentive_amount": 0, "penalty_amount": 0, "payout_amount": rupees(d.PayoutPaise), "status": status,
		"gross_earning_paise": d.GrossEarningPaise, "payout_paise": d.PayoutPaise,
	}, nil
}
