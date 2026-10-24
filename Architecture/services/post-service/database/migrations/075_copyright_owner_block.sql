-- 075: owner-registered copyright blocks (founder decision, 24 Oct 2026:
-- "Yes, block it with mail notification").
--
-- A catalogue entry (073) can now say that the RIGHTS OWNER registered the
-- title and asked for copies to be blocked (auto_block). A held upload that
-- matches such an entry is decided BLOCKED at once by the system — no
-- reviewer — with reason 'owner_registered'; it counts toward the
-- repeat-uploader pause exactly like a reviewer's block (trust-safety reads
-- the same post.copyright_review.changed "blocked" event).
--
-- The same machinery is the landing point for a future fingerprint
-- provider (service.ApplyOwnerMatch): block (as above), mute (held for a
-- reviewer with reason 'mute_requested' until audio muting exists) or track
-- (no hold; the video's earnings are excluded through the existing
-- monetization eligibility projection, copyright_owner_tracks below).
--
-- Expand-only: new columns with defaults, a new table, widened CHECKs.

-- 1. The catalogue: the owner's request and the owner's display name.
ALTER TABLE copyright_review_titles ADD COLUMN IF NOT EXISTS auto_block BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE copyright_review_titles ADD COLUMN IF NOT EXISTS rights_owner TEXT;
ALTER TABLE copyright_review_titles DROP CONSTRAINT IF EXISTS copyright_review_titles_rights_owner_check;
ALTER TABLE copyright_review_titles ADD CONSTRAINT copyright_review_titles_rights_owner_check
    CHECK (rights_owner IS NULL OR (btrim(rights_owner) <> '' AND char_length(rights_owner) <= 200));
-- An entry that blocks automatically names who asked for it.
ALTER TABLE copyright_review_titles DROP CONSTRAINT IF EXISTS copyright_review_titles_auto_block_owner_check;
ALTER TABLE copyright_review_titles ADD CONSTRAINT copyright_review_titles_auto_block_owner_check
    CHECK (NOT auto_block OR rights_owner IS NOT NULL);

-- 2. The review: the owner named when it matched (a snapshot, like
--    matched_title) and why the system acted.
--      owner_registered  the system blocked it for the registered owner
--      mute_requested    the owner asked for a mute; held for a reviewer
ALTER TABLE copyright_title_reviews ADD COLUMN IF NOT EXISTS rights_owner TEXT;
ALTER TABLE copyright_title_reviews ADD COLUMN IF NOT EXISTS reason TEXT;
ALTER TABLE copyright_title_reviews DROP CONSTRAINT IF EXISTS copyright_title_reviews_reason_check;
ALTER TABLE copyright_title_reviews ADD CONSTRAINT copyright_title_reviews_reason_check
    CHECK (reason IS NULL OR reason IN ('owner_registered', 'mute_requested'));

-- An owner match (a provider's fingerprint hit) names no text field.
ALTER TABLE copyright_title_reviews DROP CONSTRAINT IF EXISTS copyright_title_reviews_matched_field_check;
ALTER TABLE copyright_title_reviews ADD CONSTRAINT copyright_title_reviews_matched_field_check
    CHECK (matched_field IN ('title', 'description', 'hashtag', 'owner_match'));

-- A block is a reviewer's (decided_by set) or the system's for a registered
-- owner (decided_by NULL, reason owner_registered). Nothing else may block
-- without a named reviewer.
ALTER TABLE copyright_title_reviews DROP CONSTRAINT IF EXISTS copyright_title_reviews_decision_shape;
ALTER TABLE copyright_title_reviews ADD CONSTRAINT copyright_title_reviews_decision_shape CHECK (
    (state = 'pending' AND decided_at IS NULL AND decided_by IS NULL)
    OR (state = 'blocked' AND decided_at IS NOT NULL
        AND (decided_by IS NOT NULL OR reason = 'owner_registered'))
    OR (state = 'cleared' AND decided_at IS NOT NULL));

-- 3. History: the system's block and an owner match are transitions too.
ALTER TABLE copyright_title_review_events DROP CONSTRAINT IF EXISTS copyright_title_review_events_action_check;
ALTER TABLE copyright_title_review_events ADD CONSTRAINT copyright_title_review_events_action_check
    CHECK (action IN ('hold', 'duration_measured', 'auto_clear', 'author_request', 'clear', 'block',
                      'auto_block', 'owner_match'));

-- 4. Tracked owner matches: the video stays up, its earnings are excluded.
--    posts' search-eligibility event carries earnings_excluded while any
--    row exists, and analytics' ownership projection reads it as
--    ineligible (no money) without hiding anything.
CREATE TABLE IF NOT EXISTS copyright_owner_tracks (
    post_id       UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    title_id      UUID NOT NULL REFERENCES copyright_review_titles(id),
    rights_owner  TEXT CHECK (rights_owner IS NULL OR char_length(rights_owner) <= 200),
    reference     TEXT CHECK (reference IS NULL OR char_length(reference) <= 200),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, title_id)
);
