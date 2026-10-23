-- 019_copyright_blocks.sql: upheld copyright blocks and the repeat-uploader
-- rule (founder quick wins, 2026-10-24).
--
-- post-service's copyright title review publishes
-- post.copyright_review.changed; a "blocked" transition (a reviewer upheld
-- the block) becomes ONE row here, keyed by the review, so a redelivered
-- event is the same block. The standing answer counts these together with
-- trust-safety's own upheld copyright cases (trust.copyright_cases in
-- hold_active) and strikes in strike_group 'copyright':
--
--   3 upheld blocks within 90 days → long-video uploads paused for 7 days
--   from the block that made the third (standing.long_video_paused_until).
--
-- It is deliberately NOT a trust.user_strikes row: a strike of any severity
-- is an "active strike" to the Action-button and membership eligibility
-- checks, and the founder's rule pauses long video only — reels and posts
-- are unaffected.
--
-- A block is voided, never deleted (voided_at / void_reason / voided_by).
-- Re-runnable: every statement is IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS trust.copyright_blocks (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    post_id      UUID NOT NULL,
    source       TEXT NOT NULL CHECK (source IN ('title_review')),
    source_ref   UUID NOT NULL,            -- post-service's review id
    matched_title TEXT,
    decided_by   UUID,                     -- the reviewer who upheld it
    blocked_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    voided_at    TIMESTAMPTZ,
    void_reason  TEXT,
    voided_by    UUID,
    CONSTRAINT copyright_blocks_one_per_source UNIQUE (source, source_ref),
    CONSTRAINT copyright_blocks_void_shape CHECK (
        (voided_at IS NULL AND void_reason IS NULL AND voided_by IS NULL)
        OR (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> '')),
    CONSTRAINT copyright_blocks_ids_not_nil CHECK (
        user_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND post_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE INDEX IF NOT EXISTS idx_copyright_blocks_user
    ON trust.copyright_blocks (user_id, blocked_at DESC) WHERE voided_at IS NULL;
