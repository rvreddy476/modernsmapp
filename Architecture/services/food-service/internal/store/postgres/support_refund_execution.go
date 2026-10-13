package postgres

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func enqueueSupportRefundDecisionTx(ctx context.Context, tx pgx.Tx, requestID, orderID, customerID uuid.UUID, status string) error {
	body, err := json.Marshal(map[string]any{"refund_request_id": requestID, "order_id": orderID, "user_id": customerID, "status": status})
	if err != nil {
		return err
	}
	return foodOutbox.Enqueue(ctx, tx, "food.refund."+status, "food.order."+orderID.String(), body)
}

// SupportRefundPlan returns only the refund durably linked to this approval.
func (s *Store) SupportRefundPlan(ctx context.Context, requestID uuid.UUID) (*RefundPlan, error) {
	return loadRefundPlanTx(ctx, s.db, `r.id = (SELECT execution_refund_id FROM food.refund_requests WHERE id=$1)`, requestID)
}

// Failure stays inspectable by finance and retryable by the existing worker.
// Only a stable classification is stored: provider errors can contain secrets.
func (s *Store) RecordRefundSubmissionFailure(ctx context.Context, refundID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE food.refunds SET raw_response=raw_response || jsonb_build_object(
  'last_submission_error','submission_failed_retry_pending','last_attempt_at',NOW(),
  'next_submission_at',NOW()+LEAST(900,30*power(2,LEAST(5,COALESCE((raw_response->>'submission_attempts')::integer,0)+1))) * INTERVAL '1 second',
  'submission_attempts',COALESCE((raw_response->>'submission_attempts')::integer,0)+1)
  WHERE id=$1 AND status='PENDING'`, refundID)
	return err
}

func reconcileSupportRefundsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE food.refund_requests q SET status='processed',
  refund_txn_id=COALESCE(r.provider_refund_id,r.raw_response->>'payments_command_id',r.id::text)
  FROM food.refunds r WHERE q.execution_refund_id=r.id AND r.order_id=$1
  AND r.status='PROCESSED' AND q.status='approved'`, orderID)
	return err
}
