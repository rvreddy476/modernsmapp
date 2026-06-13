-- 002_chat_moderation.sql — Phase B chat moderation tables.
--
-- Adds per-stream mutes, word filters, and pin pointers on chat
-- messages. Mirrors the moderation surface from v1 live-service but
-- against the v2 store + Redis pub/sub fanout.

CREATE TABLE IF NOT EXISTS live_chat_mutes (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    muted_by    UUID NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

CREATE TABLE IF NOT EXISTS live_chat_word_filters (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    word        TEXT NOT NULL CHECK (char_length(word) BETWEEN 1 AND 100),
    added_by    UUID NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, word)
);

ALTER TABLE live_chat_messages
    ADD COLUMN IF NOT EXISTS is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_live_chat_pinned
    ON live_chat_messages(stream_id) WHERE is_pinned = TRUE;
