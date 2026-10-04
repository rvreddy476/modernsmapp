package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Refunds: a row is written with the state change that owes the money
// (status requested, a deterministic key per booking and cause), submitted
// to payments-service after commit, and resubmitted by the worker until
// payments accepts it (pending). Only payment.refunded makes it succeeded.

// ErrExceedsPaid: the refund is more than was captured minus refunds that
// have not failed.
var ErrExceedsPaid = errors.New("store: refund exceeds what is refundable")

// RefundJob is a refund to submit.
type RefundJob struct {
	ID          uuid.UUID
	BookingID   uuid.UUID
	PaymentID   uuid.UUID
	IntentID    *string
	Cause       string
	Key         string
	AmountPaise int64
	Status      string
	Attempts    int
	// ReferenceType is the payment's (doorstep_booking or doorstep_extras):
	// the refund goes to payments-service under that reference.
	ReferenceType string
}

const refundJobCols = `r.id, r.booking_id, r.payment_id, p.payments_intent_id, r.cause, r.idempotency_key, r.amount_paise, r.status, r.attempts,
	p.reference_type`

func scanRefundJob(r pgx.Row) (RefundJob, error) {
	var j RefundJob
	err := r.Scan(&j.ID, &j.BookingID, &j.PaymentID, &j.IntentID, &j.Cause, &j.Key, &j.AmountPaise, &j.Status, &j.Attempts,
		&j.ReferenceType)
	return j, err
}

// RefundJob reads one refund for submission.
func (s *Store) RefundJob(ctx context.Context, id uuid.UUID) (*RefundJob, error) {
	j, err := scanRefundJob(s.db.QueryRow(ctx, `SELECT `+refundJobCols+`
		FROM doorstep.refunds r JOIN doorstep.payments p ON p.id = r.payment_id WHERE r.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return &j, nil
}

// UnsubmittedRefunds lists refunds payments never accepted whose next
// attempt is due, oldest first.
func (s *Store) UnsubmittedRefunds(ctx context.Context, limit int) ([]RefundJob, error) {
	rows, err := s.db.Query(ctx, `SELECT `+refundJobCols+`
		FROM doorstep.refunds r JOIN doorstep.payments p ON p.id = r.payment_id
		WHERE r.status = 'requested' AND r.next_attempt_at <= $1
		ORDER BY r.next_attempt_at, r.id LIMIT $2`, s.clock(), limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (RefundJob, error) { return scanRefundJob(r) })
}

// MarkRefundSubmitted records payments' acceptance (requested -> pending).
func (s *Store) MarkRefundSubmitted(ctx context.Context, id uuid.UUID, commandID string) error {
	_, err := s.db.Exec(ctx, `UPDATE doorstep.refunds SET status = 'pending', payments_refund_id = COALESCE(payments_refund_id, NULLIF($2, '')),
		attempts = attempts + 1, last_error = NULL, updated_at = $3 WHERE id = $1 AND status = 'requested'`, id, commandID, s.clock())
	return err
}

// MarkRefundAttempt records a failed submission. terminal (payments refused
// the request) fails the row and flags the booking; otherwise it stays
// requested and is retried at next.
func (s *Store) MarkRefundAttempt(ctx context.Context, id uuid.UUID, lastErr string, terminal bool, next time.Time) error {
	at := s.clock()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	status := "requested"
	if terminal {
		status = "failed"
	}
	var booking uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE doorstep.refunds SET status = $2, attempts = attempts + 1, last_error = $3, next_attempt_at = $4,
		updated_at = $5 WHERE id = $1 AND status = 'requested' RETURNING booking_id`, id, status, truncateText(lastErr, 300), next, at).Scan(&booking)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if terminal {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET needs_attention = TRUE, attention_reason = $2, updated_at = $3 WHERE id = $1`,
			booking, "refund refused by payments-service: "+truncateText(lastErr, 200), at); err != nil {
			return err
		}
	}
	return mapErr(tx.Commit(ctx))
}

// AdminRefundRequest is an ops refund.
type AdminRefundRequest struct {
	BookingID   uuid.UUID
	AmountPaise int64
	Reason      string
	Cause       string // admin_<hash of the forwarded Idempotency-Key>
	Key         string
}

// AdminRequestRefund writes an ops refund row and its audit row in one
// transaction. The same key replays the existing row (created false). The
// amount must not exceed captured minus every refund that has not failed
// (ErrExceedsPaid).
func (s *Store) AdminRequestRefund(ctx context.Context, a Actor, in AdminRefundRequest) (*model.Refund, bool, error) {
	at := s.clock()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := lockBookingTx(ctx, tx, in.BookingID, nil)
	if err != nil {
		return nil, false, err
	}
	var existing model.Refund
	err = tx.QueryRow(ctx, `SELECT id, payment_id, cause, amount_paise, status, created_at FROM doorstep.refunds WHERE idempotency_key = $1`,
		in.Key).Scan(&existing.ID, &existing.PaymentID, &existing.Cause, &existing.AmountPaise, &existing.Status, &existing.CreatedAt)
	if err == nil {
		existing.CreatedAt = existing.CreatedAt.UTC()
		return &existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	if b.PaymentID == nil || in.AmountPaise > b.Refundable {
		return nil, false, ErrExceedsPaid
	}
	// An ops refund is made on the booking payment: never more than that
	// payment still has (a paid change-of-professional difference is its own
	// payment, B1).
	var left int64
	if err := tx.QueryRow(ctx, `SELECT p.amount_paise - COALESCE((SELECT sum(r.amount_paise) FROM doorstep.refunds r
		WHERE r.payment_id = p.id AND r.status <> 'failed'), 0) FROM doorstep.payments p WHERE p.id = $1`, *b.PaymentID).Scan(&left); err != nil {
		return nil, false, err
	}
	if in.AmountPaise > left {
		return nil, false, ErrExceedsPaid
	}
	rid, err := insertRefundTx(ctx, tx, *b.PaymentID, in.BookingID, in.Cause, in.Key, in.AmountPaise, &a.UserID, at)
	if err != nil {
		return nil, false, err
	}
	if err := auditTx(ctx, tx, a, "booking.refund", "booking", in.BookingID.String(), map[string]any{
		"refund_id": rid, "amount_paise": in.AmountPaise, "reason": in.Reason, "refundable_before_paise": b.Refundable}); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, mapErr(err)
	}
	return &model.Refund{ID: rid, PaymentID: *b.PaymentID, Cause: in.Cause, AmountPaise: in.AmountPaise, Status: "requested",
		CreatedAt: at.UTC()}, true, nil
}

// shortHash is a stable 16-hex-character digest (refund causes derived from
// a caller's key or an event id; never the raw value).
func shortHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:8])
}

// ShortHash exposes shortHash to the service (admin refund causes).
func ShortHash(v string) string { return shortHash(v) }
