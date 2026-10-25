package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Durable copyright emails (migration 019): the consumer enqueues, a worker
// leases, sends and finishes. See the migration for the delivery guarantee.

// CopyrightEmailLease is how long a claimed job is the claimant's; a worker
// that dies mid-send loses it after this and another worker retries.
const CopyrightEmailLease = 3 * time.Minute

// CopyrightEmailJob is one leased email.
type CopyrightEmailJob struct {
	ID       uuid.UUID
	Payload  json.RawMessage
	LeaseID  uuid.UUID
	Attempts int
}

// EnqueueCopyrightEmail records one email job and reports whether this call
// inserted it. false: the job exists already (a redelivered event), or the
// email was sent before the queue existed — the old pre-send claim in
// notify_meta.event_dedup under the same id — so a redelivery after the
// upgrade never sends it twice either.
func (s *Store) EnqueueCopyrightEmail(ctx context.Context, id, recipient uuid.UUID, payload []byte) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO copyright_email_jobs (id, recipient_id, payload)
		SELECT $1::uuid, $2::uuid, $3::jsonb
		WHERE NOT EXISTS (SELECT 1 FROM notify_meta.event_dedup WHERE event_id = $1::uuid)
		ON CONFLICT (id) DO NOTHING`, id, recipient, payload)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimCopyrightEmail leases the next due job: a pending one whose retry
// time has come, or a processing one whose lease ran out (its worker died).
// pgx.ErrNoRows when there is none.
func (s *Store) ClaimCopyrightEmail(ctx context.Context) (*CopyrightEmailJob, error) {
	j := &CopyrightEmailJob{}
	err := s.db.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM copyright_email_jobs
			WHERE (status = 'pending' AND available_at <= now()) OR (status = 'processing' AND lease_until < now())
			ORDER BY available_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE copyright_email_jobs j
		SET status = 'processing', lease_id = gen_random_uuid(), lease_until = now() + make_interval(secs => $1),
		    attempts = attempts + 1
		FROM next WHERE j.id = next.id
		RETURNING j.id, j.payload, j.lease_id, j.attempts`, CopyrightEmailLease.Seconds()).
		Scan(&j.ID, &j.Payload, &j.LeaseID, &j.Attempts)
	return j, err
}

// FinishCopyrightEmail records a leased job's result: status pending (retry
// after delay), sent, skipped or failed. Only the lease holder's write
// lands. A finished job's payload is emptied (nothing about the email is
// kept once it is done); a failed one keeps it for an operator and raises
// an ops alert naming the job, never the recipient.
func (s *Store) FinishCopyrightEmail(ctx context.Context, j *CopyrightEmailJob, status, code string, delay time.Duration) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	tag, err := tx.Exec(ctx, `
		UPDATE copyright_email_jobs
		SET status = $3, last_error = $4, available_at = now() + make_interval(secs => $5),
		    lease_id = NULL, lease_until = NULL,
		    completed_at = CASE WHEN $3 = 'pending' THEN NULL ELSE now() END,
		    payload = CASE WHEN $3 IN ('sent', 'skipped') THEN '{}'::jsonb ELSE payload END
		WHERE id = $1 AND lease_id = $2`, j.ID, j.LeaseID, status, code, delay.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 && status == "failed" {
		detail, _ := json.Marshal(map[string]any{"job_id": j.ID.String(), "reason": code, "attempts": j.Attempts})
		if _, err := tx.Exec(ctx, `
			INSERT INTO notify_meta.ops_alerts (source, kind, severity, subject_id, dedupe_key, detail)
			VALUES ('copyright_email', 'copyright_email_failed', 'warning', $1, $2, $3)
			ON CONFLICT (dedupe_key) DO NOTHING`, j.ID, "copyright-email:"+j.ID.String(), detail); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
