-- 008_deletion_tracking.sql: Track user data deletion request status
CREATE TABLE IF NOT EXISTS auth.deletion_requests (
    user_id      UUID PRIMARY KEY,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'processing', 'completed', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_deletion_requests_status
    ON auth.deletion_requests(status)
    WHERE status != 'completed';
