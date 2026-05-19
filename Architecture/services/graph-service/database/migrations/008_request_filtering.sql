-- 008_request_filtering.sql — connection-request auto-filter (P1.4).
-- trust-safety-service auto-scores incoming connection requests and marks
-- abusive ones "filtered" so they drop out of the recipient's main inbox
-- into a separate hidden queue. Idempotent.
ALTER TABLE connection_requests
    ADD COLUMN IF NOT EXISTS is_filtered BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS filtered_at TIMESTAMPTZ;

-- Main inbox: unfiltered pending requests, newest first.
CREATE INDEX IF NOT EXISTS idx_connection_req_inbox
    ON connection_requests (receiver_id, created_at DESC)
    WHERE is_filtered = FALSE AND status = 'pending';

-- Hidden queue: filtered pending requests, most-recently-filtered first.
CREATE INDEX IF NOT EXISTS idx_connection_req_filtered
    ON connection_requests (receiver_id, filtered_at DESC)
    WHERE is_filtered = TRUE AND status = 'pending';
