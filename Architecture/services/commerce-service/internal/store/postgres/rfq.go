// Phase F2.2 — RFQ (Request For Quote) data access.
//
// Lifecycle:
//
//	requested → quoted → accepted → (order created)
//	          ↘ rejected
//	          ↘ expired (by sweeper)
//
// Personal + org buyers both supported; the org_id column is nullable.
// rfq_quotes carries the seller's per-line pricing as a JSONB blob so
// AcceptRFQQuote can build the order without a second query.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RFQ struct {
	ID             uuid.UUID  `db:"id" json:"id"`
	BuyerUserID    uuid.UUID  `db:"buyer_user_id" json:"buyer_user_id"`
	OrganizationID *uuid.UUID `db:"organization_id" json:"organization_id,omitempty"`
	SellerID       uuid.UUID  `db:"seller_id" json:"seller_id"`
	Status         string     `db:"status" json:"status"`
	MessageText    *string    `db:"message_text" json:"message_text,omitempty"`
	RequestedAt    time.Time  `db:"requested_at" json:"requested_at"`
	ExpiresAt      time.Time  `db:"expires_at" json:"expires_at"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
}

type RFQItem struct {
	ID        uuid.UUID `db:"id" json:"id"`
	RFQID     uuid.UUID `db:"rfq_id" json:"rfq_id"`
	VariantID uuid.UUID `db:"variant_id" json:"variant_id"`
	Quantity  int       `db:"quantity" json:"quantity"`
	Notes     *string   `db:"notes" json:"notes,omitempty"`
}

type RFQQuote struct {
	ID           uuid.UUID       `db:"id" json:"id"`
	RFQID        uuid.UUID       `db:"rfq_id" json:"rfq_id"`
	QuotedTotal  float64         `db:"quoted_total" json:"quoted_total"`
	LinePrices   json.RawMessage `db:"line_prices" json:"line_prices"`
	ValidityDays int             `db:"validity_days" json:"validity_days"`
	QuotedAt     time.Time       `db:"quoted_at" json:"quoted_at"`
	ExpiresAt    time.Time       `db:"expires_at" json:"expires_at"`
	AcceptedAt   *time.Time      `db:"accepted_at" json:"accepted_at,omitempty"`
	OrderID      *uuid.UUID      `db:"order_id" json:"order_id,omitempty"`
}

// RFQLinePrice is one row inside RFQQuote.LinePrices JSONB.
type RFQLinePrice struct {
	RFQItemID uuid.UUID `json:"rfq_item_id"`
	VariantID uuid.UUID `json:"variant_id"`
	Quantity  int       `json:"quantity"`
	UnitPrice float64   `json:"unit_price"`
	LineTotal float64   `json:"line_total"`
}

// CreateRFQ inserts an RFQ + its items in one tx. Variant ownership
// validation lives in the service layer (it has the seller cache).
func (s *Store) CreateRFQ(ctx context.Context, r *RFQ, items []*RFQItem) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := tx.QueryRow(ctx, `
		INSERT INTO rfqs (buyer_user_id, organization_id, seller_id, status, message_text, expires_at)
		VALUES ($1, $2, $3, 'requested', $4, $5)
		RETURNING id, requested_at, created_at`,
		r.BuyerUserID, r.OrganizationID, r.SellerID, r.MessageText, r.ExpiresAt,
	).Scan(&r.ID, &r.RequestedAt, &r.CreatedAt); err != nil {
		return err
	}
	r.Status = "requested"
	for _, it := range items {
		if err := tx.QueryRow(ctx, `
			INSERT INTO rfq_items (rfq_id, variant_id, quantity, notes)
			VALUES ($1, $2, $3, $4)
			RETURNING id`,
			r.ID, it.VariantID, it.Quantity, it.Notes,
		).Scan(&it.ID); err != nil {
			return err
		}
		it.RFQID = r.ID
	}
	return tx.Commit(ctx)
}

func (s *Store) GetRFQByID(ctx context.Context, id uuid.UUID) (*RFQ, error) {
	r := &RFQ{}
	err := s.db.QueryRow(ctx, `
		SELECT id, buyer_user_id, organization_id, seller_id, status,
		       message_text, requested_at, expires_at, created_at
		FROM rfqs WHERE id = $1`, id).Scan(
		&r.ID, &r.BuyerUserID, &r.OrganizationID, &r.SellerID, &r.Status,
		&r.MessageText, &r.RequestedAt, &r.ExpiresAt, &r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

func (s *Store) GetRFQItems(ctx context.Context, rfqID uuid.UUID) ([]*RFQItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, rfq_id, variant_id, quantity, notes
		FROM rfq_items WHERE rfq_id = $1
		ORDER BY id`, rfqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RFQItem
	for rows.Next() {
		var it RFQItem
		if err := rows.Scan(&it.ID, &it.RFQID, &it.VariantID, &it.Quantity, &it.Notes); err != nil {
			return nil, err
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

func (s *Store) ListRFQsForBuyer(ctx context.Context, buyerID uuid.UUID, limit, offset int) ([]*RFQ, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, buyer_user_id, organization_id, seller_id, status,
		       message_text, requested_at, expires_at, created_at
		FROM rfqs WHERE buyer_user_id = $1
		ORDER BY requested_at DESC
		LIMIT $2 OFFSET $3`, buyerID, limit, offset)
	if err != nil {
		return nil, err
	}
	return scanRFQRows(rows)
}

func (s *Store) ListRFQsForSeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*RFQ, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, buyer_user_id, organization_id, seller_id, status,
			       message_text, requested_at, expires_at, created_at
			FROM rfqs WHERE seller_id = $1
			ORDER BY requested_at DESC
			LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, buyer_user_id, organization_id, seller_id, status,
			       message_text, requested_at, expires_at, created_at
			FROM rfqs WHERE seller_id = $1 AND status = $2
			ORDER BY requested_at DESC
			LIMIT $3 OFFSET $4`, sellerID, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	return scanRFQRows(rows)
}

func scanRFQRows(rows pgx.Rows) ([]*RFQ, error) {
	defer rows.Close()
	var out []*RFQ
	for rows.Next() {
		var r RFQ
		if err := rows.Scan(&r.ID, &r.BuyerUserID, &r.OrganizationID, &r.SellerID, &r.Status,
			&r.MessageText, &r.RequestedAt, &r.ExpiresAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// SaveRFQQuote inserts a new quote AND moves the parent RFQ to status
// 'quoted' in one tx. Sellers may quote once per RFQ; a follow-up
// quote replaces the prior one (older quotes stay in the table for
// audit but the parent RFQ only tracks the most recent).
func (s *Store) SaveRFQQuote(ctx context.Context, q *RFQQuote) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := tx.QueryRow(ctx, `
		INSERT INTO rfq_quotes (rfq_id, quoted_total, line_prices, validity_days, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, quoted_at`,
		q.RFQID, q.QuotedTotal, q.LinePrices, q.ValidityDays, q.ExpiresAt,
	).Scan(&q.ID, &q.QuotedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE rfqs SET status = 'quoted' WHERE id = $1`, q.RFQID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetRFQQuote(ctx context.Context, quoteID uuid.UUID) (*RFQQuote, error) {
	q := &RFQQuote{}
	err := s.db.QueryRow(ctx, `
		SELECT id, rfq_id, quoted_total, line_prices, validity_days,
		       quoted_at, expires_at, accepted_at, order_id
		FROM rfq_quotes WHERE id = $1`, quoteID).Scan(
		&q.ID, &q.RFQID, &q.QuotedTotal, &q.LinePrices, &q.ValidityDays,
		&q.QuotedAt, &q.ExpiresAt, &q.AcceptedAt, &q.OrderID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return q, nil
}

func (s *Store) ListRFQQuotes(ctx context.Context, rfqID uuid.UUID) ([]*RFQQuote, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, rfq_id, quoted_total, line_prices, validity_days,
		       quoted_at, expires_at, accepted_at, order_id
		FROM rfq_quotes WHERE rfq_id = $1
		ORDER BY quoted_at DESC`, rfqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RFQQuote
	for rows.Next() {
		var q RFQQuote
		if err := rows.Scan(&q.ID, &q.RFQID, &q.QuotedTotal, &q.LinePrices, &q.ValidityDays,
			&q.QuotedAt, &q.ExpiresAt, &q.AcceptedAt, &q.OrderID); err != nil {
			return nil, err
		}
		out = append(out, &q)
	}
	return out, rows.Err()
}

// MarkRFQQuoteAccepted stamps the accepted quote with the order id +
// flips the RFQ to status 'accepted'. Caller wraps this with the order
// creation in their own tx so the quote, RFQ, and order all commit or
// roll back together.
func (s *Store) MarkRFQQuoteAccepted(ctx context.Context, tx pgx.Tx, quoteID, orderID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE rfq_quotes
		SET accepted_at = NOW(), order_id = $2
		WHERE id = $1 AND accepted_at IS NULL`,
		quoteID, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE rfqs SET status = 'accepted'
		WHERE id = (SELECT rfq_id FROM rfq_quotes WHERE id = $1)`, quoteID); err != nil {
		return err
	}
	return nil
}

// UpdateRFQStatus is the small state-machine setter. Callers should
// pass one of: requested, quoted, accepted, expired, rejected, cancelled.
func (s *Store) UpdateRFQStatus(ctx context.Context, rfqID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE rfqs SET status = $2 WHERE id = $1`, rfqID, status)
	return err
}

// ExpireStaleQuotes flips quote status when their validity window
// closes. Returns the number of rows touched so the worker can log /
// alert. Quotes don't have a status column directly — we use the
// parent RFQ's status; this helper is therefore advisory and used by
// service.ExpireRFQs.
func (s *Store) ExpireStaleRFQs(ctx context.Context) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE rfqs SET status = 'expired'
		WHERE status IN ('requested','quoted')
		  AND expires_at <= NOW()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
