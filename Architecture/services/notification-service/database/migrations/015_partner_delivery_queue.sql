-- Operational Partner notifications: durable retries, not a pre-send dedup claim.
CREATE TABLE IF NOT EXISTS partner_notification_jobs (
    id uuid PRIMARY KEY,
    payload jsonb NOT NULL,
    expires_at timestamptz,
    status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','delivered','expired','failed')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    lease_id uuid,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS partner_notification_pending ON partner_notification_jobs(available_at,created_at)
    WHERE status IN ('pending','processing');
CREATE TABLE IF NOT EXISTS partner_notification_receipts (
    job_id uuid NOT NULL REFERENCES partner_notification_jobs(id),
    token_hash text NOT NULL,
    delivered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(job_id,token_hash)
);
