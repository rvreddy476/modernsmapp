// Seller money: COD remittances, pending payout summaries, delivered items
// for a seller and payout batch creation. Moved out of store.go unchanged.

package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CreateCODRemittance inserts a COD remittance row. Idempotent on
// shipment_id (the table has a UNIQUE constraint) — a second delivery
// webhook for the same shipment is dropped silently. Returns nil on
// successful insert OR on conflict, both of which are "fine".
func (s *Store) CreateCODRemittance(ctx context.Context, r *CODRemittance) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO cod_remittances (
			id, shipment_id, order_id, seller_id,
			gross_amount, commission_amount, platform_fee, tds_amount, net_amount,
			currency_code, status, delivered_at, created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (shipment_id) DO NOTHING`,
		r.ID, r.ShipmentID, r.OrderID, r.SellerID,
		r.GrossAmount, r.CommissionAmount, r.PlatformFee, r.TDSAmount, r.NetAmount,
		r.CurrencyCode, r.Status, r.DeliveredAt, r.CreatedAt,
	)
	return err
}

// ListCODRemittancesBySeller returns the seller's COD remittances newest
// first, optionally filtered by status. Used by the seller payouts UI.
func (s *Store) ListCODRemittancesBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*CODRemittance, int, error) {
	conds := []string{"seller_id = $1"}
	args := []any{sellerID}
	idx := 2
	if status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, status)
		idx++
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM cod_remittances "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx, `
		SELECT id, shipment_id, order_id, seller_id,
		       gross_amount, commission_amount, platform_fee, tds_amount, net_amount,
		       currency_code, status, delivered_at, settled_at, payout_batch_id, created_at
		FROM cod_remittances `+where+
		fmt.Sprintf(" ORDER BY delivered_at DESC LIMIT $%d OFFSET $%d", idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*CODRemittance
	for rows.Next() {
		r := &CODRemittance{}
		if err := rows.Scan(&r.ID, &r.ShipmentID, &r.OrderID, &r.SellerID,
			&r.GrossAmount, &r.CommissionAmount, &r.PlatformFee, &r.TDSAmount, &r.NetAmount,
			&r.CurrencyCode, &r.Status, &r.DeliveredAt, &r.SettledAt, &r.PayoutBatchID, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// PendingPayoutSummary groups outstanding (unsettled) COD remittances by
// seller so the admin reconciliation dashboard can show "how much do we owe
// each seller" in one query. Phase 4.5.
type PendingPayoutSummary struct {
	SellerID         uuid.UUID `db:"seller_id" json:"seller_id"`
	StoreName        string    `db:"store_name" json:"store_name"`
	Email            string    `db:"email" json:"email"`
	RemittanceCount  int       `db:"remittance_count" json:"remittance_count"`
	TotalGross       float64   `db:"total_gross" json:"total_gross"`
	TotalCommission  float64   `db:"total_commission" json:"total_commission"`
	TotalPlatformFee float64   `db:"total_platform_fee" json:"total_platform_fee"`
	TotalTDS         float64   `db:"total_tds" json:"total_tds"`
	TotalNet         float64   `db:"total_net" json:"total_net"`
	OldestDelivered  time.Time `db:"oldest_delivered" json:"oldest_delivered"`
}

// ListPendingPayoutsBySeller returns one row per seller with an outstanding
// COD remittance balance. Used by the admin reconciliation dashboard.
// Phase 4.5.
func (s *Store) ListPendingPayoutsBySeller(ctx context.Context, limit int) ([]*PendingPayoutSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			cr.seller_id,
			COALESCE(sl.store_name, '') AS store_name,
			COALESCE(sl.email, '') AS email,
			COUNT(*)::int AS remittance_count,
			COALESCE(SUM(cr.gross_amount), 0)      AS total_gross,
			COALESCE(SUM(cr.commission_amount), 0) AS total_commission,
			COALESCE(SUM(cr.platform_fee), 0)      AS total_platform_fee,
			COALESCE(SUM(cr.tds_amount), 0)        AS total_tds,
			COALESCE(SUM(cr.net_amount), 0)        AS total_net,
			MIN(cr.delivered_at)                   AS oldest_delivered
		FROM cod_remittances cr
		LEFT JOIN sellers sl ON sl.id = cr.seller_id
		WHERE cr.status = 'pending'
		GROUP BY cr.seller_id, sl.store_name, sl.email
		ORDER BY MIN(cr.delivered_at) ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PendingPayoutSummary
	for rows.Next() {
		var r PendingPayoutSummary
		if err := rows.Scan(&r.SellerID, &r.StoreName, &r.Email, &r.RemittanceCount,
			&r.TotalGross, &r.TotalCommission, &r.TotalPlatformFee, &r.TotalTDS, &r.TotalNet,
			&r.OldestDelivered); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// DeliveredItemRow joins a delivered order item with its order header so the
// caller can render or compute earnings without a second round trip.
// Phase 4.4.
type DeliveredItemRow struct {
	Item          *OrderItem
	OrderNumber   string
	PaymentMethod *string
}

// ListDeliveredItemsForSeller returns delivered order items for the seller,
// newest first. The order header carries payment_method so the service layer
// can split COD vs prepaid (COD has its own remittance ledger).
// Phase 4.4.
func (s *Store) ListDeliveredItemsForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*DeliveredItemRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT oi.id, oi.order_id, oi.product_id, oi.variant_id, oi.seller_id, oi.product_title,
		       oi.sku, oi.quantity, oi.unit_mrp, oi.unit_price, oi.discount_amount, oi.tax_amount,
		       oi.final_price, oi.status, oi.shipment_id, oi.tracking_number, oi.return_eligible_until,
		       oi.delivered_at, oi.created_at,
		       o.order_number, o.payment_method
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		WHERE oi.seller_id = $1
		  AND (oi.status = 'delivered' OR oi.delivered_at IS NOT NULL)
		ORDER BY oi.delivered_at DESC NULLS LAST
		LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DeliveredItemRow
	for rows.Next() {
		var it OrderItem
		var orderNumber string
		var paymentMethod *string
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.VariantID, &it.SellerID,
			&it.ProductTitle, &it.SKU, &it.Quantity, &it.UnitMRP, &it.UnitPrice, &it.DiscountAmount,
			&it.TaxAmount, &it.FinalPrice, &it.Status, &it.ShipmentID, &it.TrackingNumber,
			&it.ReturnEligibleUntil, &it.DeliveredAt, &it.CreatedAt,
			&orderNumber, &paymentMethod); err != nil {
			return nil, err
		}
		out = append(out, &DeliveredItemRow{Item: &it, OrderNumber: orderNumber, PaymentMethod: paymentMethod})
	}
	return out, rows.Err()
}

// ─── Payout Batches ──────────────────────────────────────────

func (s *Store) CreatePayoutBatch(ctx context.Context, batch *PayoutBatch, txns []*PayoutTransaction) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	batch.ID = uuid.New()
	batch.CreatedAt = time.Now()
	if _, err = tx.Exec(ctx, `
		INSERT INTO payout_batches (id,batch_date,payout_cycle_start,payout_cycle_end,
		  total_sellers,total_amount,status,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		batch.ID, batch.BatchDate, batch.CycleStart, batch.CycleEnd,
		batch.TotalSellers, batch.TotalAmount, batch.Status, batch.CreatedAt,
	); err != nil {
		return err
	}

	for _, t := range txns {
		t.ID = uuid.New()
		t.BatchID = batch.ID
		t.InitiatedAt = time.Now()
		if _, err = tx.Exec(ctx, `
			INSERT INTO payout_transactions (id,batch_id,seller_id,gross_amount,commission_amount,
			  platform_fee,tax_deducted,adjustment_amount,net_amount,bank_account_id,status,initiated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			t.ID, t.BatchID, t.SellerID, t.GrossAmount, t.CommissionAmount,
			t.PlatformFee, t.TaxDeducted, t.AdjustmentAmount, t.NetAmount,
			t.BankAccountID, t.Status, t.InitiatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
