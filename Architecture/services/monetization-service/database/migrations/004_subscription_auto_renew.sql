-- Add auto_renew and billing_period support to subscriptions and creator_tiers.

ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS auto_renew    BOOLEAN     NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS payment_failed_at TIMESTAMPTZ;

-- billing_period on creator tiers: 'monthly' (default), 'quarterly', 'yearly'
ALTER TABLE creator_tiers
    ADD COLUMN IF NOT EXISTS billing_period TEXT NOT NULL DEFAULT 'monthly';

-- Payout requests table (tracks pending/processing/paid payout transactions separately
-- from the generic transactions table for easier worker querying).
CREATE TABLE IF NOT EXISTS payout_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    transaction_id   UUID NOT NULL REFERENCES transactions(id),
    amount           DECIMAL(12,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'processing', 'paid', 'failed', 'held')),
    payout_method_id UUID,
    requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at     TIMESTAMPTZ,
    notes            TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_payout_requests_user    ON payout_requests (user_id, status);
CREATE INDEX IF NOT EXISTS idx_payout_requests_pending ON payout_requests (status, requested_at)
    WHERE status = 'pending';
