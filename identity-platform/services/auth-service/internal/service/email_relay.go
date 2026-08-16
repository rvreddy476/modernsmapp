package service

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/identity-auth-service/internal/store"
)

// EmailJobStore is the consumer-owned boundary the relay needs. Narrow on
// purpose: the relay drains a queue, it does not get the whole Store.
type EmailJobStore interface {
	FetchDueEmailJobs(ctx context.Context, limit int) ([]store.EmailJob, error)
	MarkEmailJobSent(ctx context.Context, id int64) error
	RescheduleEmailJob(ctx context.Context, id int64, nextAttempt time.Time, lastErr string) error
	EmailJobBacklog(ctx context.Context) (int, time.Time, error)
}

// EmailSendFunc issues and sends the mail for one job. In production this is
// Service.RequestEmailVerification, which mints a FRESH code — the queue row
// carries no credential (migration 016).
type EmailSendFunc func(ctx context.Context, userID uuid.UUID) error

// EmailJobRelay drains auth.email_delivery_jobs.
//
// It exists so a mail-provider outage degrades into "the email is late"
// instead of "the account was created and nobody will ever be told".
type EmailJobRelay struct {
	store    EmailJobStore
	send     EmailSendFunc
	log      *slog.Logger
	interval time.Duration
	batch    int
}

const (
	// A job that has failed this many times is left in the queue but stops
	// being retried on the fast cadence — the backoff cap does that — so it
	// remains visible for an operator rather than being silently dropped.
	emailJobBaseBackoff = 30 * time.Second
	emailJobMaxBackoff  = 30 * time.Minute
	emailJobBatch       = 25

	// Crossing either bound means new registrants are not being contacted.
	emailBacklogWarnCount = 50
	emailBacklogWarnAge   = 5 * time.Minute
)

func NewEmailJobRelay(
	s EmailJobStore,
	send EmailSendFunc,
	log *slog.Logger,
	interval time.Duration,
) *EmailJobRelay {
	if log == nil {
		log = slog.Default()
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &EmailJobRelay{store: s, send: send, log: log, interval: interval, batch: emailJobBatch}
}

func (r *EmailJobRelay) Start(ctx context.Context) {
	r.log.Info("starting verification email relay", "interval", r.interval)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.log.Info("verification email relay stopped")
			return
		case <-ticker.C:
			r.ProcessOnce(ctx)
		}
	}
}

// ProcessOnce drains one batch. Exported so a test can drive the relay
// deterministically instead of waiting on a ticker.
func (r *EmailJobRelay) ProcessOnce(ctx context.Context) {
	jobs, err := r.store.FetchDueEmailJobs(ctx, r.batch)
	if err != nil {
		r.log.Error("failed to fetch due email jobs", "event", "email_relay_fetch_failed", "err", err)
		return
	}

	for _, job := range jobs {
		if err := r.send(ctx, job.UserID); err != nil {
			next := time.Now().Add(backoffFor(job.Attempts))
			if rescheduleErr := r.store.RescheduleEmailJob(ctx, job.ID, next, err.Error()); rescheduleErr != nil {
				r.log.Error("failed to reschedule email job",
					"event", "email_relay_reschedule_failed",
					"job_id", job.ID, "err", rescheduleErr)
			}
			r.log.Warn("verification email send failed — will retry",
				"event", "email_relay_send_failed",
				"job_id", job.ID,
				"attempts", job.Attempts+1,
				"retry_in_seconds", int(backoffFor(job.Attempts).Seconds()),
				"err", err)
			continue
		}

		if err := r.store.MarkEmailJobSent(ctx, job.ID); err != nil {
			// The mail went out. Failing to record that risks one duplicate
			// on the next pass, which is strictly better than dropping it.
			r.log.Error("email sent but job not marked — may resend",
				"event", "email_relay_mark_failed",
				"job_id", job.ID, "err", err)
			continue
		}
		r.log.Info("verification email delivered by relay",
			"event", "email_relay_sent", "job_id", job.ID, "attempts", job.Attempts+1)
	}

	r.reportBacklog(ctx)
}

// reportBacklog emits the queue-health signal. Stable event names so a
// CloudWatch metric filter can derive depth and oldest-age until real OTel
// metrics land (audit B6). No user id, no job id in the dimensions.
func (r *EmailJobRelay) reportBacklog(ctx context.Context) {
	count, oldest, err := r.store.EmailJobBacklog(ctx)
	if err != nil {
		return
	}
	ageSeconds := int(time.Since(oldest).Seconds())
	if count > emailBacklogWarnCount || (count > 0 && time.Since(oldest) > emailBacklogWarnAge) {
		r.log.Warn("verification email backlog — new registrants are not being contacted",
			"event", "email_backlog_unhealthy",
			"pending", count,
			"oldest_age_seconds", ageSeconds)
		return
	}
	r.log.Debug("verification email backlog",
		"event", "email_backlog", "pending", count, "oldest_age_seconds", ageSeconds)
}

// backoffFor returns bounded exponential backoff for the next attempt.
//
// No jitter is added here because the relay is a single serialised drain loop
// rather than N clients retrying in lockstep; the thundering-herd shape this
// would guard against does not exist. Add jitter if the relay is ever sharded.
func backoffFor(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	backoff := time.Duration(float64(emailJobBaseBackoff) * math.Pow(2, float64(attempts)))
	if backoff > emailJobMaxBackoff || backoff <= 0 {
		return emailJobMaxBackoff
	}
	return backoff
}
