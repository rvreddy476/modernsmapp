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

// EmailJobFirstAttemptDelay is how long a freshly enqueued job waits before
// the relay may take it.
//
// M2 (registration audit): the job used to be due the instant it committed,
// while the registration fast path was still sending. The relay ticks every
// 10 seconds, so it could take the job in that gap and send a SECOND code,
// which replaced the first: the user typed the code from the email that
// arrived first and was told it was wrong. The fast path marks the job sent
// when it succeeds, well inside this delay; the relay only ever picks up a job
// whose immediate send failed.
const EmailJobFirstAttemptDelay = 2 * time.Minute

// EmailJobClaimLease is how long a claimed job is hidden from every other
// relay replica while one replica works on it.
const EmailJobClaimLease = 2 * time.Minute

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
		INSERT INTO auth.email_delivery_jobs (user_id, purpose, next_attempt_at)
		VALUES ($1, $2, now() + make_interval(secs => $3))
	`, userID, purpose, EmailJobFirstAttemptDelay.Seconds())
	return err
}

// FetchDueEmailJobs CLAIMS up to limit unsent, unabandoned jobs whose backoff
// has elapsed.
//
// M2 (registration audit): this was a SELECT ... FOR UPDATE SKIP LOCKED run as
// its own autocommit statement, so the row locks were released the moment it
// returned and two replicas (prod runs 2 to 3) could fetch and send the same
// job. The claim is now one UPDATE that pushes next_attempt_at out by a lease
// in the same statement that selects the rows, so a claimed job is invisible
// to every other replica until the lease runs out (a crashed replica's jobs
// come back on their own). A successful send closes the job; a failed one is
// rescheduled with backoff, overwriting the lease.
func (s *Store) FetchDueEmailJobs(ctx context.Context, limit int) ([]EmailJob, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE auth.email_delivery_jobs j
		SET next_attempt_at = now() + make_interval(secs => $2)
		WHERE j.id IN (
			SELECT id
			FROM auth.email_delivery_jobs
			WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= now()
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING j.id, j.user_id, j.purpose, j.attempts
	`, limit, EmailJobClaimLease.Seconds())
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

// MarkEmailJobFailed abandons a job: it will never be retried.
//
// M4 (registration audit): jobs had no terminal failure state, so an address
// that can never receive mail was retried every 30 minutes forever, each time
// minting and bcrypt-hashing a code and calling the mail provider. A failed
// job keeps its row (and last_error) for an operator; failed_at takes it out
// of the queue, the backlog alarm and the due index.
func (s *Store) MarkEmailJobFailed(ctx context.Context, id int64, lastErr string) error {
	const maxErrLen = 500
	if len(lastErr) > maxErrLen {
		lastErr = lastErr[:maxErrLen]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE auth.email_delivery_jobs
		SET failed_at = now(), attempts = attempts + 1, last_error = $2
		WHERE id = $1 AND sent_at IS NULL
	`, id, lastErr)
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
// second code a few seconds later. Also used after a successful RESEND and on
// activation: the code the user just received (or already used) must not be
// replaced by a relay send of a job left over from a failed first attempt.
func (s *Store) MarkUserEmailJobsSent(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.email_delivery_jobs
		SET sent_at = now(), last_error = NULL
		WHERE user_id = $1 AND sent_at IS NULL AND failed_at IS NULL
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
		WHERE sent_at IS NULL AND failed_at IS NULL
	`)
	err = row.Scan(&count, &oldest)
	return count, oldest, err
}
