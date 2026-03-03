CREATE INDEX IF NOT EXISTS idx_reports_assigned_to
    ON trust.reports(assigned_to)
    WHERE assigned_to IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_reports_status
    ON trust.reports(status);
