package postgres

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"time"
)

type PartnerNotificationJob struct {
	ID         uuid.UUID
	Payload    json.RawMessage
	ExpiresAt  *time.Time
	LeaseID    uuid.UUID
	Attempts   int
	RetryAfter time.Duration // provider hint for this attempt; persisted as available_at
}

func (s *Store) EnqueuePartnerNotification(ctx context.Context, id uuid.UUID, payload []byte, expires *time.Time) error {
	_, err := s.db.Exec(ctx, `INSERT INTO partner_notification_jobs(id,payload,expires_at) VALUES($1,$2,$3) ON CONFLICT(id) DO NOTHING`, id, payload, expires)
	return err
}
func (s *Store) ClaimPartnerNotification(ctx context.Context) (*PartnerNotificationJob, error) {
	j := &PartnerNotificationJob{}
	err := s.db.QueryRow(ctx, `WITH next AS (
        SELECT id FROM partner_notification_jobs WHERE (status='pending' AND available_at<=now()) OR (status='processing' AND lease_until<now())
        ORDER BY expires_at ASC NULLS LAST,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1
    ) UPDATE partner_notification_jobs j SET status='processing',lease_id=gen_random_uuid(),lease_until=now()+interval '90 seconds',attempts=attempts+1
        FROM next WHERE j.id=next.id RETURNING j.id,j.payload,j.expires_at,j.lease_id,j.attempts`).Scan(&j.ID, &j.Payload, &j.ExpiresAt, &j.LeaseID, &j.Attempts)
	return j, err
}
func (s *Store) FinishPartnerNotification(ctx context.Context, j *PartnerNotificationJob, status, code string, delay time.Duration) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	tag, err := tx.Exec(ctx, `UPDATE partner_notification_jobs SET status=$3,last_error=$4,available_at=now()+make_interval(secs=>$5),
        lease_until=NULL,lease_id=NULL,completed_at=CASE WHEN $3='pending' THEN NULL ELSE now() END WHERE id=$1 AND lease_id=$2`, j.ID, j.LeaseID, status, code, delay.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 && status == "failed" {
		detail, _ := json.Marshal(map[string]any{"job_id": j.ID.String(), "reason": code, "attempts": j.Attempts})
		_, err = tx.Exec(ctx, `INSERT INTO notify_meta.ops_alerts(source,kind,severity,subject_id,dedupe_key,detail)
            VALUES('partner_notifications','partner_notification_failed','warning',$1,$2,$3) ON CONFLICT(dedupe_key) DO NOTHING`, j.ID, "partner-notification:"+j.ID.String(), detail)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) PartnerDeviceDelivered(ctx context.Context, id uuid.UUID, hash string) (bool, error) {
	var done bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partner_notification_receipts WHERE job_id=$1 AND token_hash=$2)`, id, hash).Scan(&done)
	return done, err
}
func (s *Store) MarkPartnerDeviceDelivered(ctx context.Context, j *PartnerNotificationJob, hash string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO partner_notification_receipts(job_id,token_hash) SELECT id,$3 FROM partner_notification_jobs WHERE id=$1 AND lease_id=$2
        ON CONFLICT DO NOTHING`, j.ID, j.LeaseID, hash)
	return err
}
