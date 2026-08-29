package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PaymentReconciliationItem is one row in rider_payment_reconciliation.
type PaymentReconciliationItem struct {
	ID              uuid.UUID  `json:"id"`
	RideID          uuid.UUID  `json:"ride_id"`
	PaymentID       *uuid.UUID `json:"payment_id,omitempty"`
	ObservedStatus  string     `json:"observed_status"`
	CanonicalStatus string     `json:"canonical_status"`
	AttemptCount    int        `json:"attempt_count"`
	NextRetryAt     time.Time  `json:"next_retry_at"`
	TerminalReason  *string    `json:"terminal_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// RecordPaymentReconciliation queues or updates a payment reconciliation row.
func (s *Store) RecordPaymentReconciliation(ctx context.Context, rideID uuid.UUID, paymentID *uuid.UUID, observed, canonical string, nextRetry time.Time, reason *string) error {
	const q = `
        INSERT INTO rider_payment_reconciliation (
            ride_id, payment_id, observed_status, canonical_status, next_retry_at, terminal_reason, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, NOW()
        )`
	_, err := s.db.Exec(ctx, q, rideID, paymentID, observed, canonical, nextRetry, reason)
	if err != nil {
		return fmt.Errorf("record payment reconciliation: %w", err)
	}
	return nil
}
