package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ReturnRequest represents a return_requests row.
type ReturnRequest struct {
	ID             uuid.UUID  `json:"id"`
	OrderID        uuid.UUID  `json:"order_id"`
	BuyerID        uuid.UUID  `json:"buyer_id"`
	Reason         string     `json:"reason"`
	Description    string     `json:"description"`
	EvidenceURLs   []string   `json:"evidence_urls"`
	Status         string     `json:"status"`
	ReturnTracking *string    `json:"return_tracking,omitempty"`
	SellerNote     *string    `json:"seller_note,omitempty"`
	RefundAmount   *float64   `json:"refund_amount,omitempty"`
	RefundMethod   *string    `json:"refund_method,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

// CreateReturnRequest inserts a new return request and returns the created row.
func (s *Store) CreateReturnRequest(ctx context.Context, r *ReturnRequest) (*ReturnRequest, error) {
	r.ID = uuid.New()
	err := s.db.QueryRow(ctx,
		`INSERT INTO return_requests
		    (id, order_id, buyer_id, reason, description, evidence_urls)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, order_id, buyer_id, reason, description, evidence_urls,
		           status, return_tracking, seller_note, refund_amount, refund_method,
		           created_at, updated_at, resolved_at`,
		r.ID, r.OrderID, r.BuyerID, r.Reason, r.Description, r.EvidenceURLs,
	).Scan(
		&r.ID, &r.OrderID, &r.BuyerID, &r.Reason, &r.Description, &r.EvidenceURLs,
		&r.Status, &r.ReturnTracking, &r.SellerNote, &r.RefundAmount, &r.RefundMethod,
		&r.CreatedAt, &r.UpdatedAt, &r.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GetReturnRequest fetches a return request by ID.
func (s *Store) GetReturnRequest(ctx context.Context, id uuid.UUID) (*ReturnRequest, error) {
	var r ReturnRequest
	err := s.db.QueryRow(ctx,
		`SELECT id, order_id, buyer_id, reason, description, evidence_urls,
		        status, return_tracking, seller_note, refund_amount, refund_method,
		        created_at, updated_at, resolved_at
		 FROM return_requests WHERE id = $1`,
		id,
	).Scan(
		&r.ID, &r.OrderID, &r.BuyerID, &r.Reason, &r.Description, &r.EvidenceURLs,
		&r.Status, &r.ReturnTracking, &r.SellerNote, &r.RefundAmount, &r.RefundMethod,
		&r.CreatedAt, &r.UpdatedAt, &r.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetReturnRequestByOrder fetches the return request for an order.
func (s *Store) GetReturnRequestByOrder(ctx context.Context, orderID uuid.UUID) (*ReturnRequest, error) {
	var r ReturnRequest
	err := s.db.QueryRow(ctx,
		`SELECT id, order_id, buyer_id, reason, description, evidence_urls,
		        status, return_tracking, seller_note, refund_amount, refund_method,
		        created_at, updated_at, resolved_at
		 FROM return_requests WHERE order_id = $1`,
		orderID,
	).Scan(
		&r.ID, &r.OrderID, &r.BuyerID, &r.Reason, &r.Description, &r.EvidenceURLs,
		&r.Status, &r.ReturnTracking, &r.SellerNote, &r.RefundAmount, &r.RefundMethod,
		&r.CreatedAt, &r.UpdatedAt, &r.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateReturnStatus transitions a return request to a new status.
// For terminal statuses (refunded, completed, rejected) resolved_at is set to NOW().
func (s *Store) UpdateReturnStatus(ctx context.Context, id uuid.UUID, status string, sellerNote *string, refundAmount *float64, refundMethod *string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE return_requests SET
		    status        = $1,
		    seller_note   = COALESCE($2, seller_note),
		    refund_amount = COALESCE($3, refund_amount),
		    refund_method = COALESCE($4, refund_method),
		    updated_at    = NOW(),
		    resolved_at   = CASE WHEN $1 IN ('refunded','completed','rejected') THEN NOW() ELSE resolved_at END
		 WHERE id = $5`,
		status, sellerNote, refundAmount, refundMethod, id,
	)
	return err
}

// UpdateReturnTracking sets the return_tracking field for a return request.
func (s *Store) UpdateReturnTracking(ctx context.Context, id uuid.UUID, trackingNumber string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE return_requests SET return_tracking = $1, updated_at = NOW() WHERE id = $2`,
		trackingNumber, id,
	)
	return err
}

// ListReturnsByBuyer returns paginated return requests for a buyer.
func (s *Store) ListReturnsByBuyer(ctx context.Context, buyerID uuid.UUID, limit, offset int) ([]ReturnRequest, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, order_id, buyer_id, reason, description, evidence_urls,
		        status, return_tracking, seller_note, refund_amount, refund_method,
		        created_at, updated_at, resolved_at
		 FROM return_requests
		 WHERE buyer_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		buyerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReturnRows(rows)
}

// ListReturnsBySeller returns paginated return requests for a seller by joining orders.
// Pass empty string for status to return all statuses.
func (s *Store) ListReturnsBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]ReturnRequest, error) {
	query := `SELECT rr.id, rr.order_id, rr.buyer_id, rr.reason, rr.description, rr.evidence_urls,
		         rr.status, rr.return_tracking, rr.seller_note, rr.refund_amount, rr.refund_method,
		         rr.created_at, rr.updated_at, rr.resolved_at
		  FROM return_requests rr
		  JOIN orders.orders o ON o.id = rr.order_id
		  WHERE o.seller_id = $1`
	args := []any{sellerID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND rr.status = $%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY rr.created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReturnRows(rows)
}

// scanReturnRows scans pgx rows into a slice of ReturnRequest.
func scanReturnRows(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]ReturnRequest, error) {
	var results []ReturnRequest
	for rows.Next() {
		var r ReturnRequest
		if err := rows.Scan(
			&r.ID, &r.OrderID, &r.BuyerID, &r.Reason, &r.Description, &r.EvidenceURLs,
			&r.Status, &r.ReturnTracking, &r.SellerNote, &r.RefundAmount, &r.RefundMethod,
			&r.CreatedAt, &r.UpdatedAt, &r.ResolvedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
