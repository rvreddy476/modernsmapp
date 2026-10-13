package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestPartnerQueueDBOperatorRetryKeepsReceiptsAndNeverReplaysOffers(t *testing.T) {
	s, db, _ := partnerQueueRig(t)
	ctx := context.Background()
	id, actor := uuid.New(), uuid.New()
	raw, _ := json.Marshal(PartnerNotice{ID: id, Type: "captain.approved"})
	if err := s.pgStore.EnqueuePartnerNotification(ctx, id, raw, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE partner_notification_jobs SET status='failed',attempts=12 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO partner_notification_receipts(job_id,token_hash) VALUES($1,'already-delivered')`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryPartnerNotification(ctx, id, actor, "Provider connection restored"); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryPartnerNotification(ctx, id, actor, "Repeated retry request"); !errors.Is(err, postgres.ErrNotificationRetryConflict) {
		t.Fatalf("duplicate retry: %v", err)
	}
	var receipts, audits int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM partner_notification_receipts WHERE job_id=$1`, id).Scan(&receipts)
	_ = db.QueryRow(ctx, `SELECT count(*) FROM partner_notification_retry_audit WHERE job_id=$1 AND actor_id=$2`, id, actor).Scan(&audits)
	if receipts != 1 || audits != 1 {
		t.Fatalf("receipts=%d audits=%d", receipts, audits)
	}
	expiry := time.Now().Add(-time.Minute)
	offer := uuid.New()
	if err := s.pgStore.EnqueuePartnerNotification(ctx, offer, raw, &expiry); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE partner_notification_jobs SET status='failed' WHERE id=$1`, offer); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryPartnerNotification(ctx, offer, actor, "Try replaying expired offer"); !errors.Is(err, postgres.ErrNotificationRetryConflict) {
		t.Fatalf("expired retry: %v", err)
	}
	rows, err := s.PartnerNotificationFailures(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("visibility rows=%d err=%v", len(rows), err)
	}
}
