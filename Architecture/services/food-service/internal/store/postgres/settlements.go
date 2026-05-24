package postgres

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// SettlementFile mirrors food.settlement_files (sans body for list).
type SettlementFile struct {
	ID          uuid.UUID `json:"id"`
	PeriodStart string    `json:"period_start"`
	PeriodEnd   string    `json:"period_end"`
	Kind        string    `json:"kind"`
	RowCount    int       `json:"row_count"`
	TotalAmount float64   `json:"total_amount"`
	GeneratedBy *string   `json:"generated_by,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
}

// GenerateRestaurantSettlementFile pulls every restaurant_settlements
// row whose period overlaps [from, to] (DATE-inclusive both ends),
// writes a CSV body, and inserts an audit row. Returns the new row.
//
// The output schema is what an accounting tool expects:
//   restaurant_id, restaurant_name, period_start, period_end,
//   gross_order_amount, commission_amount, refund_adjustment,
//   penalty_amount, payout_amount, status, paid_reference, paid_at
func (s *Store) GenerateRestaurantSettlementFile(ctx context.Context, adminID uuid.UUID, from, to time.Time) (*SettlementFile, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			rs.restaurant_id, r.name,
			rs.period_start, rs.period_end,
			rs.gross_order_amount::float8, rs.commission_amount::float8,
			rs.refund_adjustment::float8, rs.penalty_amount::float8,
			rs.payout_amount::float8, rs.status::text,
			COALESCE(rs.paid_reference, ''), COALESCE(rs.paid_at::text, '')
		FROM food.restaurant_settlements rs
		JOIN food.restaurants r ON r.id = rs.restaurant_id
		WHERE rs.period_start <= $2::date AND rs.period_end >= $1::date
		ORDER BY rs.period_start, r.name
	`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query restaurant settlements: %w", err)
	}
	defer rows.Close()

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{
		"restaurant_id", "restaurant_name", "period_start", "period_end",
		"gross_order_amount", "commission_amount", "refund_adjustment",
		"penalty_amount", "payout_amount", "status", "paid_reference", "paid_at",
	})
	rowCount := 0
	totalPayout := 0.0
	for rows.Next() {
		var (
			rid                            uuid.UUID
			name, status, paidRef, paidAt  string
			ps, pe                         time.Time
			gross, comm, refund, pen, pout float64
		)
		if err := rows.Scan(&rid, &name, &ps, &pe, &gross, &comm, &refund, &pen, &pout, &status, &paidRef, &paidAt); err != nil {
			return nil, err
		}
		_ = w.Write([]string{
			rid.String(), name,
			ps.Format("2006-01-02"), pe.Format("2006-01-02"),
			strconv.FormatFloat(gross, 'f', 2, 64),
			strconv.FormatFloat(comm, 'f', 2, 64),
			strconv.FormatFloat(refund, 'f', 2, 64),
			strconv.FormatFloat(pen, 'f', 2, 64),
			strconv.FormatFloat(pout, 'f', 2, 64),
			status, paidRef, paidAt,
		})
		rowCount++
		totalPayout += pout
	}
	w.Flush()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return s.insertSettlementFile(ctx, adminID, "restaurant", from, to, buf.Bytes(), rowCount, totalPayout)
}

// GenerateDeliverySettlementFile is the symmetric version for
// delivery_partner_settlements.
func (s *Store) GenerateDeliverySettlementFile(ctx context.Context, adminID uuid.UUID, from, to time.Time) (*SettlementFile, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			dps.delivery_partner_id, dp.full_name,
			dps.period_start, dps.period_end,
			dps.delivery_count,
			dps.gross_earning_amount::float8, dps.incentive_amount::float8,
			dps.penalty_amount::float8, dps.payout_amount::float8,
			dps.status::text,
			COALESCE(dps.paid_reference, ''), COALESCE(dps.paid_at::text, '')
		FROM food.delivery_partner_settlements dps
		JOIN food.delivery_partners dp ON dp.id = dps.delivery_partner_id
		WHERE dps.period_start <= $2::date AND dps.period_end >= $1::date
		ORDER BY dps.period_start, dp.full_name
	`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query delivery settlements: %w", err)
	}
	defer rows.Close()

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{
		"delivery_partner_id", "partner_name", "period_start", "period_end",
		"delivery_count", "gross_earning_amount", "incentive_amount",
		"penalty_amount", "payout_amount", "status", "paid_reference", "paid_at",
	})
	rowCount := 0
	totalPayout := 0.0
	for rows.Next() {
		var (
			pid                                  uuid.UUID
			name, status, paidRef, paidAt        string
			ps, pe                               time.Time
			deliveryCount                        int
			gross, incentive, penalty, payout    float64
		)
		if err := rows.Scan(&pid, &name, &ps, &pe, &deliveryCount, &gross, &incentive, &penalty, &payout, &status, &paidRef, &paidAt); err != nil {
			return nil, err
		}
		_ = w.Write([]string{
			pid.String(), name,
			ps.Format("2006-01-02"), pe.Format("2006-01-02"),
			strconv.Itoa(deliveryCount),
			strconv.FormatFloat(gross, 'f', 2, 64),
			strconv.FormatFloat(incentive, 'f', 2, 64),
			strconv.FormatFloat(penalty, 'f', 2, 64),
			strconv.FormatFloat(payout, 'f', 2, 64),
			status, paidRef, paidAt,
		})
		rowCount++
		totalPayout += payout
	}
	w.Flush()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return s.insertSettlementFile(ctx, adminID, "delivery", from, to, buf.Bytes(), rowCount, totalPayout)
}

func (s *Store) insertSettlementFile(ctx context.Context, adminID uuid.UUID, kind string, from, to time.Time, body []byte, rowCount int, totalAmount float64) (*SettlementFile, error) {
	var f SettlementFile
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.settlement_files
			(period_start, period_end, kind, file_url, body, row_count, total_amount, generated_by)
		VALUES ($1::date, $2::date, $3, '', $4, $5, $6, $7)
		RETURNING id, period_start::text, period_end::text, kind, row_count, total_amount::float8, generated_by, generated_at
	`, from, to, kind, body, rowCount, totalAmount, adminID).Scan(
		&f.ID, &f.PeriodStart, &f.PeriodEnd, &f.Kind, &f.RowCount, &f.TotalAmount, &f.GeneratedBy, &f.GeneratedAt,
	); err != nil {
		return nil, fmt.Errorf("insert settlement file: %w", err)
	}
	return &f, nil
}

// ListSettlementFiles returns the audit log of generated files,
// newest first.
func (s *Store) ListSettlementFiles(ctx context.Context, limit int) ([]SettlementFile, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, period_start::text, period_end::text, kind,
			row_count, total_amount::float8, generated_by, generated_at
		FROM food.settlement_files
		ORDER BY generated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list settlement files: %w", err)
	}
	defer rows.Close()
	var out []SettlementFile
	for rows.Next() {
		var f SettlementFile
		if err := rows.Scan(&f.ID, &f.PeriodStart, &f.PeriodEnd, &f.Kind,
			&f.RowCount, &f.TotalAmount, &f.GeneratedBy, &f.GeneratedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetSettlementFileBody streams the CSV body back to the admin
// download endpoint.
func (s *Store) GetSettlementFileBody(ctx context.Context, fileID uuid.UUID) ([]byte, string, error) {
	var body []byte
	var kind string
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(body, ''::bytea), kind
		FROM food.settlement_files WHERE id = $1
	`, fileID).Scan(&body, &kind); err != nil {
		return nil, "", err
	}
	return body, kind, nil
}
