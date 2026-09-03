-- Database setup for Architecture/notification-service

CREATE SCHEMA IF NOT EXISTS notify_meta;

CREATE TABLE IF NOT EXISTS notify_meta.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);
-- Module 1 P0-3: durable, resumable subscriber fan-out.
--
-- The Kafka consumer records a job row synchronously (so committing the
-- event implies the work is persisted) and returns. A worker claims jobs
-- with FOR UPDATE SKIP LOCKED and pages through subscribers, advancing
-- `cursor` after each batch. A crash resumes from the cursor instead of
-- losing the remaining recipients — replacing the previous untracked
-- goroutine that dropped everything on restart and silently stopped at
-- 5,000 recipients.
CREATE TABLE IF NOT EXISTS subscriber_fanout_jobs (
    post_id        UUID PRIMARY KEY,
    channel_id     UUID NOT NULL,
    author_id      UUID NOT NULL,
    content_type   TEXT NOT NULL,
    deep_link      TEXT NOT NULL,
    notif_type     TEXT NOT NULL,
    visibility     TEXT NOT NULL DEFAULT 'public',
    post_created_at TIMESTAMPTZ NOT NULL,
    -- Keyset cursor: last subscriber user_id delivered to.
    cursor         UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    delivered      BIGINT NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','running','completed','failed')),
    attempts       INT NOT NULL DEFAULT 0,
    last_error     TEXT,
    claimed_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_fanout_jobs_pending
    ON subscriber_fanout_jobs (created_at)
    WHERE status IN ('pending','running');

-- Per-recipient dedup: one notification per (post, user) regardless of
-- duplicate event delivery or a job retry resuming over a boundary.
CREATE TABLE IF NOT EXISTS subscriber_fanout_delivered (
    post_id      UUID NOT NULL,
    user_id      UUID NOT NULL,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

-- Account control (auth-service 30-day deletion flow). A row here means the
-- account is deactivated or scheduled for deletion: every notification
-- addressed to the user is dropped at creation time. Removed on
-- user.reactivated / user.deletion_cancelled and by the purge.
CREATE TABLE IF NOT EXISTS notification_suppressed_users (
    user_id       UUID PRIMARY KEY,
    reason        TEXT        NOT NULL DEFAULT '',
    suppressed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
