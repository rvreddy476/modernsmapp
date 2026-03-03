ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_idempotency_key
    ON subscriptions(idempotency_key)
    WHERE idempotency_key IS NOT NULL;
