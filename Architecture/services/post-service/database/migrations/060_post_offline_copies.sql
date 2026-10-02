-- Migration 060: offline copies inside the app (2026-10-02).
--
-- A viewer never receives a file. "Save offline" stores the video in the
-- app's own private storage; this table is the server's record of who was
-- granted a copy, on which device, and until when. One row per
-- (user, post, device); the device id is an opaque value the client made up
-- and keeps (at most 64 characters).
--
--   POST   /v1/posts/:id/offline        grant or refresh (30 days)
--   POST   /v1/posts/offline/check      is each copy still valid
--   GET    /v1/posts/offline            the caller's active copies on a device
--   DELETE /v1/posts/:id/offline        remove (sets revoked_at)
--
-- A row is never hard-deleted by something a user does: removing a copy,
-- deleting the post, turning downloads off and making the post private all
-- set revoked_at with a reason. The rows go with the post when the purge
-- worker hard-deletes it (ON DELETE CASCADE) and with a purged user
-- (PurgeUser deletes them explicitly).
--
-- The stored row is never the whole answer: the check computes validity
-- from the post as it stands and reads revoked_at / expires_at on top.

CREATE TABLE IF NOT EXISTS post_offline_copies (
    user_id         UUID        NOT NULL,
    post_id         UUID        NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    device_id       TEXT        NOT NULL CHECK (char_length(device_id) BETWEEN 1 AND 64),
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    last_checked_at TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    revoke_reason   TEXT,
    -- What the grant handed the device (rendition, caption tracks, sound,
    -- poster), so the Offline list is rebuilt without asking media-service
    -- about every row again. Never a storage key or a signed URL.
    grant_card      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (user_id, post_id, device_id)
);

-- Revoking a post's copies (delete, downloads off, private) goes by post.
CREATE INDEX IF NOT EXISTS idx_post_offline_copies_post
    ON post_offline_copies (post_id) WHERE revoked_at IS NULL;
