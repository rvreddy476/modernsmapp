-- DM consent model (founder-approved 20 Oct 2026). Additive and idempotent;
-- database/setup.sql carries the same statement (applied on every boot by
-- BootstrapSchema).
--
-- Message requests now expire after 30 days without an answer: a sweeper in
-- message-service marks pending requests whose expires_at has passed as
-- 'expired' (the status, the expires_at column and its 30-day default have
-- existed since 003). This partial index keeps that sweep an index range
-- scan over pending rows only.
--
-- No data change: a pending request already older than 30 days is expired
-- by the first sweep after deploy, and every Requests list already treats a
-- pending request past its expires_at as gone.
CREATE INDEX IF NOT EXISTS idx_message_requests_pending_expiry
    ON chat.message_requests(expires_at) WHERE status = 'pending';
