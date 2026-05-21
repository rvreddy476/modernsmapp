package postgres

import (
	"context"
	"fmt"

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
	ID          uuid.UUID  `json:"id"`
	TicketID    *uuid.UUID `json:"ticket_id,omitempty"`
	OrderID     uuid.UUID  `json:"order_id"`
	CustomerID  uuid.UUID  `json:"customer_id"`
	Amount      float64    `json:"amount"`
	Reason      *string    `json:"reason,omitempty"`
	Status      string     `json:"status"`
	DecidedBy   *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt   *string    `json:"decided_at,omitempty"`
	RefundTxnID *string    `json:"refund_txn_id,omitempty"`
	CreatedAt   string     `json:"created_at"`
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

// SetTicketStatus is the admin/moderator transition. resolved_at is
// stamped when status moves to 'resolved' or 'closed'.
func (s *Store) SetTicketStatus(ctx context.Context, ticketID uuid.UUID, status string) error {
	allowed := map[string]bool{"open": true, "in_progress": true, "resolved": true, "closed": true, "cancelled": true}
	if !allowed[status] {
		return fmt.Errorf("invalid ticket status: %s", status)
	}
	tag, err := s.db.Exec(ctx, `
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
	return nil
}

// CreateRefundRequest inserts a customer-requested refund. The admin
// later approves/rejects it via DecideRefund.
func (s *Store) CreateRefundRequest(ctx context.Context, customerID, orderID uuid.UUID, ticketID *uuid.UUID, amount float64, reason string) (*RefundRequest, error) {
	var r RefundRequest
	if err := s.db.QueryRow(ctx, `
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
	return &r, nil
}

// DecideRefund is the admin path: status must be 'approved' or
// 'rejected'. Once approved, the payments-service async worker
// processes the actual refund and sets status='processed' via
// MarkRefundProcessed.
func (s *Store) DecideRefund(ctx context.Context, adminID, refundID uuid.UUID, status, reason string) error {
	if status != "approved" && status != "rejected" {
		return fmt.Errorf("invalid refund decision: %s", status)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE food.refund_requests
		SET status = $2::food.refund_status,
			decided_by = $3,
			decided_at = NOW(),
			reason = COALESCE(NULLIF($4, ''), reason)
		WHERE id = $1 AND status = 'requested'
	`, refundID, status, adminID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MarkRefundProcessed is called by the payments worker once the actual
// refund settles.
func (s *Store) MarkRefundProcessed(ctx context.Context, refundID uuid.UUID, refundTxnID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.refund_requests
		SET status = 'processed', refund_txn_id = $2
		WHERE id = $1 AND status = 'approved'
	`, refundID, refundTxnID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
