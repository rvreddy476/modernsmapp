ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS circle_overrides JSONB DEFAULT '{}';

CREATE TABLE IF NOT EXISTS notification_digests (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    period_type TEXT NOT NULL CHECK (period_type IN ('weekly','monthly')),
    period_start DATE NOT NULL,
    content     JSONB NOT NULL,
    sent_at     TIMESTAMPTZ,
    UNIQUE (user_id, period_type, period_start)
);

CREATE TABLE IF NOT EXISTS notification_bundles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    bundle_type     TEXT NOT NULL,
    count           INT NOT NULL DEFAULT 0,
    actor_ids       UUID[] NOT NULL DEFAULT '{}',
    ref_id          UUID,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at         TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_bundles_user ON notification_bundles(user_id, bundle_type, sent_at);
