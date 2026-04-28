-- Returns + refund flow: extend return_requests with the courier label URL
-- (so customers see "where to drop the package") and the upstream payment
-- intent ID we kicked a refund against (for audit + idempotency).
--
-- Idempotent — safe to apply on environments where these columns may
-- already exist from a hand-applied patch.

ALTER TABLE return_requests
    ADD COLUMN IF NOT EXISTS pickup_tracking_number TEXT,
    ADD COLUMN IF NOT EXISTS pickup_label_url       TEXT,
    ADD COLUMN IF NOT EXISTS pickup_courier         TEXT,
    ADD COLUMN IF NOT EXISTS refund_intent_id       TEXT,
    ADD COLUMN IF NOT EXISTS refund_status          TEXT NOT NULL DEFAULT 'none'
        CHECK (refund_status IN ('none','pending','succeeded','failed','manual'));
