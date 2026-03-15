-- Scheduled calls and call links
ALTER TABLE calls.call_sessions ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;
ALTER TABLE calls.call_sessions ADD COLUMN IF NOT EXISTS link_token TEXT;
ALTER TABLE calls.call_sessions ADD COLUMN IF NOT EXISTS link_expires_at TIMESTAMPTZ;
ALTER TABLE calls.call_sessions ADD COLUMN IF NOT EXISTS lobby_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Unique index on link_token (only when not null)
CREATE UNIQUE INDEX IF NOT EXISTS idx_call_sessions_link_token ON calls.call_sessions(link_token) WHERE link_token IS NOT NULL;

-- Call reminders
CREATE TABLE IF NOT EXISTS calls.call_reminders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    call_session_id UUID NOT NULL REFERENCES calls.call_sessions(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL,
    remind_at       TIMESTAMPTZ NOT NULL,
    sent            BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (call_session_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_call_reminders_remind ON calls.call_reminders(remind_at) WHERE sent = FALSE;

-- Post-call AI summaries
CREATE TABLE IF NOT EXISTS calls.call_summaries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    call_session_id UUID NOT NULL REFERENCES calls.call_sessions(id) ON DELETE CASCADE,
    transcript_url  TEXT,
    summary_text    TEXT,
    key_points      JSONB,
    action_items    JSONB,
    participants    UUID[],
    duration_ms     INT,
    language        VARCHAR(10) DEFAULT 'en',
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (call_session_id)
);
