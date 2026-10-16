-- Migration 062: comment reports from trust-safety reach the auto-hold
-- (TikTok parity T-13).
--
-- The gateway sends POST /v1/reports to trust-safety-service, so
-- post-service's own report route (and the comments.flagged_count hold it
-- drives) was never reached. post-service now consumes trust-safety's
-- ReportFiled event (social.events.v1) and, for entity_type "comment",
-- counts the report toward the existing threshold
-- (store/postgres/comment_report_holds.go).
--
-- One row per (comment, reporter) that has been counted. The primary key
-- is the whole idempotency rule:
--   * a redelivered event (same report id) hits the same key: no recount;
--   * one reporter filing a second report on the same comment (allowed by
--     trust-safety once the first is closed) hits the same key: one
--     reporter counts once per comment.
-- report_id records which report was counted, for audit.

CREATE TABLE IF NOT EXISTS comment_report_reporters (
    comment_id  UUID        NOT NULL,
    reporter_id UUID        NOT NULL,
    report_id   UUID        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (comment_id, reporter_id)
);
