-- Migration 019: durable copyright emails (follow-up to the 24 Oct copyright
-- email, 25 Oct 2026).
--
-- The copyright email used a pre-send claim in notify_meta.event_dedup and a
-- Kafka offset committed before the handler ran, so a crash, or a send that
-- still failed after its three in-process tries, lost the email. Now the
-- consumer handles post.copyright_review.changed synchronously and commits
-- the offset only after this row is written (at-least-once), and a worker
-- sends it with durable retries — the partner_notification_jobs pattern
-- (015), so an SMTP outage never stalls the topic.
--
-- id is the UUIDv5 of (review, state[, appeal]) — CopyrightEmailDedupID — so
-- a redelivered event inserts nothing (ON CONFLICT DO NOTHING): one email per
-- review and state, however often the event arrives. A send repeats only if
-- the worker dies between the provider's acceptance and the 'sent' write
-- (the lease expires and another worker sends it again), the unavoidable
-- at-least-once window.
--
-- recipient_id is kept so an account purge removes the rows (PurgeUser); the
-- payload holds ids and the titles the email names, never an address, and
-- is emptied once the job is done.
CREATE TABLE IF NOT EXISTS copyright_email_jobs (
    id           UUID PRIMARY KEY,
    recipient_id UUID NOT NULL,
    payload      JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'processing', 'sent', 'skipped', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until  TIMESTAMPTZ,
    lease_id     UUID,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS copyright_email_jobs_due ON copyright_email_jobs (available_at, created_at)
    WHERE status IN ('pending', 'processing');
CREATE INDEX IF NOT EXISTS copyright_email_jobs_recipient ON copyright_email_jobs (recipient_id);
