-- 009_dating_report_grievances.sql: one grievance per dating report.
-- dating-service links every report to an IT Rules 2021 grievance through
-- the internal route POST /v1/internal/grievances/dating-reports and retries
-- until it succeeds, so a retry must return the grievance the first attempt
-- created: the report id is unique among dating_report grievances.
CREATE UNIQUE INDEX IF NOT EXISTS uq_grievances_dating_report
    ON trust.grievances (about_entity_id)
    WHERE about_entity_type = 'dating_report';
