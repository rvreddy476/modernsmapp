-- 009_handle_history.sql: Handle (username) change audit + redirect support
-- Old handles redirect to current for 90 days after change.
-- Cooldown: 30 days between changes.

CREATE TABLE IF NOT EXISTS profile.handle_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    old_username    TEXT NOT NULL,
    new_username    TEXT NOT NULL,
    changed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cooldown_until  TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);

CREATE INDEX IF NOT EXISTS idx_handle_history_user
    ON profile.handle_history(user_id, changed_at DESC);

-- Lookup by old username for redirect resolution (most recent first)
CREATE INDEX IF NOT EXISTS idx_handle_history_old_username
    ON profile.handle_history(old_username, changed_at DESC);

COMMENT ON TABLE profile.handle_history IS 'Tracks username/handle changes for redirect support and cooldown enforcement';
