-- 018_copyright_report_reasons.sql: copyright report reasons (founder
-- quick wins, 2026-10-24).
--
--   copyright_mine   "Copyright: this is my content"
--   copyright_media  "Copyright: film, TV or music"
--
-- Posts only (videos and reels are posts; service/moderation.go
-- copyrightEntityTypes), into the same queue as every other report.
--
-- Idempotent: the constraint is dropped and re-added with 017's values plus
-- the two new reasons.
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_reason_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_reason_check
    CHECK (reason IN (
        'spam', 'harassment', 'scam_fraud', 'sexual_content', 'hate_abuse',
        'impersonation', 'child_safety', 'violence_threat', 'self_harm',
        'misinformation', 'intellectual_property', 'other',
        'scam_link',
        'copyright_mine', 'copyright_media'
    ));
