-- 006_connection_rename.sql
-- Messaging/privacy spec v2 §3.2/§19: the canonical backend term is
-- "connection", not "friend". Renames the tables and adds the §8.3/§8.4
-- columns the connection-request lifecycle needs.
--
-- The DO block is ordering-safe: if setup.sql has already created an empty
-- `connections`/`connection_requests` on a not-yet-migrated DB, that empty
-- table is dropped before the rename so no real data is lost.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = 'public' AND table_name = 'friends') THEN
        DROP TABLE IF EXISTS connections;
        ALTER TABLE friends RENAME TO connections;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = 'public' AND table_name = 'friend_requests') THEN
        DROP TABLE IF EXISTS connection_requests;
        ALTER TABLE friend_requests RENAME TO connection_requests;
    END IF;
END $$;

ALTER TABLE connections         ADD COLUMN IF NOT EXISTS source_request_id BIGINT;
ALTER TABLE connection_requests ADD COLUMN IF NOT EXISTS source       TEXT        NOT NULL DEFAULT 'profile';
ALTER TABLE connection_requests ADD COLUMN IF NOT EXISTS message      VARCHAR(280);
ALTER TABLE connection_requests ADD COLUMN IF NOT EXISTS risk_score   SMALLINT    NOT NULL DEFAULT 0;
ALTER TABLE connection_requests ADD COLUMN IF NOT EXISTS responded_at TIMESTAMPTZ;
ALTER TABLE connection_requests ADD COLUMN IF NOT EXISTS expires_at   TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days');

-- Spec §8.3 status vocabulary: pending/accepted/declined/cancelled/expired.
UPDATE connection_requests SET status = 'declined' WHERE status = 'rejected';

ALTER TABLE follows ADD COLUMN IF NOT EXISTS source  TEXT NOT NULL DEFAULT 'profile';
ALTER TABLE blocks  ADD COLUMN IF NOT EXISTS reason  VARCHAR(32);
ALTER TABLE blocks  ADD COLUMN IF NOT EXISTS context VARCHAR(32);

-- Drives the pending-request expiry sweeper.
CREATE INDEX IF NOT EXISTS idx_connection_requests_expiry
    ON connection_requests (expires_at) WHERE status = 'pending';
