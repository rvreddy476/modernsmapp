package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/food-service/internal/pricing"
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

	// Wave 1 B3.
	RestaurantID            uuid.UUID
	RestaurantLegalName     string
	PlatformInvoiceNumber   string
	RestaurantInvoiceNumber string
	FinalAmountPaise        int64
	// Breakdown is nil for an order placed before B3.
	Breakdown *pricing.Breakdown
	// Lines describes each item and add-on line of Breakdown, by ref.
	Lines map[string]InvoiceLineRef

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
	// Wave 1 B3, integer paise. LineTotalPaise is pre-tax.
	UnitPricePaise int64
	LineTotalPaise int64
	TaxAmountPaise int64
}

// GetInvoiceData pulls every field needed by the renderer.
// Restaurant address comes from food.restaurants (legacy columns) +
// the new state column; buyer address comes from the order snapshot.
func (s *Store) GetInvoiceData(ctx context.Context, userID, orderID uuid.UUID) (*InvoiceData, error) {
	d := &InvoiceData{OrderID: orderID}
	var rawBreakdown []byte
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
			COALESCE(o.delivery_address_snapshot->>'receiver_name', o.delivery_address_snapshot->>'name', ''),
			COALESCE(o.delivery_address_snapshot->>'city', ''),
			COALESCE(o.delivery_address_snapshot->>'state', ''),
			COALESCE(o.delivery_address_snapshot->>'address_line1', o.delivery_address_snapshot->>'line1', ''),
			r.id, COALESCE(r.legal_name, ''),
			COALESCE(o.platform_invoice_number, ''), COALESCE(o.restaurant_invoice_number, ''),
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint),
			o.tax_breakdown
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
		&d.RestaurantID, &d.RestaurantLegalName,
		&d.PlatformInvoiceNumber, &d.RestaurantInvoiceNumber, &d.FinalAmountPaise,
		&rawBreakdown,
	); err != nil {
		return nil, err
	}
	if err := s.loadInvoiceLines(ctx, d, rawBreakdown); err != nil {
		return nil, err
	}
	return d, nil
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
		INSERT INTO food.invoice_sequences (series, financial_year, last_number, updated_at)
		VALUES ('LEGACY', $1, 1, NOW())
		ON CONFLICT (series, financial_year) DO UPDATE
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
