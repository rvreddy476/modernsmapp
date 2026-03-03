package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

// CreateIntent creates a new payment intent. Idempotent on idempotency_key.
func (s *Store) CreateIntent(ctx context.Context, in PaymentIntent) (*PaymentIntent, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	in.ID = uuid.New()
	err = tx.QueryRow(ctx,
		`INSERT INTO payments.payment_intents
		 (id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status, idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9)
		 ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = NOW()
		 RETURNING id, payer_id, payee_id, reference_type, reference_id, amount, currency, method, status, idempotency_key, created_at, updated_at`,
		in.ID, in.PayerID, in.PayeeID, in.ReferenceType, in.ReferenceID,
		in.Amount, in.Currency, in.Method, in.IdempotencyKey,
	).Scan(&in.ID, &in.PayerID, &in.PayeeID, &in.ReferenceType, &in.ReferenceID,
		&in.Amount, &in.Currency, &in.Method, &in.Status, &in.IdempotencyKey,
		&in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return nil, err
	}

	// Write audit entry in same transaction
	_, err = tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, new_status, actor_id) VALUES ($1,'initiated','pending',$2)`,
		in.ID, in.PayerID,
	)
	if err != nil {
		return nil, err
	}

	return &in, tx.Commit(ctx)
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

	_, err = tx.Exec(ctx,
		`UPDATE payments.payment_intents
		 SET status = $1, provider_ref = COALESCE(NULLIF($2,''), provider_ref), updated_at = NOW()
		 WHERE id = $3 AND status = $4`,
		newStatus, providerRef, id, oldStatus,
	)
	if err != nil {
		return err
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

// ensure pgx import is used
var _ pgx.Tx
