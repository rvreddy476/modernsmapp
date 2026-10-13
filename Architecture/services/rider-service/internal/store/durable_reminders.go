package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QueueDocumentReminder records the dedupe key and the notification in one
// transaction. A broker outage is handled by the outbox, never by dropping a
// reminder after marking it as sent. The recipient is the persisted owner.
func (s *Store) QueueDocumentReminder(ctx context.Context, d ExpiringDocument, bucket string) (bool, error) {
	if d.PartnerID == uuid.Nil || d.DocumentID == uuid.Nil || bucket == "" {
		return false, errors.New("document reminder requires partner, document and bucket")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var user uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT user_id FROM rider_partners WHERE id=$1`, d.PartnerID).Scan(&user); err != nil {
		return false, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO rider_doc_reminders_sent(partner_id,document_id,expires_at,bucket)
	 VALUES($1,$2,$3::date,$4) ON CONFLICT(document_id,bucket) DO NOTHING RETURNING id`,
		d.PartnerID, d.DocumentID, d.ExpiresAt, bucket).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(map[string]any{"partner_id": d.PartnerID, "partner_user_id": user,
		"document_id": d.DocumentID, "document_kind": d.DocumentKind, "owner_kind": d.OwnerKind,
		"expires_at": d.ExpiresAt, "bucket": bucket, "occurred_at": time.Now().UTC()})
	if err != nil {
		return false, err
	}
	if err = outbox.NewQueuer("rider").Enqueue(ctx, tx, "rider.document.expiring", "rider.partner."+d.PartnerID.String(), json.RawMessage(body)); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// RejectSubscriptionPaymentBy is the manual-proof decision only. Online payment
// settlement is refused again under lock, even if the service's precheck raced.
func (s *Store) RejectSubscriptionPaymentBy(ctx context.Context, paymentID uuid.UUID, review PartnerReview) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var partner uuid.UUID
	var subscription *uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `SELECT partner_id,subscription_id,status FROM rider_subscription_payments WHERE id=$1 FOR UPDATE`, paymentID).Scan(&partner, &subscription, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPaymentNotFound
	}
	if err != nil {
		return err
	}
	if subscription != nil {
		var checkout bool
		err = tx.QueryRow(ctx, `SELECT intent_id IS NOT NULL OR payment_status IS NOT NULL FROM rider_partner_subscriptions WHERE id=$1 FOR UPDATE`, *subscription).Scan(&checkout)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if checkout {
			return errors.New("invalid: online subscription settlement requires signed payment event")
		}
	}
	if status == "failed" {
		return nil
	}
	if status == "paid" || status == "verified" {
		return errors.New("invalid: settled payment cannot be rejected")
	}
	if _, err = tx.Exec(ctx, `UPDATE rider_subscription_payments SET status='failed',rejection_reason=$2,updated_at=now() WHERE id=$1`, paymentID, review.Reason); err != nil {
		return err
	}
	if err = enqueuePartnerReviewTx(ctx, tx, "rider.subscription.payment_rejected", partner, "rejected", review); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
