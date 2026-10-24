-- 020_copyright_block_overturn.sql: an overturned copyright block stops
-- counting (post-service migration 076, one appeal per block).
--
-- The author may appeal a blocked title review once; when a reviewer then
-- CLEARS it (post.copyright_review.changed state "cleared"), the block that
-- was recorded for it in 019 is voided (void_reason appeal_cleared, voided_by
-- the reviewer), so it no longer counts toward the repeat-uploader pause and
-- the pause lifts on the next standing answer if fewer than three remain.
--
-- 019 allowed one row per review EVER (copyright_blocks_one_per_source), so a
-- review blocked again after a clear could never count again. The rule is now
-- one ACTIVE (not voided) row per review: the voided row stays as the record
-- of the overturned block, and a later block of the same review is a new row.
-- RecordCopyrightBlock inserts only a block newer than every row the review
-- already has, so a redelivered old block never comes back after its void.
--
-- Re-runnable: DROP ... IF EXISTS / CREATE ... IF NOT EXISTS.

ALTER TABLE trust.copyright_blocks DROP CONSTRAINT IF EXISTS copyright_blocks_one_per_source;

CREATE UNIQUE INDEX IF NOT EXISTS uq_copyright_blocks_active_per_source
    ON trust.copyright_blocks (source, source_ref) WHERE voided_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_copyright_blocks_source_ref
    ON trust.copyright_blocks (source, source_ref, blocked_at DESC);
