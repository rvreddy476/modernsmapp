package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotRefundable is returned when a payment intent is not in a refundable state.
var ErrNotRefundable = errors.New("payment intent is not in a refundable state")

// ErrPaymentNotFound is returned when a payment intent does not exist.
var ErrPaymentNotFound = errors.New("payment intent not found")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

type PaymentIntent struct {
	ID             uuid.UUID `json:"id"`
	PayerID        uuid.UUID `json:"payer_id"`
	PayeeID        uuid.UUID `json:"payee_id"`
	ReferenceType  string    `json:"reference_type"`
	ReferenceID    uuid.UUID `json:"reference_id"`
	// Amount is the original payment amount in rupees-major. NOTE:
	// audit P7 followup — this is still float64 at the row + JSON
	// boundary. Refund correctness now uses AmountMinor() + the int64
	// RefundedAmountMinor column below; a follow-up migration should
	// convert this column to int64 paise and rename to amount_minor.
	Amount              float64   `json:"amount"`
	Currency            string    `json:"currency"`
	Method              string    `json:"method"`
	Status              string    `json:"status"`
	ProviderRef         string    `json:"provider_ref,omitempty"`
	UPIIntentURL        string    `json:"upi_intent_url,omitempty"`
	IdempotencyKey      string    `json:"idempotency_key"`
	RefundedAmountMinor int64     `json:"refunded_amount_minor"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// AmountMinor returns the intent amount converted to paise-minor int64.
// The math.Round is intentional — `int64(amount*100)` truncates ₹100.50
// to 10049 paise (IEEE-754 representation of 100.5 → 100.499999...).
// Pin behavior with the rupeesToPaise pin in the service test.
func (p *PaymentIntent) AmountMinor() int64 {
	return int64(math.Round(p.Amount * 100))
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	IntentID  uuid.UUID `json:"intent_id"`
	Event     string    `json:"event"`
	OldStatus string    `json:"old_status,omitempty"`
	NewStatus string    `json:"new_status,omitempty"`
	ActorID   uuid.UUID `json:"actor_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateIntentResult is returned by CreateIntent, carrying the intent and
// a flag indicating whether the record already existed (idempotent replay).
type CreateIntentResult struct {
	Intent      *PaymentIntent
	WasExisting bool // true if idempotency_key already existed
}

// CreateIntent creates a new payment intent. Idempotent on idempotency_key.
// If the key already exists the existing row is returned and WasExisting is set to true.
func (s *Store) CreateIntent(ctx context.Context, in PaymentIntent) (*CreateIntentResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	err = tx.QueryRow(ctx,
		`INSERT INTO payments.payment_intents
		    (payer_id, payee_id, reference_type, reference_id, amount, currency, method, status, provider_ref, idempotency_key, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9, $10)
		 ON CONFLICT (idempotency_key)
		 DO UPDATE SET updated_at = payments.payment_intents.updated_at
		 RETURNING id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status,
		           idempotency_key, COALESCE(provider_ref,''), created_at, updated_at`,
		in.PayerID, in.PayeeID, in.ReferenceType, in.ReferenceID,
		in.Amount, in.Currency, in.Method, in.ProviderRef, in.IdempotencyKey, "{}",
	).Scan(&in.ID, &in.PayerID, &in.PayeeID, &in.ReferenceType, &in.ReferenceID,
		&in.Amount, &in.Currency, &in.Method, &in.Status, &in.IdempotencyKey,
		&in.ProviderRef, &in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return nil, err
	}

	result := &CreateIntentResult{
		Intent:      &in,
		WasExisting: time.Since(in.CreatedAt) > time.Second,
	}

	// Only write audit entry for genuinely new intents
	if !result.WasExisting {
		_, err = tx.Exec(ctx,
			`INSERT INTO payments.payment_audit_log (intent_id, event, new_status, actor_id) VALUES ($1,'initiated','pending',$2)`,
			in.ID, in.PayerID,
		)
		if err != nil {
			return nil, err
		}
	}

	return result, tx.Commit(ctx)
}

// GetIntent fetches a payment intent by ID.
func (s *Store) GetIntent(ctx context.Context, id uuid.UUID) (*PaymentIntent, error) {
	var p PaymentIntent
	err := s.db.QueryRow(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		        COALESCE(refunded_amount_minor, 0), created_at, updated_at
		 FROM payments.payment_intents WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
		&p.Amount, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
		&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt)
	return &p, err
}

// GetIntentByProviderRef is used by the webhook-publish path so events
// can carry the full intent shape (reference_type / reference_id) that
// commerce-service consumers need. Returns nil + ErrPaymentNotFound
// when the provider hasn't been wired up against any intent yet.
func (s *Store) GetIntentByProviderRef(ctx context.Context, providerRef string) (*PaymentIntent, error) {
	if providerRef == "" {
		return nil, ErrPaymentNotFound
	}
	var p PaymentIntent
	err := s.db.QueryRow(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		        COALESCE(refunded_amount_minor, 0), created_at, updated_at
		 FROM payments.payment_intents WHERE provider_ref = $1
		 ORDER BY updated_at DESC LIMIT 1`,
		providerRef,
	).Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
		&p.Amount, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
		&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateStatus atomically updates status and writes an audit entry.
func (s *Store) UpdateStatus(ctx context.Context, id uuid.UUID, oldStatus, newStatus, providerRef string, actorID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	result, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		 SET status = $1, provider_ref = COALESCE(NULLIF($2,''), provider_ref), updated_at = NOW()
		 WHERE id = $3 AND status = $4`,
		newStatus, providerRef, id, oldStatus,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrPaymentNotFound
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, actor_id) VALUES ($1,'status_changed',$2,$3,$4)`,
		id, oldStatus, newStatus, actorID,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// InitiateRefund atomically transitions a payment intent from 'succeeded' to 'refunded'.
// Returns ErrNotRefundable if the intent is not found or not in 'succeeded' status.
//
// Deprecated: superseded by ApplyRefund, which accepts a paise-minor
// refund amount and bookkeeps the partial-refund running total. Kept on
// the type only for the (unlikely) external caller; the in-tree service
// path uses ApplyRefund.
func (s *Store) InitiateRefund(ctx context.Context, intentID uuid.UUID) error {
	result, err := s.db.Exec(ctx,
		`UPDATE payments.payment_intents
		 SET status = 'refunded', updated_at = NOW()
		 WHERE id = $1 AND status = 'succeeded'`,
		intentID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotRefundable
	}
	return nil
}

// ApplyRefund atomically books a refund of `amountMinor` paise against
// the intent. Audit P6 + P7:
//
//   - amountMinor must satisfy 0 < amountMinor <= remaining (caller
//     pre-validates; this method enforces again at the DB layer with
//     a WHERE clause so concurrent refunds can't oversubscribe).
//   - The new total = refunded_amount_minor + amountMinor.
//   - If new total >= intent_amount_minor, status flips to 'refunded';
//     otherwise to 'partially_refunded'.
//
// The status transition is checked against validStatusTransitions
// (allowed: succeeded → refunded/partially_refunded, partially_refunded
// → refunded/partially_refunded). The audit row records the running
// refunded_amount_minor for traceability.
//
// Returns ErrNotRefundable when the row isn't in a refundable state or
// when the requested amount exceeds the remaining refundable balance
// (the WHERE clause filters both).
func (s *Store) ApplyRefund(ctx context.Context, intentID uuid.UUID, amountMinor, intentAmountMinor int64, actorID uuid.UUID) (newStatus string, newRefundedMinor int64, err error) {
	if amountMinor <= 0 {
		return "", 0, fmt.Errorf("refund amount must be positive")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Atomic update: only succeed when (a) intent is in a refundable
	// state, (b) the running total stays <= the intent amount. The CASE
	// flips status to 'refunded' when fully covered, otherwise
	// 'partially_refunded'. RETURNING gives us the new status + running
	// total so the service can publish the right event without a re-read.
	err = tx.QueryRow(ctx, `
		UPDATE payments.payment_intents
		   SET refunded_amount_minor = refunded_amount_minor + $2,
		       status = CASE
		                   WHEN refunded_amount_minor + $2 >= $3 THEN 'refunded'
		                   ELSE 'partially_refunded'
		                END,
		       updated_at = NOW()
		 WHERE id = $1
		   AND status IN ('succeeded', 'partially_refunded')
		   AND refunded_amount_minor + $2 <= $3
		 RETURNING status, refunded_amount_minor
	`, intentID, amountMinor, intentAmountMinor).Scan(&newStatus, &newRefundedMinor)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrNotRefundable
		}
		return "", 0, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, actor_id, metadata)
		 VALUES ($1, 'refund_applied', 'succeeded', $2, $3, jsonb_build_object('amount_minor', $4::bigint, 'refunded_total_minor', $5::bigint))`,
		intentID, newStatus, actorID, amountMinor, newRefundedMinor,
	)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return newStatus, newRefundedMinor, nil
}

// RecordRefundIfFresh inserts a row into payments.refunds_applied if
// the refund_provider_ref hasn't been seen before. Returns true when
// the insert actually happened (caller proceeds to ApplyRefund) and
// false when ON CONFLICT short-circuited (caller skips — the refund
// was already applied by an earlier webhook delivery).
//
// This is the refund-level idempotency layer. The webhook_events
// dedup catches identical event_ids, but Razorpay can re-deliver the
// same refund with a fresh event_id (manual replay, queue re-issue),
// so we key on the refund id itself.
func (s *Store) RecordRefundIfFresh(ctx context.Context, refundProviderRef string, intentID uuid.UUID, amountMinor int64) (bool, error) {
	if refundProviderRef == "" {
		return false, fmt.Errorf("refund_provider_ref must be non-empty")
	}
	if amountMinor <= 0 {
		return false, fmt.Errorf("amount_minor must be positive")
	}
	tag, err := s.db.Exec(ctx,
		`INSERT INTO payments.refunds_applied (refund_provider_ref, intent_id, amount_minor)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (refund_provider_ref) DO NOTHING`,
		refundProviderRef, intentID, amountMinor,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListByReference returns payment intents for a given reference, capped
// at 100 most-recent rows. HP4: prior version had no LIMIT — an order
// with many retried/failed intent attempts could pull an unbounded set
// into memory and back through the API envelope. Callers want the
// latest attempts anyway (status-display + refund-locator); 100 is
// well past any real-world tail.
func (s *Store) ListByReference(ctx context.Context, refType string, refID uuid.UUID) ([]PaymentIntent, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		        COALESCE(refunded_amount_minor, 0), created_at, updated_at
		 FROM payments.payment_intents
		 WHERE reference_type = $1 AND reference_id = $2
		 ORDER BY created_at DESC
		 LIMIT 100`,
		refType, refID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []PaymentIntent
	for rows.Next() {
		var p PaymentIntent
		if err := rows.Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
			&p.Amount, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
			&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		intents = append(intents, p)
	}
	return intents, rows.Err()
}

// validStatusTransitions encodes the allowed state-machine edges for
// payment_intents. Audit P2: previously UpdateStatusByProviderRef did
// an unconditional UPDATE — a late payment.captured webhook could flip
// `refunded` back to `succeeded`, and a duplicate refund.processed
// could flip `succeeded` → `refunded` → `succeeded` via reordering.
//
// `pending` is the initial state; terminal states (failed, refunded,
// cancelled) accept no further transitions. `succeeded` only moves
// forward to refunded.
var validStatusTransitions = map[string]map[string]bool{
	"pending": {
		"processing": true, "succeeded": true, "failed": true, "cancelled": true,
	},
	"processing": {
		"succeeded": true, "failed": true,
	},
	"succeeded": {
		// Audit P6: partial refunds open a `partially_refunded` middle
		// state. From there a follow-up refund can push to `refunded`
		// (full) or stay partial by accumulating in refunded_amount_minor.
		"refunded": true, "partially_refunded": true,
	},
	"partially_refunded": {
		"refunded": true, "partially_refunded": true,
	},
	"failed":    {},
	"refunded":  {},
	"cancelled": {},
}

// ErrInvalidStatusTransition is returned when a status update would
// violate the payment-intent state machine.
var ErrInvalidStatusTransition = errors.New("invalid payment status transition")

// UpdateStatusByProviderRef updates the status of an intent matched by
// its provider_ref (gateway order ID). Returns ErrPaymentNotFound when
// no intent matches, ErrInvalidStatusTransition when the requested
// transition is forbidden by the state machine. The transition is
// applied atomically inside a single UPDATE so two concurrent webhook
// retries can't both succeed.
func (s *Store) UpdateStatusByProviderRef(ctx context.Context, providerRef, newStatus, paymentID string) error {
	// Build the allowed-current-status list for newStatus.
	allowedCurrent := make([]string, 0, 4)
	for from, edges := range validStatusTransitions {
		if edges[newStatus] {
			allowedCurrent = append(allowedCurrent, from)
		}
	}
	if len(allowedCurrent) == 0 {
		return fmt.Errorf("%w: unknown target status %q", ErrInvalidStatusTransition, newStatus)
	}

	cmd, err := s.db.Exec(ctx, `
		UPDATE payments.payment_intents
		SET status = $1,
		    provider_ref = CASE WHEN $2 <> '' THEN $2 ELSE provider_ref END,
		    updated_at = NOW()
		WHERE provider_ref = $3 AND status = ANY($4)
	`, newStatus, paymentID, providerRef, allowedCurrent)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		// Distinguish "no such intent" from "invalid transition" so the
		// caller (handler) can return the right error to the caller.
		var existsCount int
		_ = s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM payments.payment_intents WHERE provider_ref = $1`,
			providerRef).Scan(&existsCount)
		if existsCount == 0 {
			return ErrPaymentNotFound
		}
		return ErrInvalidStatusTransition
	}
	return nil
}

// RecordWebhookEventIfNew inserts a row into payments.webhook_events
// returning true iff the event_id is new. Audit P3: Razorpay retries
// webhooks aggressively; previously every retry re-ran the status
// update and re-published a Kafka event. Now the handler can short-
// circuit duplicates with a single SELECT-INSERT.
func (s *Store) RecordWebhookEventIfNew(ctx context.Context, eventID, eventType, providerRef string) (bool, error) {
	if eventID == "" {
		// No event_id in the payload → can't dedup. Caller treats as new.
		return true, nil
	}
	cmd, err := s.db.Exec(ctx, `
		INSERT INTO payments.webhook_events (event_id, event_type, provider_ref, received_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, eventType, providerRef)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() == 1, nil
}

// CreateHold creates a payment hold record for an escrow payment.
func (s *Store) CreateHold(ctx context.Context, intentID uuid.UUID, amount int64, currency, condition string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO payments.payment_holds (payment_intent_id, hold_amount, currency, release_condition)
		VALUES ($1, $2, $3, $4)
	`, intentID, amount, currency, condition)
	return err
}

// ReleaseHold marks a payment hold as released.
func (s *Store) ReleaseHold(ctx context.Context, intentID uuid.UUID, releasedBy string) error {
	result, err := s.db.Exec(ctx, `
		UPDATE payments.payment_holds
		SET released_at = NOW(), released_by = $2
		WHERE payment_intent_id = $1 AND released_at IS NULL
	`, intentID, releasedBy)
	if err != nil {
		return err
	}
	if n := result.RowsAffected(); n == 0 {
		return fmt.Errorf("no active hold found for intent %s", intentID)
	}
	return nil
}

// ensure pgx import is used
var _ pgx.Tx
