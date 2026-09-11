-- 011: monetization eligibility on the ownership projection.
-- Plan Phase 1F; audit M-07.
--
-- Removed, deleted and private content kept earning because no
-- eligibility state existed anywhere analytics or monetization could
-- read. The state is projected from post-service's
-- PostSearchEligibilityChanged (plus PostDeleted / PostRestored as belt
-- and braces) by the ownership consumer, gated on the producer's
-- monotonic search_rev so a late-delivered approval cannot resurrect a
-- taken-down post.
--
-- It is dated. The daily rollup emits a row for a content item only
-- where the state is eligible or eligibility_effective_from is after
-- the day; frozen days are untouched by construction, so what was
-- earned while the content was public stands. Ingest keeps accepting
-- raw events for ineligible content, so dashboards still see them; the
-- views just never reach the summary.
--
-- Existing rows default to eligible: nothing that accrues today stops
-- accruing until post-service says so.

ALTER TABLE analytics.content_ownership
    ADD COLUMN IF NOT EXISTS eligibility_state TEXT NOT NULL DEFAULT 'eligible',
    ADD COLUMN IF NOT EXISTS eligibility_effective_from TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS eligibility_rev BIGINT NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'content_ownership_eligibility_state_check'
    ) THEN
        ALTER TABLE analytics.content_ownership
            ADD CONSTRAINT content_ownership_eligibility_state_check
            CHECK (eligibility_state IN ('eligible', 'ineligible', 'deleted'));
    END IF;
END $$;

-- The rollup joins ownership for every content item in a day; the
-- ineligible set is small and this keeps the join cheap for it.
CREATE INDEX IF NOT EXISTS idx_content_ownership_ineligible
    ON analytics.content_ownership (content_id, eligibility_effective_from)
    WHERE eligibility_state <> 'eligible';
