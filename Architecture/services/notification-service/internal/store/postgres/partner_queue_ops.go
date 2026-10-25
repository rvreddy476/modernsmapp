package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

var ErrNotificationRetryConflict = errors.New("notification cannot be retried")

type PartnerNotificationSummary struct {
	ID          uuid.UUID  `json:"id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
	AvailableAt time.Time  `json:"available_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

func (s *Store) PartnerNotificationFailures(ctx context.Context) ([]PartnerNotificationSummary, error) {
	rows, err := s.db.Query(ctx, `SELECT id,COALESCE(payload->>'Type',''),status,attempts,last_error,created_at,available_at,expires_at FROM partner_notification_jobs WHERE status IN ('failed','pending','processing') ORDER BY CASE status WHEN 'failed' THEN 0 ELSE 1 END,created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PartnerNotificationSummary{}
	for rows.Next() {
		var r PartnerNotificationSummary
		if err = rows.Scan(&r.ID, &r.Type, &r.Status, &r.Attempts, &r.LastError, &r.CreatedAt, &r.AvailableAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Retry only failed, non-offer notices. Do not clear device receipts: devices
// already notified must not receive duplicates during operator recovery.
func (s *Store) RetryPartnerNotification(ctx context.Context, id, actor uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if id == uuid.Nil || actor == uuid.Nil || len(reason) < 10 || len(reason) > 1000 {
		return ErrNotificationRetryConflict
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	tag, err := tx.Exec(ctx, `UPDATE partner_notification_jobs SET status='pending',attempts=0,last_error='',available_at=now(),completed_at=NULL,lease_id=NULL,lease_until=NULL WHERE id=$1 AND status='failed' AND expires_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotificationRetryConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO partner_notification_retry_audit(job_id,actor_id,reason) VALUES($1,$2,$3)`, id, actor, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
