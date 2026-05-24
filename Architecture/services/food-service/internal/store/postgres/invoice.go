package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// InvoiceData is everything the GST invoice renderer needs in one
// pass — fewer round trips than re-querying via GetOrder.
type InvoiceData struct {
	OrderID            uuid.UUID
	OrderNumber        string
	PlacedAt           time.Time
	InvoiceNumber      string // may be empty until allocate-on-first-pull
	Subtotal           float64
	TaxTotal           float64
	DeliveryFee        float64
	PackagingFee       float64
	CouponCode         string
	CouponDiscount     float64
	GrandTotal         float64

	RestaurantName     string
	RestaurantGSTIN    string
	RestaurantState    string
	RestaurantAddrLine string
	RestaurantCity     string

	BuyerName          string
	BuyerCity          string
	BuyerState         string
	BuyerAddrLine      string

	Items []InvoiceItem
}

// InvoiceItem mirrors one line on the invoice.
type InvoiceItem struct {
	Name      string
	HSN       string
	Quantity  int
	UnitPrice float64
	TaxAmount float64
	LineTotal float64
	TaxPct    float64
}

// GetInvoiceData pulls every field needed by the renderer.
// Restaurant address comes from food.restaurants (legacy columns) +
// the new state column; buyer address comes from the order snapshot.
func (s *Store) GetInvoiceData(ctx context.Context, userID, orderID uuid.UUID) (*InvoiceData, error) {
	d := &InvoiceData{OrderID: orderID}
	if err := s.db.QueryRow(ctx, `
		SELECT
			o.order_number, o.placed_at,
			COALESCE(o.invoice_number, ''),
			o.item_subtotal::float8, o.tax_total::float8,
			o.delivery_fee::float8, o.packaging_fee::float8,
			COALESCE(o.coupon_code, ''),
			o.coupon_discount::float8, o.final_amount::float8,
			r.name, COALESCE(r.gstin, ''),
			COALESCE(r.state, ''), r.address_line1, r.city,
			COALESCE(o.delivery_address_snapshot->>'name', ''),
			COALESCE(o.delivery_address_snapshot->>'city', ''),
			COALESCE(o.delivery_address_snapshot->>'state', ''),
			COALESCE(o.delivery_address_snapshot->>'line1', '')
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		WHERE o.id = $1 AND o.user_id = $2
	`, orderID, userID).Scan(
		&d.OrderNumber, &d.PlacedAt, &d.InvoiceNumber,
		&d.Subtotal, &d.TaxTotal, &d.DeliveryFee, &d.PackagingFee,
		&d.CouponCode, &d.CouponDiscount, &d.GrandTotal,
		&d.RestaurantName, &d.RestaurantGSTIN, &d.RestaurantState,
		&d.RestaurantAddrLine, &d.RestaurantCity,
		&d.BuyerName, &d.BuyerCity, &d.BuyerState, &d.BuyerAddrLine,
	); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT
			oi.item_name_snapshot,
			COALESCE(mi.hsn_code, ''),
			oi.quantity, oi.unit_price_snapshot::float8,
			oi.tax_amount::float8, oi.line_total::float8,
			oi.tax_percentage_snapshot::float8
		FROM food.order_items oi
		LEFT JOIN food.menu_items mi ON mi.id = oi.menu_item_id
		WHERE oi.order_id = $1
		ORDER BY oi.created_at
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("query items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it InvoiceItem
		if err := rows.Scan(&it.Name, &it.HSN, &it.Quantity, &it.UnitPrice,
			&it.TaxAmount, &it.LineTotal, &it.TaxPct); err != nil {
			return nil, err
		}
		d.Items = append(d.Items, it)
	}
	return d, rows.Err()
}

// AllocateInvoiceNumber atomically picks the next number in the
// current FY sequence + persists it on the order. Idempotent — if the
// order already has an invoice_number, return it unchanged.
func (s *Store) AllocateInvoiceNumber(ctx context.Context, orderID uuid.UUID, financialYear string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var existing string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(invoice_number, '')
		FROM food.orders WHERE id = $1 FOR UPDATE
	`, orderID).Scan(&existing); err != nil {
		return "", err
	}
	if existing != "" {
		return existing, tx.Commit(ctx)
	}
	var seq int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.invoice_sequences (financial_year, last_number, updated_at)
		VALUES ($1, 1, NOW())
		ON CONFLICT (financial_year) DO UPDATE
		SET last_number = food.invoice_sequences.last_number + 1,
			updated_at = NOW()
		RETURNING last_number
	`, financialYear).Scan(&seq); err != nil {
		return "", err
	}
	number := fmt.Sprintf("FIGO/%s/%06d", financialYear, seq)
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET invoice_number = $2 WHERE id = $1
	`, orderID, number); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return number, nil
}
