-- 017_scam_link_reason.sql: the Action button's report reason (pinned
-- contract, 23 Oct 2026).
--
-- scam_link ("Fake job or scam link") reports a post's or a profile's
-- Action button. The service accepts it only on entity_type post or user
-- (service/moderation.go scamLinkEntityTypes); post-service consumes the
-- ReportFiled event and puts the target's link under review at 3 distinct
-- reporters within 7 days.
--
-- Idempotent: the constraint is dropped and re-added with the 12 spec
-- categories of 005 plus scam_link.
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_reason_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_reason_check
    CHECK (reason IN (
        'spam', 'harassment', 'scam_fraud', 'sexual_content', 'hate_abuse',
        'impersonation', 'child_safety', 'violence_threat', 'self_harm',
        'misinformation', 'intellectual_property', 'other',
        'scam_link'
    ));
