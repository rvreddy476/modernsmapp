// Phase 6.1 — service-side handlers for fulfillment_jobs. These are
// the resilient replacements for the old `go s.fulfillPaidOrder(orderID)`
// fire-and-forget goroutines: invoked by the worker after claiming a row
// from the queue, return an error so the worker can decide retry vs
// dead-letter.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// EnqueueFulfillPaidOrder appends a fulfill_paid_order job to the queue.
// Caller has already persisted the order — this is a separate write so
// it's safe to fail (worst case: an admin replays from the dead-letter
// queue).
func (s *Service) EnqueueFulfillPaidOrder(ctx context.Context, orderID uuid.UUID) {
	payload, _ := json.Marshal(map[string]any{"order_id": orderID})
	if err := s.store.EnqueueJobPool(ctx, "fulfill_paid_order", payload); err != nil {
		slog.Error("enqueue fulfill_paid_order failed",
			"order_id", orderID, "error", err)
	}
}

// EnqueueProcessReturnApproved is the resilient analogue for return
// refunds. Phase 6.1 — used by ApproveReturn after the DB write commits.
func (s *Service) EnqueueProcessReturnApproved(ctx context.Context, returnID uuid.UUID) {
	payload, _ := json.Marshal(map[string]any{"return_id": returnID})
	if err := s.store.EnqueueJobPool(ctx, "process_return_approved", payload); err != nil {
		slog.Error("enqueue process_return_approved failed",
			"return_id", returnID, "error", err)
	}
}

// FulfillPaidOrderJob is the idempotent handler the worker invokes. The
// underlying IssueInvoice + CreateShipmentForOrder calls are safe to
// retry (each guards by order state).
func (s *Service) FulfillPaidOrderJob(ctx context.Context, orderID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s.blob != nil {
		if _, err := s.IssueInvoice(ctx, orderID); err != nil {
			return fmt.Errorf("issue invoice: %w", err)
		}
	}
	if s.courier != nil {
		if _, err := s.CreateShipmentForOrder(ctx, orderID); err != nil {
			return fmt.Errorf("create shipment: %w", err)
		}
	}
	return nil
}

// AdminListDeadLetterJobs returns the dead-letter queue so an admin
// can inspect repeated failures. Phase 6.3.
func (s *Service) AdminListDeadLetterJobs(ctx context.Context, limit int) ([]*struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Attempts   int             `json:"attempts"`
	LastError  *string         `json:"last_error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	DeadLetter *time.Time      `json:"dead_letter_at,omitempty"`
}, error) {
	rows, err := s.store.ListDeadLetterJobs(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*struct {
		ID         int64           `json:"id"`
		Kind       string          `json:"kind"`
		Payload    json.RawMessage `json:"payload"`
		Attempts   int             `json:"attempts"`
		LastError  *string         `json:"last_error,omitempty"`
		CreatedAt  time.Time       `json:"created_at"`
		DeadLetter *time.Time      `json:"dead_letter_at,omitempty"`
	}, 0, len(rows))
	for _, r := range rows {
		out = append(out, &struct {
			ID         int64           `json:"id"`
			Kind       string          `json:"kind"`
			Payload    json.RawMessage `json:"payload"`
			Attempts   int             `json:"attempts"`
			LastError  *string         `json:"last_error,omitempty"`
			CreatedAt  time.Time       `json:"created_at"`
			DeadLetter *time.Time      `json:"dead_letter_at,omitempty"`
		}{
			ID: r.ID, Kind: r.Kind, Payload: r.Payload, Attempts: r.Attempts,
			LastError: r.LastError, CreatedAt: r.CreatedAt, DeadLetter: r.DeadLetter,
		})
	}
	return out, nil
}

// ProcessReturnApprovedJob is the idempotent refund + ledger handler.
// Currently a thin wrapper around initiateReturnRefund — the persistence
// guard inside that path is what makes the retry idempotent.
func (s *Service) ProcessReturnApprovedJob(ctx context.Context, returnID uuid.UUID) error {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return fmt.Errorf("load return: %w", err)
	}
	if r == nil {
		return fmt.Errorf("return not found")
	}
	if r.Status != "approved" {
		// Not in a state we can act on — treat as done so the worker
		// doesn't retry forever.
		return nil
	}
	// Pass the seller as the actor — they're the party debited, and the
	// existing initiateReturnRefund uses the actorID only for logging.
	s.initiateReturnRefund(ctx, r, r.SellerID)
	return nil
}
