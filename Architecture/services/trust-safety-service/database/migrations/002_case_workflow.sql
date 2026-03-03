-- Add case workflow fields to trust.reports
ALTER TABLE trust.reports
    ADD COLUMN IF NOT EXISTS assigned_to UUID,
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resolution_notes TEXT;

-- Enforce valid status values
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_status_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_status_check
    CHECK (status IN ('open', 'reviewing', 'resolved', 'dismissed'));
