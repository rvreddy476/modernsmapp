-- Align trust.reports category/reason values with messaging/privacy spec §10.5.
-- The report category is stored in the `reason` column. The accepted set is
-- exactly 12 values; any pre-existing row outside that set is migrated to 'other'
-- before the CHECK constraint is (re)created.

-- 1. Migrate now-invalid existing rows to 'other'.
UPDATE trust.reports
SET reason = 'other',
    updated_at = NOW()
WHERE reason NOT IN (
    'spam', 'harassment', 'scam_fraud', 'sexual_content', 'hate_abuse',
    'impersonation', 'child_safety', 'violence_threat', 'self_harm',
    'misinformation', 'intellectual_property', 'other'
);

-- 2. Drop any prior category constraint and add the spec-aligned one.
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_reason_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_reason_check
    CHECK (reason IN (
        'spam', 'harassment', 'scam_fraud', 'sexual_content', 'hate_abuse',
        'impersonation', 'child_safety', 'violence_threat', 'self_harm',
        'misinformation', 'intellectual_property', 'other'
    ));
