package postgres

import (
	"context"
	"errors"
	"fmt"
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
	ID             uuid.UUID  `json:"id"`
	PayerID        uuid.UUID  `json:"payer_id"`
	PayeeID        uuid.UUID  `json:"payee_id"`
	ReferenceType  string     `json:"reference_type"`
	ReferenceID    uuid.UUID  `json:"reference_id"`
	Amount         float64    `json:"amount"`
	Currency       string     `json:"currency"`
	Method         string     `json:"method"`
	Status         string     `json:"status"`
	ProviderRef    string     `json:"provider_ref,omitempty"`
	UPIIntentURL   string     `json:"upi_intent_url,omitempty"`
	IdempotencyKey string     `json:"idempotency_key"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
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
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key, created_at, updated_at
		 FROM payments.payment_intents WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
		&p.Amount, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
		&p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt)
	return &p, err
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

// ListByReference returns payment intents for a given reference.
func (s *Store) ListByReference(ctx context.Context, refType string, refID uuid.UUID) ([]PaymentIntent, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key, created_at, updated_at
		 FROM payments.payment_intents
		 WHERE reference_type = $1 AND reference_id = $2
		 ORDER BY created_at DESC`,
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
			&p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		intents = append(intents, p)
	}
	return intents, rows.Err()
}

// UpdateStatusByProviderRef updates the status of an intent matched by its provider_ref (gateway order ID).
func (s *Store) UpdateStatusByProviderRef(ctx context.Context, providerRef, newStatus, paymentID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payments.payment_intents
		SET status = $1,
		    provider_ref = CASE WHEN $2 <> '' THEN $2 ELSE provider_ref END,
		    updated_at = NOW()
		WHERE provider_ref = $3
	`, newStatus, paymentID, providerRef)
	return err
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
