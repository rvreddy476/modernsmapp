-- 012_appeal_decision_binding.sql: an ordinary appeal is bound to the
-- decision it challenges (Copyright Match plan, section 4 P-3, section 6.3).
--
-- Before this file an appeal named only the post. Adjudication read the
-- post's revision fresh, so the overturn's fence covered milliseconds, and
-- the canonical approve ran BEFORE the local transition, so an uphold and
-- an overturn racing each other could leave the appeal upheld and the post
-- approved. Now:
--
--   * appealed_decision_id is post-service's base decision at submission
--     (NULL until post-service exposes it on the moderation subject, and
--     for rows that pre-date this file) and appealed_revision is the post's
--     content revision at submission. The overturn is signed with THAT
--     revision, never a fresh one.
--   * status gains 'overturning' (the canonical approve is in flight; the
--     appeal can no longer be upheld) and 'superseded' (a later decision on
--     the post replaced the one appealed; the appeal is closed without an
--     outcome and never overturns).
--   * overturn_decision_id is the moderationcap decision id the approve is
--     sent under (the appeal id), overturn_started_at is when the appeal
--     entered 'overturning' (the sweeper replays in-flight approves older
--     than a grace period) and overturn_applied_at is when post-service
--     acknowledged it. Each is set once; a replay finds them set and
--     writes nothing.
--   * The one-active-appeal index counts 'overturning' as active.
--
-- Re-runnable: the integration helpers apply every migration file on each
-- open, so every statement is IF NOT EXISTS or DO-guarded.

ALTER TABLE trust.content_appeals
    ADD COLUMN IF NOT EXISTS appealed_decision_id UUID,
    ADD COLUMN IF NOT EXISTS appealed_revision    BIGINT,
    ADD COLUMN IF NOT EXISTS overturn_decision_id UUID,
    ADD COLUMN IF NOT EXISTS overturn_started_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS overturn_applied_at  TIMESTAMPTZ;

-- Rows overturned before this file: the decision id was always the appeal
-- id, and the acknowledgement time is the best record we have.
UPDATE trust.content_appeals
   SET overturn_decision_id = COALESCE(overturn_decision_id, id),
       overturn_started_at  = COALESCE(overturn_started_at, resolved_at, submitted_at),
       overturn_applied_at  = COALESCE(overturn_applied_at, resolved_at, submitted_at)
 WHERE status = 'overturned'
   AND (overturn_decision_id IS NULL OR overturn_started_at IS NULL OR overturn_applied_at IS NULL);

-- The status CHECK was declared inline on the column (004), so it carries
-- the generated name.
ALTER TABLE trust.content_appeals DROP CONSTRAINT IF EXISTS content_appeals_status_check;
ALTER TABLE trust.content_appeals ADD CONSTRAINT content_appeals_status_check
    CHECK (status IN ('open', 'under_review', 'overturning', 'upheld', 'overturned', 'superseded', 'expired'));

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'content_appeals_revision_positive') THEN
        ALTER TABLE trust.content_appeals ADD CONSTRAINT content_appeals_revision_positive
            CHECK (appealed_revision IS NULL OR appealed_revision > 0);
    END IF;
    -- In flight or done: the decision id is fixed. Done: acknowledged.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'content_appeals_overturn_shape') THEN
        ALTER TABLE trust.content_appeals ADD CONSTRAINT content_appeals_overturn_shape CHECK (
            (status NOT IN ('overturning', 'overturned'))
            OR (status = 'overturning' AND overturn_decision_id IS NOT NULL AND overturn_started_at IS NOT NULL
                AND reviewed_by IS NOT NULL)
            OR (status = 'overturned' AND overturn_decision_id IS NOT NULL AND overturn_started_at IS NOT NULL
                AND overturn_applied_at IS NOT NULL));
    END IF;
END $$;

-- 008's one-active-appeal index must also hold while an overturn is in
-- flight, or a second appeal could be opened under it.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = 'trust' AND indexname = 'uq_appeals_one_active_per_user_content'
           AND indexdef LIKE '%overturning%'
    ) THEN
        DROP INDEX IF EXISTS trust.uq_appeals_one_active_per_user_content;
        CREATE UNIQUE INDEX uq_appeals_one_active_per_user_content
            ON trust.content_appeals (user_id, content_type, content_id)
            WHERE status IN ('open', 'under_review', 'overturning');
    END IF;
END $$;

-- The sweeper that replays in-flight overturns reads by status and age.
CREATE INDEX IF NOT EXISTS idx_appeals_overturning
    ON trust.content_appeals (overturn_started_at) WHERE status = 'overturning';
