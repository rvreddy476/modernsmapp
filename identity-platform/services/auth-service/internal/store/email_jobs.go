package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EmailJobPurposeVerify is the only purpose in use today. The column exists so
// password-reset and step-up mail can share the queue later.
const EmailJobPurposeVerify = "email_verify"

// EmailJob is one durable send.
//
// It intentionally holds no code: auth.otp_codes stores only a hash, so a
// retry must re-issue rather than replay. See migration 016.
type EmailJob struct {
	ID       int64
	UserID   uuid.UUID
	Purpose  string
	Attempts int
}

// EnqueueEmailJobTx queues a send inside the caller's transaction.
//
// This is the whole point of the table: the account row and the obligation to
// email its owner commit together, so a mail outage can never produce an
// account nobody will ever contact.
func (s *Store) EnqueueEmailJobTx(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	purpose string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.email_delivery_jobs (user_id, purpose)
		VALUES ($1, $2)
	`, userID, purpose)
	return err
}

// FetchDueEmailJobs returns unsent jobs whose backoff has elapsed.
//
// Bounded by limit and covered by the partial index on (next_attempt_at)
// WHERE sent_at IS NULL. FOR UPDATE SKIP LOCKED lets more than one relay
// replica drain the queue without two of them sending the same mail.
func (s *Store) FetchDueEmailJobs(ctx context.Context, limit int) ([]EmailJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, purpose, attempts
		FROM auth.email_delivery_jobs
		WHERE sent_at IS NULL AND next_attempt_at <= now()
		ORDER BY next_attempt_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []EmailJob
	for rows.Next() {
		var j EmailJob
		if err := rows.Scan(&j.ID, &j.UserID, &j.Purpose, &j.Attempts); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// MarkEmailJobSent closes a job out.
func (s *Store) MarkEmailJobSent(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.email_delivery_jobs
		SET sent_at = now(), last_error = NULL
		WHERE id = $1
	`, id)
	return err
}

// RescheduleEmailJob records a failure and backs the job off.
//
// last_error is truncated: a provider can return a very long body, and an
// unbounded error column is a slow way to fill a disk.
func (s *Store) RescheduleEmailJob(
	ctx context.Context,
	id int64,
	nextAttempt time.Time,
	lastErr string,
) error {
	const maxErrLen = 500
	if len(lastErr) > maxErrLen {
		lastErr = lastErr[:maxErrLen]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE auth.email_delivery_jobs
		SET attempts = attempts + 1,
		    next_attempt_at = $2,
		    last_error = $3
		WHERE id = $1
	`, id, nextAttempt, lastErr)
	return err
}

// MarkUserEmailJobsSent closes out every pending job for a user.
//
// Used by the registration fast path: when the inline send succeeds there is
// nothing left for the relay to do, and leaving the row pending would send a
// second code a few seconds later.
func (s *Store) MarkUserEmailJobsSent(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.email_delivery_jobs
		SET sent_at = now(), last_error = NULL
		WHERE user_id = $1 AND sent_at IS NULL
	`, userID)
	return err
}

// EmailJobBacklog reports queue depth and the age of the oldest pending job.
//
// Oldest-age is the signal that matters: a large queue draining steadily is
// healthy, while a small queue that is not moving means every new registrant
// is waiting on mail that will not arrive.
func (s *Store) EmailJobBacklog(ctx context.Context) (count int, oldest time.Time, err error) {
	row := s.db.QueryRow(ctx, `
		SELECT count(*), COALESCE(min(created_at), now())
		FROM auth.email_delivery_jobs
		WHERE sent_at IS NULL
	`)
	err = row.Scan(&count, &oldest)
	return count, oldest, err
}
