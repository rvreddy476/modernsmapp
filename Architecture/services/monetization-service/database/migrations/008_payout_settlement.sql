CREATE TABLE IF NOT EXISTS payout_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','settled','failed')),
    total_paise BIGINT NOT NULL DEFAULT 0,
    payout_count INT NOT NULL DEFAULT 0,
    provider_batch_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ
);

ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS batch_id UUID REFERENCES payout_batches(id);
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS provider_reference TEXT;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS failure_reason TEXT;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS payout_statements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    total_earnings_paise BIGINT NOT NULL DEFAULT 0,
    total_deductions_paise BIGINT NOT NULL DEFAULT 0,
    total_payout_paise BIGINT NOT NULL DEFAULT 0,
    pdf_media_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ps_user ON payout_statements(user_id, period_end DESC);

-- Enhanced payout_requests status
ALTER TABLE payout_requests DROP CONSTRAINT IF EXISTS payout_requests_status_check;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('pending','kyc_check','approved','batched','in_flight','settled','failed','returned','held','processing','paid'));
