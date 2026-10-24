-- 076: appeal of a copyright block, and removal of an owner track
-- (follow-ups to 075, 25 Oct 2026).
--
-- 1. The uploader may ask for a review of a BLOCKED video ONCE — the
--    system's block for a registered rights owner (reason owner_registered)
--    or a reviewer's. The appeal reuses the review-requested shape that 073
--    already has (author_note + author_requested_at on a PENDING review): the
--    review goes back to 'pending', so it is in the reviewer queue and a
--    reviewer clears it (published as normal, once) or blocks it again. The
--    hold — the active title_review post_restrictions row — is never touched
--    by the appeal, so the video stays hidden until a reviewer clears it.
--    appealed_at marks that the one appeal is used: a second one is refused
--    (409), whatever state the review is in. The block it reopened is in
--    copyright_title_review_events (the 'block' / 'auto_block' row), which is
--    why the review row's decided_* columns may be emptied by the appeal (the
--    073/075 decision-shape CHECK requires that of a pending review).
--
-- 2. A tracked owner match (075's copyright_owner_tracks) can be removed by
--    an admin, audited in post_admin_audit like the other copyright writes.
--
-- Expand-only: one nullable column, widened CHECKs.

ALTER TABLE copyright_title_reviews ADD COLUMN IF NOT EXISTS appealed_at TIMESTAMPTZ;
-- An appeal is an author request: it always carries the request's time.
ALTER TABLE copyright_title_reviews DROP CONSTRAINT IF EXISTS copyright_title_reviews_appeal_shape;
ALTER TABLE copyright_title_reviews ADD CONSTRAINT copyright_title_reviews_appeal_shape
    CHECK (appealed_at IS NULL OR author_requested_at IS NOT NULL);

-- History: the appeal is a transition (blocked -> pending), actor = the author.
ALTER TABLE copyright_title_review_events DROP CONSTRAINT IF EXISTS copyright_title_review_events_action_check;
ALTER TABLE copyright_title_review_events ADD CONSTRAINT copyright_title_review_events_action_check
    CHECK (action IN ('hold', 'duration_measured', 'auto_clear', 'author_request', 'clear', 'block',
                      'auto_block', 'owner_match', 'appeal'));

-- The admin audit admits the track removal (target = the post; the title id
-- is in the audit row's previous value). Guarded like 072 / 073.
DO $$
BEGIN
    IF to_regclass('post_admin_audit') IS NOT NULL THEN
        ALTER TABLE post_admin_audit DROP CONSTRAINT IF EXISTS post_admin_audit_action_check;
        ALTER TABLE post_admin_audit ADD CONSTRAINT post_admin_audit_action_check
            CHECK (action IN ('report.review', 'comment.moderate', 'action_link.review',
                              'copyright_title.create', 'copyright_title.update', 'copyright_review.decide',
                              'copyright_track.remove'));
        ALTER TABLE post_admin_audit DROP CONSTRAINT IF EXISTS post_admin_audit_target_type_check;
        ALTER TABLE post_admin_audit ADD CONSTRAINT post_admin_audit_target_type_check
            CHECK (target_type IN ('content_report', 'comment', 'action_link', 'copyright_title', 'copyright_review',
                                   'copyright_track'));
    END IF;
END
$$;
