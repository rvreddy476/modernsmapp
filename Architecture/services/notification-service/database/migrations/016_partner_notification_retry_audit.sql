CREATE TABLE IF NOT EXISTS partner_notification_retry_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 job_id uuid NOT NULL REFERENCES partner_notification_jobs(id),
 actor_id uuid NOT NULL,
 reason text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
