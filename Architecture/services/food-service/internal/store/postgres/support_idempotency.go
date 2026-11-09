package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RefundRequestInput is POST /v1/food/refunds.
type RefundRequestInput struct {
	CustomerID uuid.UUID
	OrderID    uuid.UUID
	TicketID   *uuid.UUID
	Amount     float64 // rupees, whole paise (refundAmountMinor)
	Reason     string
	// IdempotencyKey is the optional Idempotency-Key header.
	IdempotencyKey string
}

// replayedEntityID reads the id a completed support or refund request stored
// under its Idempotency-Key. A row without one belongs to another kind of
// request, so the key is refused as reused, never answered with a guess.
func replayedEntityID(body map[string]any) (uuid.UUID, error) {
	raw, ok := body["id"].(string)
	if !ok {
		return uuid.Nil, errGenericIdempotencyMismatch
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errGenericIdempotencyMismatch
	}
	return id, nil
}

// ownTicketTx is a ticket the customer opened; anyone else's reads as reused.
func ownTicketTx(ctx context.Context, tx pgx.Tx, ticketID, customerID uuid.UUID) (*Ticket, error) {
	var t Ticket
	err := tx.QueryRow(ctx, `
		SELECT id, customer_id, order_id, category, subject, detail, status::text,
			assigned_to, resolved_at::text, created_at::text
		FROM food.support_tickets WHERE id = $1 AND customer_id = $2
	`, ticketID, customerID).Scan(&t.ID, &t.CustomerID, &t.OrderID, &t.Category, &t.Subject,
		&t.Detail, &t.Status, &t.AssignedTo, &t.ResolvedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errGenericIdempotencyMismatch
	}
	if err != nil {
		return nil, fmt.Errorf("replay ticket: %w", err)
	}
	return &t, nil
}

// ownRefundRequestTx is a refund request the customer made, as it stands now
// (it may have been decided since the first answer).
func ownRefundRequestTx(ctx context.Context, tx pgx.Tx, requestID, customerID uuid.UUID) (*RefundRequest, error) {
	var r RefundRequest
	err := tx.QueryRow(ctx, `
		SELECT id, ticket_id, order_id, customer_id, amount::float8, reason, status::text,
			decided_by, decided_at::text, refund_txn_id, created_at::text
		FROM food.refund_requests WHERE id = $1 AND customer_id = $2
	`, requestID, customerID).Scan(&r.ID, &r.TicketID, &r.OrderID, &r.CustomerID, &r.Amount, &r.Reason,
		&r.Status, &r.DecidedBy, &r.DecidedAt, &r.RefundTxnID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errGenericIdempotencyMismatch
	}
	if err != nil {
		return nil, fmt.Errorf("replay refund request: %w", err)
	}
	return &r, nil
}
