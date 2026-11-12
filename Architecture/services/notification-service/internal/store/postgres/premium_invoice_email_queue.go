package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Durable Momentum Premium invoice e-mails (migration 020): the consumer
// enqueues, a worker leases, sends and finishes — the copyright e-mail queue
// (019) on its own table, with the same lease and result semantics. Jobs use
// CopyrightEmailJob as the leased-job shape.

// EnqueuePremiumInvoiceEmail records one e-mail job; false when it exists
// already (a redelivered or re-published event).
func (s *Store) EnqueuePremiumInvoiceEmail(ctx context.Context, id, recipient uuid.UUID, payload []byte) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO premium_invoice_email_jobs (id, recipient_id, payload)
		VALUES ($1, $2, $3::jsonb)
		ON CONFLICT (id) DO NOTHING`, id, recipient, payload)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimPremiumInvoiceEmail leases the next due job (pending and due, or
// processing with a lapsed lease). pgx.ErrNoRows when there is none.
func (s *Store) ClaimPremiumInvoiceEmail(ctx context.Context) (*CopyrightEmailJob, error) {
	j := &CopyrightEmailJob{}
	err := s.db.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM premium_invoice_email_jobs
			WHERE (status = 'pending' AND available_at <= now()) OR (status = 'processing' AND lease_until < now())
			ORDER BY available_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE premium_invoice_email_jobs j
		SET status = 'processing', lease_id = gen_random_uuid(), lease_until = now() + make_interval(secs => $1),
		    attempts = attempts + 1
		FROM next WHERE j.id = next.id
		RETURNING j.id, j.payload, j.lease_id, j.attempts`, CopyrightEmailLease.Seconds()).
		Scan(&j.ID, &j.Payload, &j.LeaseID, &j.Attempts)
	return j, err
}

// FinishPremiumInvoiceEmail records a leased job's result (pending = retry
// after delay, sent, skipped, failed); only the lease holder's write lands.
// A done job's payload is emptied; a failed one raises an ops alert naming
// the job, never the recipient.
func (s *Store) FinishPremiumInvoiceEmail(ctx context.Context, j *CopyrightEmailJob, status, code string, delay time.Duration) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	tag, err := tx.Exec(ctx, `
		UPDATE premium_invoice_email_jobs
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
			VALUES ('premium_invoice_email', 'premium_invoice_email_failed', 'warning', $1, $2, $3)
			ON CONFLICT (dedupe_key) DO NOTHING`, j.ID, "premium-invoice-email:"+j.ID.String(), detail); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
