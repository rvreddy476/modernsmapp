package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ticket mirrors food.support_tickets.
type Ticket struct {
	ID         uuid.UUID  `json:"id"`
	CustomerID uuid.UUID  `json:"customer_id"`
	OrderID    *uuid.UUID `json:"order_id,omitempty"`
	Category   string     `json:"category"`
	Subject    string     `json:"subject"`
	Detail     *string    `json:"detail,omitempty"`
	Status     string     `json:"status"`
	AssignedTo *uuid.UUID `json:"assigned_to,omitempty"`
	ResolvedAt *string    `json:"resolved_at,omitempty"`
	CreatedAt  string     `json:"created_at"`
}

// TicketMessage mirrors food.ticket_messages.
type TicketMessage struct {
	ID        uuid.UUID `json:"id"`
	TicketID  uuid.UUID `json:"ticket_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	IsAdmin   bool      `json:"is_admin"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"created_at"`
}

// RefundRequest mirrors food.refund_requests.
type RefundRequest struct {
	ID                uuid.UUID  `json:"id"`
	TicketID          *uuid.UUID `json:"ticket_id,omitempty"`
	OrderID           uuid.UUID  `json:"order_id"`
	CustomerID        uuid.UUID  `json:"customer_id"`
	Amount            float64    `json:"amount"`
	Reason            *string    `json:"reason,omitempty"`
	Status            string     `json:"status"`
	DecidedBy         *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt         *string    `json:"decided_at,omitempty"`
	RefundTxnID       *string    `json:"refund_txn_id,omitempty"`
	CreatedAt         string     `json:"created_at"`
	ExecutionRefundID *uuid.UUID `json:"execution_refund_id,omitempty"`
	ExecutionStatus   *string    `json:"execution_status,omitempty"`
	ExecutionError    *string    `json:"execution_error,omitempty"`
}

var ErrInvalidRefundAmount = errors.New("refund amount must be whole paise and within the remaining refundable balance")
var ErrSupportOrderForbidden = errors.New("order or ticket is not available to this account")
var ErrRefundDecisionConflict = errors.New("refund request was already decided differently")

func refundAmountMinor(amount float64) (int64, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 || amount > 99999999.99 {
		return 0, ErrInvalidRefundAmount
	}
	minor := int64(math.Round(amount * 100))
	if minor <= 0 || math.Abs(amount*100-float64(minor)) > 0.000001 {
		return 0, ErrInvalidRefundAmount
	}
	return minor, nil
}

// CreateTicket inserts a new support ticket.
type CreateTicketInput struct {
	CustomerID uuid.UUID
	OrderID    *uuid.UUID
	Category   string
	Subject    string
	Detail     string
}

func (s *Store) CreateTicket(ctx context.Context, in CreateTicketInput) (*Ticket, error) {
	if in.OrderID != nil {
		var allowed bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.orders o
			WHERE o.id=$1 AND (o.user_id=$2 OR EXISTS(SELECT 1 FROM food.restaurants r WHERE r.id=o.restaurant_id AND r.owner_user_id=$2)
			OR EXISTS(SELECT 1 FROM food.delivery_assignments a JOIN food.delivery_partners p ON p.id=a.delivery_partner_id WHERE a.order_id=o.id AND p.user_id=$2)))`, *in.OrderID, in.CustomerID).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrSupportOrderForbidden
		}
	}
	var t Ticket
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.support_tickets (customer_id, order_id, category, subject, detail)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		RETURNING id, customer_id, order_id, category, subject, detail, status::text,
			assigned_to, resolved_at::text, created_at::text
	`, in.CustomerID, in.OrderID, in.Category, in.Subject, in.Detail).Scan(
		&t.ID, &t.CustomerID, &t.OrderID, &t.Category, &t.Subject, &t.Detail,
		&t.Status, &t.AssignedTo, &t.ResolvedAt, &t.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListMyTickets returns tickets visible to the customer.
func (s *Store) ListMyTickets(ctx context.Context, customerID uuid.UUID) ([]Ticket, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, customer_id, order_id, category, subject, detail, status::text,
			assigned_to, resolved_at::text, created_at::text
		FROM food.support_tickets
		WHERE customer_id = $1
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.CustomerID, &t.OrderID, &t.Category, &t.Subject,
			&t.Detail, &t.Status, &t.AssignedTo, &t.ResolvedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AppendTicketMessage adds a message to a ticket. customerID OR
// adminID must match; isAdmin controls the column. Visibility check
// happens in the service layer.
func (s *Store) AppendTicketMessage(ctx context.Context, ticketID, authorID uuid.UUID, isAdmin bool, body string) (*TicketMessage, error) {
	var m TicketMessage
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.ticket_messages (ticket_id, author_id, is_admin, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id, ticket_id, author_id, is_admin, body, created_at::text
	`, ticketID, authorID, isAdmin, body).Scan(
		&m.ID, &m.TicketID, &m.AuthorID, &m.IsAdmin, &m.Body, &m.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &m, nil
}

// GetTicketWithMessages returns the ticket + its messages. Visibility:
// the customer who created the ticket OR any admin.
func (s *Store) GetTicketWithMessages(ctx context.Context, ticketID uuid.UUID) (*Ticket, []TicketMessage, error) {
	var t Ticket
	if err := s.db.QueryRow(ctx, `
		SELECT id, customer_id, order_id, category, subject, detail, status::text,
			assigned_to, resolved_at::text, created_at::text
		FROM food.support_tickets WHERE id = $1
	`, ticketID).Scan(&t.ID, &t.CustomerID, &t.OrderID, &t.Category, &t.Subject,
		&t.Detail, &t.Status, &t.AssignedTo, &t.ResolvedAt, &t.CreatedAt); err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, ticket_id, author_id, is_admin, body, created_at::text
		FROM food.ticket_messages
		WHERE ticket_id = $1 ORDER BY created_at
	`, ticketID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var msgs []TicketMessage
	for rows.Next() {
		var m TicketMessage
		if err := rows.Scan(&m.ID, &m.TicketID, &m.AuthorID, &m.IsAdmin, &m.Body, &m.CreatedAt); err != nil {
			return nil, nil, err
		}
		msgs = append(msgs, m)
	}
	return &t, msgs, rows.Err()
}

// ListTicketsForAdmin returns every ticket in `status` (or all if
// empty), newest first. Admin / moderator only — service layer
// enforces the scope.
func (s *Store) ListTicketsForAdmin(ctx context.Context, status string, limit int) ([]Ticket, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, customer_id, order_id, category, subject, detail, status::text,
			assigned_to, resolved_at::text, created_at::text
		FROM food.support_tickets
		WHERE $1 = '' OR status::text = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.CustomerID, &t.OrderID, &t.Category, &t.Subject,
			&t.Detail, &t.Status, &t.AssignedTo, &t.ResolvedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListRefundsForAdmin returns refund requests filtered by status.
// Most-recent first.
func (s *Store) ListRefundsForAdmin(ctx context.Context, status string, limit int) ([]RefundRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.ticket_id, q.order_id, q.customer_id, q.amount::float8, q.reason,
			q.status::text, q.decided_by, q.decided_at::text, q.refund_txn_id, q.created_at::text,
			q.execution_refund_id, r.status, r.raw_response->>'last_submission_error'
		FROM food.refund_requests q LEFT JOIN food.refunds r ON r.id=q.execution_refund_id
		WHERE $1 = '' OR q.status::text = $1
		ORDER BY q.created_at DESC
		LIMIT $2
	`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundRequest
	for rows.Next() {
		var r RefundRequest
		if err := rows.Scan(&r.ID, &r.TicketID, &r.OrderID, &r.CustomerID, &r.Amount,
			&r.Reason, &r.Status, &r.DecidedBy, &r.DecidedAt, &r.RefundTxnID, &r.CreatedAt, &r.ExecutionRefundID, &r.ExecutionStatus, &r.ExecutionError); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetTicketStatus is the admin/moderator transition. resolved_at is
// stamped when status moves to 'resolved' or 'closed'.
func (s *Store) SetTicketStatus(ctx context.Context, adminID, ticketID uuid.UUID, status string) error {
	allowed := map[string]bool{"open": true, "in_progress": true, "resolved": true, "closed": true, "cancelled": true}
	if !allowed[status] {
		return fmt.Errorf("invalid ticket status: %s", status)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	tag, err := tx.Exec(ctx, `
		UPDATE food.support_tickets
		SET status = $2::food.ticket_status,
			resolved_at = CASE
				WHEN $2 IN ('resolved', 'closed') THEN COALESCE(resolved_at, NOW())
				ELSE resolved_at
			END
		WHERE id = $1
	`, ticketID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err := writeAdminAudit(ctx, tx, adminID, "ticket.status", "support_ticket", &ticketID, map[string]any{
		"status": status,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateRefundRequest inserts a customer-requested refund. The admin
// later approves/rejects it via DecideRefund.
func (s *Store) CreateRefundRequest(ctx context.Context, customerID, orderID uuid.UUID, ticketID *uuid.UUID, amount float64, reason string) (*RefundRequest, error) {
	minor, err := refundAmountMinor(amount)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if _, err := tx.Exec(ctx, `SELECT id FROM food.orders WHERE id=$1 FOR UPDATE`, orderID); err != nil {
		return nil, err
	}
	var owner uuid.UUID
	var remaining int64
	var paymentStatus string
	if err := tx.QueryRow(ctx, `SELECT user_id, payment_status::text, COALESCE(final_amount_paise,ROUND(final_amount*100)::bigint)
		- COALESCE((SELECT SUM(ROUND(amount*100)::bigint) FROM food.refunds WHERE order_id=o.id AND status IN ('PENDING','SUBMITTED','PROCESSED')),0)::bigint
		- COALESCE((SELECT SUM(ROUND(amount*100)::bigint) FROM food.refund_requests WHERE order_id=o.id AND status='requested'),0)::bigint
		FROM food.orders o WHERE id=$1 FOR UPDATE`, orderID).Scan(&owner, &paymentStatus, &remaining); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSupportOrderForbidden
		}
		return nil, err
	}
	if owner != customerID {
		return nil, ErrSupportOrderForbidden
	}
	if paymentStatus != "CAPTURED" && paymentStatus != "PARTIALLY_REFUNDED" {
		return nil, ErrRefundNotEligible
	}
	if minor > remaining {
		return nil, ErrInvalidRefundAmount
	}
	if ticketID != nil {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.support_tickets WHERE id=$1 AND customer_id=$2 AND order_id=$3)`, *ticketID, customerID, orderID).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrSupportOrderForbidden
		}
	}
	var r RefundRequest
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.refund_requests (ticket_id, order_id, customer_id, amount, reason)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		RETURNING id, ticket_id, order_id, customer_id, amount::float8, reason, status::text,
			decided_by, decided_at::text, refund_txn_id, created_at::text
	`, ticketID, orderID, customerID, amount, reason).Scan(
		&r.ID, &r.TicketID, &r.OrderID, &r.CustomerID, &r.Amount, &r.Reason,
		&r.Status, &r.DecidedBy, &r.DecidedAt, &r.RefundTxnID, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	if err := enqueueSupportRefundDecisionTx(ctx, tx, r.ID, orderID, customerID, "requested"); err != nil {
		return nil, err
	}
	return &r, tx.Commit(ctx)
}

// DecideRefund atomically records an executable refund for each approval.
// Only an authoritative payment event sets a linked request to processed.
func (s *Store) DecideRefund(ctx context.Context, adminID, refundID uuid.UUID, status, reason string) error {
	if status != "approved" && status != "rejected" {
		return fmt.Errorf("invalid refund decision: %s", status)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT order_id FROM food.refund_requests WHERE id=$1`, refundID).Scan(&orderID); err != nil {
		return err
	}
	// Consistent lock order with payment-event reconciliation and new requests.
	if _, err := tx.Exec(ctx, `SELECT id FROM food.orders WHERE id=$1 FOR UPDATE`, orderID); err != nil {
		return err
	}
	var current string
	var amount float64
	var customerID uuid.UUID
	var executionID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status::text,amount::float8,customer_id,execution_refund_id FROM food.refund_requests WHERE id=$1 FOR UPDATE`, refundID).Scan(&current, &amount, &customerID, &executionID); err != nil {
		return err
	}
	if current != "requested" && !(current == "approved" && status == "approved" && executionID == nil) {
		if current == status || (current == "processed" && status == "approved") {
			return tx.Commit(ctx)
		}
		return ErrRefundDecisionConflict
	}
	if status == "approved" {
		// Historical support requests must pass the same ownership and remaining
		// balance checks; do not link another refund already in flight.
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT o.user_id=$2 AND NOT EXISTS(SELECT 1 FROM food.refunds WHERE order_id=o.id AND status IN ('PENDING','SUBMITTED'))
			AND (q.ticket_id IS NULL OR EXISTS(SELECT 1 FROM food.support_tickets t WHERE t.id=q.ticket_id AND t.customer_id=$2 AND t.order_id=o.id))
			FROM food.orders o JOIN food.refund_requests q ON q.order_id=o.id WHERE o.id=$1 AND q.id=$3`, orderID, customerID, refundID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrRefundNotEligible
		}
		if _, err := refundAmountMinor(amount); err != nil {
			return err
		}
		plan, err := s.adminRequestRefundTx(ctx, tx, adminID, orderID, reason, amount, "support-refund:"+refundID.String())
		if err != nil {
			return err
		}
		executionID = &plan.RefundID
	}
	tag, err := tx.Exec(ctx, `
		UPDATE food.refund_requests
		SET status = $2::food.refund_status,
			decided_by = $3,
			decided_at = NOW(),
			reason = COALESCE(NULLIF($4, ''), reason), execution_refund_id=$5
		WHERE id = $1 AND (status = 'requested' OR (status='approved' AND execution_refund_id IS NULL))
	`, refundID, status, adminID, reason, executionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err := writeAdminAudit(ctx, tx, adminID, "refund_request.decide", "refund_request", &refundID, map[string]any{
		"status": status, "reason": reason,
	}); err != nil {
		return err
	}
	if err := enqueueSupportRefundDecisionTx(ctx, tx, refundID, orderID, customerID, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkRefundProcessed is called by the payments worker once the actual
// refund settles.
func (s *Store) MarkRefundProcessed(ctx context.Context, refundID uuid.UUID, refundTxnID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.refund_requests q
		SET status = 'processed', refund_txn_id = $2
		WHERE id = $1 AND status = 'approved' AND EXISTS(
		 SELECT 1 FROM food.refunds r WHERE r.id=q.execution_refund_id AND r.status='PROCESSED')
	`, refundID, refundTxnID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
