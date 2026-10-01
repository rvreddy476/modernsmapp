-- 003_truthful_live.sql — truthful lifecycle, viewer presence, moderation
-- (remove, bans, moderators, reports, platform live bans), admin audit,
-- webhook idempotency and the transactional outbox (1 Oct 2026).
--
-- States: scheduled -> starting (POST /start) -> live (host track published)
-- <-> reconnecting (host left / unpublished) -> ended | failed. A stream is
-- 'live' only on evidence of media; the sweeper ends what the webhooks miss.
--
-- Every statement is idempotent; the migration runner applies the whole file
-- in one transaction.

ALTER TABLE live_streams DROP CONSTRAINT IF EXISTS live_streams_status_check;
ALTER TABLE live_streams ADD CONSTRAINT live_streams_status_check
    CHECK (status IN ('scheduled','starting','live','reconnecting','ended','failed'));

ALTER TABLE live_streams
    ADD COLUMN IF NOT EXISTS ended_reason      TEXT,
    ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS viewer_count      INT NOT NULL DEFAULT 0;

ALTER TABLE live_streams DROP CONSTRAINT IF EXISTS live_streams_ended_reason_check;
ALTER TABLE live_streams ADD CONSTRAINT live_streams_ended_reason_check
    CHECK (ended_reason IS NULL OR ended_reason IN
        ('host_ended','host_lost','room_finished','admin_stopped','no_media'));

-- Existing rows got status_changed_at = NOW() from the column default; give
-- them the moment their current status actually began.
UPDATE live_streams
SET status_changed_at = COALESCE(ended_at, started_at, created_at);

-- Before this migration POST /start flipped a stream to 'live' with no media.
-- Such a row is no evidence of a broadcast: it becomes 'reconnecting', and
-- the sweeper ends it as host_lost after the grace unless LiveKit shows the
-- host publishing (the reconcile step) or a track_published webhook arrives.
UPDATE live_streams
SET status = 'reconnecting', status_changed_at = NOW(), updated_at = NOW()
WHERE status = 'live';

-- The only paths that ended a stream before were the host's POST /end and
-- the account-control hide (also the host's own action).
UPDATE live_streams SET ended_reason = 'host_ended'
WHERE status = 'ended' AND ended_reason IS NULL;

CREATE INDEX IF NOT EXISTS idx_live_streams_active_status
    ON live_streams (status, status_changed_at)
    WHERE status IN ('starting','live','reconnecting');

-- Viewer presence from LiveKit participant webhooks. One row per (stream,
-- identity) so a duplicated join never double counts; the host is excluded
-- by the count query, not by the writer.
CREATE TABLE IF NOT EXISTS live_stream_presence (
    stream_id  UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    present    BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_live_stream_presence_present
    ON live_stream_presence (stream_id) WHERE present;

-- Removed chat messages stay for the audit trail and reports; every read
-- for viewers filters them out.
ALTER TABLE live_chat_messages
    ADD COLUMN IF NOT EXISTS removed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS removed_by UUID;

-- Per-stream moderators (host-appointed, at most 5 — enforced in Go).
CREATE TABLE IF NOT EXISTS live_stream_moderators (
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL,
    added_by  UUID NOT NULL,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

-- Per-stream bans: no chat, no viewer token, no live room subscription.
CREATE TABLE IF NOT EXISTS live_stream_bans (
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL,
    banned_by UUID NOT NULL,
    reason    TEXT NOT NULL DEFAULT '' CHECK (char_length(reason) <= 500),
    banned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

-- Platform-wide live ban (admin): no going live, no chat anywhere.
CREATE TABLE IF NOT EXISTS live_platform_bans (
    user_id   UUID PRIMARY KEY,
    reason    TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
    banned_by UUID NOT NULL,
    banned_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Viewer reports. One per reporter per target (the stream itself, or one
-- message of it). message_id carries no FK: a purged message must not
-- rewrite the uniqueness of the reports about it.
CREATE TABLE IF NOT EXISTS live_reports (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id         UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    reporter_id       UUID NOT NULL,
    message_id        UUID,
    target_user_id    UUID NOT NULL,
    reason            TEXT NOT NULL CHECK (reason IN
                         ('spam','harassment','hate','nudity','violence','scam','other')),
    note              TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    status            TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    resolution        TEXT CHECK (resolution IS NULL OR resolution IN ('dismiss','remove_message','ban_user')),
    resolution_reason TEXT,
    resolved_by       UUID,
    resolved_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_live_reports_once
    ON live_reports (reporter_id, stream_id,
                     COALESCE(message_id, '00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX IF NOT EXISTS idx_live_reports_status
    ON live_reports (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_live_reports_reporter
    ON live_reports (reporter_id, created_at DESC);

-- Append-only audit of every admin action taken through the admin family.
CREATE TABLE IF NOT EXISTS live_admin_audit (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    UUID NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_admin_audit_at ON live_admin_audit (at DESC);

CREATE OR REPLACE FUNCTION live_v2_admin_audit_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'live_admin_audit is append-only';
END
$$;
DROP TRIGGER IF EXISTS trg_live_admin_audit_no_update ON live_admin_audit;
CREATE TRIGGER trg_live_admin_audit_no_update
    BEFORE UPDATE OR DELETE ON live_admin_audit
    FOR EACH ROW EXECUTE FUNCTION live_v2_admin_audit_append_only();
DROP TRIGGER IF EXISTS trg_live_admin_audit_no_truncate ON live_admin_audit;
CREATE TRIGGER trg_live_admin_audit_no_truncate
    BEFORE TRUNCATE ON live_admin_audit
    FOR EACH STATEMENT EXECUTE FUNCTION live_v2_admin_audit_append_only();

-- LiveKit webhook idempotency: an event id is recorded after it was applied;
-- a redelivery of a recorded id is acknowledged without re-applying.
CREATE TABLE IF NOT EXISTS live_webhook_events (
    event_id    TEXT PRIMARY KEY,
    event       TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_webhook_events_received ON live_webhook_events (received_at);

-- Recording import jobs. post-service turns live.stream.vod_ready into a
-- long video only for a registered media asset that is READY, so the egress
-- file is imported into media-service (POST
-- /v1/media/internal/recordings/import, idempotent on (source, source_ref =
-- stream id)) and re-asked until its processing_status is ready; vod_ready
-- is enqueued only in the transaction that records the ready media id.
-- state: pending (retrying / waiting for processing), done (vod_ready
-- enqueued), failed (terminal: refused import, failed/rejected/deleted
-- asset, or 6 hours without a ready asset — no vod_ready).
CREATE TABLE IF NOT EXISTS live_recording_imports (
    stream_id         UUID PRIMARY KEY REFERENCES live_streams(id) ON DELETE CASCADE,
    owner_user_id     UUID NOT NULL,
    bucket            TEXT NOT NULL,
    object_key        TEXT NOT NULL,
    recording_url     TEXT NOT NULL,
    duration_ms       BIGINT NOT NULL DEFAULT 0,
    state             TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','done','failed')),
    media_id          UUID,
    processing_status TEXT,
    attempts          INT NOT NULL DEFAULT 0,
    next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    done_at           TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_live_recording_imports_due
    ON live_recording_imports (next_attempt_at) WHERE done_at IS NULL;

-- Transactional outbox: commerce-service migration 005's outbox_events shape
-- (the shared/outbox.Publisher contract), in its own schema because the
-- shared `app` database already has a public.outbox_events owned by another
-- service with a different shape (rider-service did the same: `rider`).
CREATE SCHEMA IF NOT EXISTS live_v2;
CREATE TABLE IF NOT EXISTS live_v2.outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    idempotency_key TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON live_v2.outbox_events (id)
    WHERE published_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_idempotency_key
    ON live_v2.outbox_events (idempotency_key)
    WHERE idempotency_key IS NOT NULL;
