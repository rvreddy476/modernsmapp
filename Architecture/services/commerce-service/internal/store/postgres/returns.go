// Return requests: create, list for seller and customer, status and pickup
// label updates, and the refund markers on returns and orders. Moved out of
// store.go unchanged.

package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Return Requests ─────────────────────────────────────────

func (s *Store) CreateReturnRequest(ctx context.Context, r *ReturnRequest) error {
	r.ID = uuid.New()
	r.RequestedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO return_requests (id,order_id,order_item_id,customer_user_id,seller_id,
		  reason_code,reason_description,status,requested_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, r.OrderID, r.OrderItemID, r.CustomerUserID, r.SellerID,
		r.ReasonCode, r.ReasonDescription, r.Status, r.RequestedAt,
	)
	return err
}

// ListReturnsBySeller returns return requests for a seller, optionally
// filtered by status. Phase 4.3 — feeds the seller returns inbox.
// status="" returns all states.
func (s *Store) ListReturnsBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*ReturnRequest, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
				reason_description, status, approved_at, rejected_at, rejection_reason,
				requested_at, refund_amount
			FROM return_requests
			WHERE seller_id = $1
			ORDER BY requested_at DESC
			LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
				reason_description, status, approved_at, rejected_at, rejection_reason,
				requested_at, refund_amount
			FROM return_requests
			WHERE seller_id = $1 AND status = $2
			ORDER BY requested_at DESC
			LIMIT $3 OFFSET $4`, sellerID, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReturnRequest
	for rows.Next() {
		var r ReturnRequest
		if err := rows.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
			&r.ReasonCode, &r.ReasonDescription, &r.Status, &r.ApprovedAt, &r.RejectedAt,
			&r.RejectionReason, &r.RequestedAt, &r.RefundAmount); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ListReturnsByCustomer returns a customer's return requests across all orders.
func (s *Store) ListReturnsByCustomer(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*ReturnRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
			reason_description, status, approved_at, rejected_at, rejection_reason,
			requested_at
		FROM return_requests
		WHERE customer_user_id = $1
		ORDER BY requested_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReturnRequest
	for rows.Next() {
		var r ReturnRequest
		if err := rows.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
			&r.ReasonCode, &r.ReasonDescription, &r.Status, &r.ApprovedAt, &r.RejectedAt,
			&r.RejectionReason, &r.RequestedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, nil
}

// UpdateReturnStatus advances a return through requested → approved/rejected.
// rejReason is only persisted when status is 'rejected'; pass nil otherwise.
//
// (Earlier signature took an actorID for audit, but no approved_by /
// rejected_by columns exist on return_requests so it was always discarded.
// Dropped to avoid a pgx parameter-type-inference error when callers passed
// untyped nils.)
func (s *Store) UpdateReturnStatus(ctx context.Context, id uuid.UUID, status string, rejReason *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests SET status=$2,
		  approved_at=CASE WHEN $2='approved' THEN NOW() ELSE approved_at END,
		  rejected_at=CASE WHEN $2='rejected' THEN NOW() ELSE rejected_at END,
		  rejection_reason=COALESCE($3,rejection_reason)
		WHERE id=$1`, id, status, rejReason)
	return err
}

// GetReturnRequestByID returns a single return request for inspection.
func (s *Store) GetReturnRequestByID(ctx context.Context, id uuid.UUID) (*ReturnRequest, error) {
	r := &ReturnRequest{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, order_item_id, customer_user_id, seller_id,
		       reason_code, reason_description, status,
		       approved_at, rejected_at, rejection_reason, refund_amount,
		       requested_at
		FROM return_requests WHERE id=$1`, id).Scan(
		&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
		&r.ReasonCode, &r.ReasonDescription, &r.Status,
		&r.ApprovedAt, &r.RejectedAt, &r.RejectionReason, &r.RefundAmount,
		&r.RequestedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// SetReturnPickupLabel records the courier-issued return shipping details
// (pickup at the customer's address, drop at the seller). Called after
// ApproveReturn books a pickup with the courier.
func (s *Store) SetReturnPickupLabel(ctx context.Context, returnID uuid.UUID, courierName, awb, labelURL string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET pickup_courier=$2, pickup_tracking_number=$3, pickup_label_url=$4
		WHERE id=$1`, returnID, courierName, awb, labelURL)
	return err
}

// SetReturnRefund stamps the refund intent + status onto the return. Used
// once payments-service accepts the refund — even if the gateway is async,
// we record the intent ID immediately so a follow-up webhook can find it.
func (s *Store) SetReturnRefund(ctx context.Context, returnID uuid.UUID, refundIntentID, status string, amount float64) error { // money-exempt: returns are fenced at /v1/commerce/returns (LB-11)
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET refund_intent_id=$2, refund_status=$3, refund_amount=$4
		WHERE id=$1`, returnID, refundIntentID, status, amount)
	return err
}

// MarkReturnRefundSucceededByIntent flips a return's refund_status to
// 'succeeded' once payments-service confirms the refund via Kafka. Keyed
// on refund_intent_id (set by SetReturnRefund at approve time) so the
// consumer doesn't need to know the return ID. Returns the affected row
// count so the caller can log a no-op gracefully (event for a refund
// that was never tied to a return).
func (s *Store) MarkReturnRefundSucceededByIntent(ctx context.Context, intentID string) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET refund_status='succeeded'
		WHERE refund_intent_id=$1 AND refund_status IN ('pending','processing')`,
		intentID)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// StampOrderRefundIntent records the refund intent ID + flips the
// order's payment_status to 'refund_pending'. Used by CancelOrder when
// it kicks off the refund on payments-service so the later
// payment.refunded event can find the order via intent_id.
func (s *Store) StampOrderRefundIntent(ctx context.Context, orderID uuid.UUID, intentID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE orders
		SET refund_intent_id = $2,
		    payment_status = 'refund_pending',
		    updated_at = NOW()
		WHERE id = $1 AND payment_status = 'paid'`,
		orderID, intentID)
	return err
}

// MarkOrderRefundedByPayment is used by the consumer when a refund
// event arrives for an order-level intent (i.e. a CancelOrder refund
// rather than a per-line return). Flips orders.payment_status to
// 'refunded' if currently 'paid'. Keyed on the intent id stamped onto
// the order at refund-initiation time. Returns affected row count.
func (s *Store) MarkOrderRefundedByPayment(ctx context.Context, intentID string) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE orders
		SET payment_status='refunded', updated_at=NOW()
		WHERE refund_intent_id=$1 AND payment_status IN ('paid','refund_pending')`,
		intentID)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}
