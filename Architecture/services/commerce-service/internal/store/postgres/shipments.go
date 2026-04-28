package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── Invoice numbering ─────────────────────────────────────────────────────

// NextInvoiceSequence atomically increments and returns the next sequence for a FY.
func (s *Store) NextInvoiceSequence(ctx context.Context, fy string) (int64, error) {
	var seq int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO invoice_sequences (financial_year, last_sequence)
		VALUES ($1, 1)
		ON CONFLICT (financial_year) DO UPDATE
		SET last_sequence = invoice_sequences.last_sequence + 1,
		    updated_at = NOW()
		RETURNING last_sequence
	`, fy).Scan(&seq)
	return seq, err
}

// ── Invoices ──────────────────────────────────────────────────────────────

type Invoice struct {
	ID            uuid.UUID `db:"id"`
	OrderID       uuid.UUID `db:"order_id"`
	InvoiceNumber string    `db:"invoice_number"`
	FinancialYear string    `db:"financial_year"`
	Sequence      int64     `db:"sequence"`
	SellerID      uuid.UUID `db:"seller_id"`
	BuyerUserID   uuid.UUID `db:"buyer_user_id"`
	GrandTotal    float64   `db:"grand_total"`
	CurrencyCode  string    `db:"currency_code"`
	IsInterstate  bool      `db:"is_interstate"`
	CGSTTotal     float64   `db:"cgst_total"`
	SGSTTotal     float64   `db:"sgst_total"`
	IGSTTotal     float64   `db:"igst_total"`
	HTMLMediaKey  *string   `db:"html_media_key"`
	PDFMediaKey   *string   `db:"pdf_media_key"`
	IssuedAt      time.Time `db:"issued_at"`
	CreatedAt     time.Time `db:"created_at"`
}

func (s *Store) CreateInvoice(ctx context.Context, inv *Invoice) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO invoices
		  (order_id, invoice_number, financial_year, sequence, seller_id, buyer_user_id,
		   grand_total, currency_code, is_interstate, cgst_total, sgst_total, igst_total, html_media_key, pdf_media_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, issued_at, created_at
	`, inv.OrderID, inv.InvoiceNumber, inv.FinancialYear, inv.Sequence, inv.SellerID, inv.BuyerUserID,
		inv.GrandTotal, inv.CurrencyCode, inv.IsInterstate, inv.CGSTTotal, inv.SGSTTotal, inv.IGSTTotal,
		inv.HTMLMediaKey, inv.PDFMediaKey,
	).Scan(&inv.ID, &inv.IssuedAt, &inv.CreatedAt)
}

func (s *Store) GetInvoiceByOrder(ctx context.Context, orderID uuid.UUID) (*Invoice, error) {
	inv := &Invoice{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, invoice_number, financial_year, sequence, seller_id, buyer_user_id,
		       grand_total, currency_code, is_interstate, cgst_total, sgst_total, igst_total,
		       html_media_key, pdf_media_key, issued_at, created_at
		FROM invoices WHERE order_id = $1
	`, orderID).Scan(&inv.ID, &inv.OrderID, &inv.InvoiceNumber, &inv.FinancialYear, &inv.Sequence,
		&inv.SellerID, &inv.BuyerUserID, &inv.GrandTotal, &inv.CurrencyCode, &inv.IsInterstate,
		&inv.CGSTTotal, &inv.SGSTTotal, &inv.IGSTTotal, &inv.HTMLMediaKey, &inv.PDFMediaKey,
		&inv.IssuedAt, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// ── Shipments ─────────────────────────────────────────────────────────────

type Shipment struct {
	ID             uuid.UUID  `db:"id"`
	OrderID        uuid.UUID  `db:"order_id"`
	SellerID       uuid.UUID  `db:"seller_id"`
	Courier        string     `db:"courier"`
	TrackingNumber *string    `db:"tracking_number"`
	CourierOrderID *string    `db:"courier_order_id"`
	LabelURL       *string    `db:"label_url"`
	TrackingURL    *string    `db:"tracking_url"`
	Status         string     `db:"status"`
	ETA            *time.Time `db:"eta"`
	ShippedAt      *time.Time `db:"shipped_at"`
	DeliveredAt    *time.Time `db:"delivered_at"`
	LastEventAt    *time.Time `db:"last_event_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

func (s *Store) CreateShipment(ctx context.Context, sh *Shipment) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO shipments
		  (order_id, seller_id, courier, tracking_number, courier_order_id, label_url, tracking_url, status, eta, shipped_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at
	`, sh.OrderID, sh.SellerID, sh.Courier, sh.TrackingNumber, sh.CourierOrderID,
		sh.LabelURL, sh.TrackingURL, sh.Status, sh.ETA, sh.ShippedAt,
	).Scan(&sh.ID, &sh.CreatedAt, &sh.UpdatedAt)
}

func (s *Store) GetShipmentByOrder(ctx context.Context, orderID uuid.UUID) (*Shipment, error) {
	sh := &Shipment{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, seller_id, courier, tracking_number, courier_order_id, label_url, tracking_url,
		       status, eta, shipped_at, delivered_at, last_event_at, created_at, updated_at
		FROM shipments WHERE order_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, orderID).Scan(&sh.ID, &sh.OrderID, &sh.SellerID, &sh.Courier, &sh.TrackingNumber, &sh.CourierOrderID,
		&sh.LabelURL, &sh.TrackingURL, &sh.Status, &sh.ETA, &sh.ShippedAt, &sh.DeliveredAt,
		&sh.LastEventAt, &sh.CreatedAt, &sh.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sh, nil
}

// ListShipmentsByOrder returns every shipment for the order, ordered oldest
// first. Multi-seller orders create one shipment per seller (each seller's
// items ship from their own pickup address), so callers that need full
// fulfillment state must use this rather than GetShipmentByOrder.
func (s *Store) ListShipmentsByOrder(ctx context.Context, orderID uuid.UUID) ([]*Shipment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, seller_id, courier, tracking_number, courier_order_id, label_url, tracking_url,
		       status, eta, shipped_at, delivered_at, last_event_at, created_at, updated_at
		FROM shipments WHERE order_id = $1
		ORDER BY created_at ASC
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Shipment
	for rows.Next() {
		sh := &Shipment{}
		if err := rows.Scan(&sh.ID, &sh.OrderID, &sh.SellerID, &sh.Courier, &sh.TrackingNumber, &sh.CourierOrderID,
			&sh.LabelURL, &sh.TrackingURL, &sh.Status, &sh.ETA, &sh.ShippedAt, &sh.DeliveredAt,
			&sh.LastEventAt, &sh.CreatedAt, &sh.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// GetShipmentByOrderAndSeller returns the shipment for one seller's items
// in the order, or pgx.ErrNoRows if that seller hasn't been booked yet.
// Used by the multi-seller idempotency check in CreateShipmentsForOrder.
func (s *Store) GetShipmentByOrderAndSeller(ctx context.Context, orderID, sellerID uuid.UUID) (*Shipment, error) {
	sh := &Shipment{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, seller_id, courier, tracking_number, courier_order_id, label_url, tracking_url,
		       status, eta, shipped_at, delivered_at, last_event_at, created_at, updated_at
		FROM shipments WHERE order_id = $1 AND seller_id = $2
		ORDER BY created_at DESC LIMIT 1
	`, orderID, sellerID).Scan(&sh.ID, &sh.OrderID, &sh.SellerID, &sh.Courier, &sh.TrackingNumber, &sh.CourierOrderID,
		&sh.LabelURL, &sh.TrackingURL, &sh.Status, &sh.ETA, &sh.ShippedAt, &sh.DeliveredAt,
		&sh.LastEventAt, &sh.CreatedAt, &sh.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sh, nil
}

func (s *Store) GetShipmentByTracking(ctx context.Context, courier, tracking string) (*Shipment, error) {
	sh := &Shipment{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, seller_id, courier, tracking_number, courier_order_id, label_url, tracking_url,
		       status, eta, shipped_at, delivered_at, last_event_at, created_at, updated_at
		FROM shipments WHERE courier = $1 AND tracking_number = $2
	`, courier, tracking).Scan(&sh.ID, &sh.OrderID, &sh.SellerID, &sh.Courier, &sh.TrackingNumber,
		&sh.CourierOrderID, &sh.LabelURL, &sh.TrackingURL, &sh.Status, &sh.ETA, &sh.ShippedAt,
		&sh.DeliveredAt, &sh.LastEventAt, &sh.CreatedAt, &sh.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sh, nil
}

func (s *Store) UpdateShipmentStatus(ctx context.Context, shipmentID uuid.UUID, status string, occurredAt time.Time) error {
	var deliveredClause string
	args := []any{status, occurredAt, shipmentID}
	if status == "delivered" {
		deliveredClause = ", delivered_at = $2"
	}
	_, err := s.db.Exec(ctx, fmt.Sprintf(`
		UPDATE shipments
		SET status = $1, last_event_at = $2, updated_at = NOW() %s
		WHERE id = $3
	`, deliveredClause), args...)
	return err
}

func (s *Store) AppendShipmentEvent(ctx context.Context, shipmentID uuid.UUID, status, location, remark string, occurredAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shipment_events (shipment_id, status, location, remark, occurred_at)
		VALUES ($1,$2,$3,$4,$5)
	`, shipmentID, status, location, remark, occurredAt)
	return err
}

func (s *Store) ListShipmentEvents(ctx context.Context, shipmentID uuid.UUID) ([]*ShipmentEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, shipment_id, status, location, remark, occurred_at, created_at
		FROM shipment_events WHERE shipment_id = $1
		ORDER BY occurred_at DESC
	`, shipmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ShipmentEvent
	for rows.Next() {
		e := &ShipmentEvent{}
		if err := rows.Scan(&e.ID, &e.ShipmentID, &e.Status, &e.Location, &e.Remark, &e.OccurredAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

type ShipmentEvent struct {
	ID         uuid.UUID `db:"id"`
	ShipmentID uuid.UUID `db:"shipment_id"`
	Status     string    `db:"status"`
	Location   *string   `db:"location"`
	Remark     *string   `db:"remark"`
	OccurredAt time.Time `db:"occurred_at"`
	CreatedAt  time.Time `db:"created_at"`
}
