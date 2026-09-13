package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/google/uuid"
)

// Invoice data and numbering for per-party invoices (Wave 1 B3).
//
// Series: the platform numbers from series PLATFORM ("FP/<yyyy>/<seq>"), each
// restaurant from its own series R:<restaurant id> ("FR/<yyyy>/<seq>"), where
// <yyyy> is the compact financial year (2026-27 -> 2627). Both fit the
// 16-character invoice number limit. The format and series are adviser
// question 14.

// InvoiceLineRef describes one item or add-on line of an order's breakdown.
type InvoiceLineRef struct {
	Name           string
	Quantity       int64
	UnitPricePaise int64
}

// loadInvoiceLines decodes the stored breakdown and reads the order's items
// and add-ons, keyed by the refs PlaceOrder wrote into the breakdown.
func (s *Store) loadInvoiceLines(ctx context.Context, d *InvoiceData, rawBreakdown []byte) error {
	if len(rawBreakdown) > 0 {
		var b pricing.Breakdown
		if err := json.Unmarshal(rawBreakdown, &b); err != nil {
			return fmt.Errorf("decode tax breakdown: %w", err)
		}
		d.Breakdown = &b
	}
	d.Lines = map[string]InvoiceLineRef{}
	rows, err := s.db.Query(ctx, `
		SELECT oi.id, oi.item_name_snapshot, COALESCE(mi.hsn_code, ''), oi.quantity,
			oi.unit_price_snapshot::float8, oi.tax_amount::float8, oi.line_total::float8,
			oi.tax_percentage_snapshot::float8,
			COALESCE(oi.unit_price_paise, ROUND(oi.unit_price_snapshot * 100)::bigint),
			COALESCE(oi.line_total_paise, ROUND(oi.line_total * 100)::bigint),
			COALESCE(oi.tax_amount_paise, ROUND(oi.tax_amount * 100)::bigint)
		FROM food.order_items oi
		LEFT JOIN food.menu_items mi ON mi.id = oi.menu_item_id
		WHERE oi.order_id = $1
		ORDER BY oi.created_at, oi.id
	`, d.OrderID)
	if err != nil {
		return fmt.Errorf("query items: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var it InvoiceItem
		if err := rows.Scan(&id, &it.Name, &it.HSN, &it.Quantity, &it.UnitPrice, &it.TaxAmount, &it.LineTotal,
			&it.TaxPct, &it.UnitPricePaise, &it.LineTotalPaise, &it.TaxAmountPaise); err != nil {
			rows.Close()
			return err
		}
		d.Items = append(d.Items, it)
		d.Lines[orderItemRef(id)] = InvoiceLineRef{Name: it.Name, Quantity: int64(it.Quantity), UnitPricePaise: it.UnitPricePaise}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	addons, err := s.db.Query(ctx, `
		SELECT oia.id, oia.addon_name_snapshot, oia.quantity,
			COALESCE(oia.unit_price_paise, ROUND(oia.unit_price_snapshot * 100)::bigint)
		FROM food.order_item_addons oia
		JOIN food.order_items oi ON oi.id = oia.order_item_id
		WHERE oi.order_id = $1
	`, d.OrderID)
	if err != nil {
		return fmt.Errorf("query add-ons: %w", err)
	}
	defer addons.Close()
	for addons.Next() {
		var id uuid.UUID
		var ref InvoiceLineRef
		var qty int
		if err := addons.Scan(&id, &ref.Name, &qty, &ref.UnitPricePaise); err != nil {
			return err
		}
		ref.Quantity = int64(qty)
		d.Lines[orderAddonRef(id)] = ref
	}
	return addons.Err()
}

// compactFinancialYear turns "2026-27" into "2627".
func compactFinancialYear(fy string) string {
	if len(fy) == 7 && fy[4] == '-' {
		return fy[2:4] + fy[5:7]
	}
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, fy)
}

// AllocateOrderInvoiceNumbers allocates, once, the platform and/or restaurant
// invoice numbers an order needs and stores them on the order. Idempotent: a
// number already stored is returned unchanged. orders.invoice_number (the
// X-Invoice-Number header) keeps any pre-B3 number, else the platform number,
// else the restaurant number.
func (s *Store) AllocateOrderInvoiceNumbers(ctx context.Context, orderID, restaurantID uuid.UUID, financialYear string, platform, restaurant bool) (string, string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	var platformNo, restaurantNo string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(platform_invoice_number, ''), COALESCE(restaurant_invoice_number, '')
		FROM food.orders WHERE id = $1 FOR UPDATE
	`, orderID).Scan(&platformNo, &restaurantNo); err != nil {
		return "", "", err
	}
	next := func(series, prefix string) (string, error) {
		var seq int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO food.invoice_sequences (series, financial_year, last_number, updated_at)
			VALUES ($1, $2, 1, NOW())
			ON CONFLICT (series, financial_year) DO UPDATE
			SET last_number = food.invoice_sequences.last_number + 1,
				updated_at = NOW()
			RETURNING last_number
		`, series, financialYear).Scan(&seq); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s/%s/%06d", prefix, compactFinancialYear(financialYear), seq), nil
	}
	if platform && platformNo == "" {
		if platformNo, err = next("PLATFORM", "FP"); err != nil {
			return "", "", err
		}
	}
	if restaurant && restaurantNo == "" {
		if restaurantNo, err = next("R:"+restaurantID.String(), "FR"); err != nil {
			return "", "", err
		}
	}
	primary := platformNo
	if primary == "" {
		primary = restaurantNo
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET platform_invoice_number = NULLIF($2, ''),
			restaurant_invoice_number = NULLIF($3, ''),
			invoice_number = COALESCE(invoice_number, NULLIF($4, ''))
		WHERE id = $1
	`, orderID, platformNo, restaurantNo, primary); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return platformNo, restaurantNo, nil
}
