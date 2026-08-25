-- Module 4 M4-P0-4 — canonical story media and durable moderation state.
--
-- WHAT WAS WRONG
--
-- `stories` (migration 002) stored a caller-supplied `media_url` and had no
-- moderation state at all. Two consequences:
--
--   1. The bytes a story pointed at were whatever string the client sent, so
--      the story record had no owned relationship to any media asset this
--      platform processed, scanned, or is allowed to serve.
--   2. A story was publishable the instant it was inserted. Nothing in
--      trust-safety-service has ever referenced stories, so story media was
--      the one publication path on the platform with no review state.
--
-- WHAT THIS MIGRATION ESTABLISHES
--
-- A story references a canonical media asset by id, and carries a constrained
-- moderation state that defaults to NON-PUBLISHABLE. Reads filter on it. There
-- is deliberately no flag that can loosen the gate: per the Module 4 approval,
-- if provider capacity stalls the answer is to hold items or stop accepting
-- uploads, never to publish unreviewed media.

-- ── 1. Canonical media reference ───────────────────────────────────────────
--
-- Nullable because legacy rows have no media_id and are being retired below
-- rather than deleted. New rows are required to carry one; that is enforced in
-- the application insert path and by the CHECK in step 4.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS media_id UUID;

-- ── 2. Moderation state ────────────────────────────────────────────────────
--
-- `pending`       — created, awaiting a decision. Not publishable.
-- `approved`      — a revision-matched terminal decision approved it.
-- `rejected`      — a terminal decision refused it. Not publishable, ever.
-- `manual_review` — provider could not decide, or the item was held. Not
--                   publishable while it sits here.
--
-- DEFAULT 'pending' is the load-bearing part: a row inserted by any path that
-- does not know about moderation is non-publishable rather than public.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS moderation_state TEXT NOT NULL DEFAULT 'pending';

-- content_revision is monotonic per story and is what a decision must match.
-- A decision carrying a stale revision cannot approve content that changed
-- after it was evaluated.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS content_revision BIGINT NOT NULL DEFAULT 1;

-- Decision evidence. Kept on the story so an operator can answer "why is this
-- visible" without joining another service's store.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS moderated_at TIMESTAMPTZ;
ALTER TABLE stories ADD COLUMN IF NOT EXISTS moderation_decision_id TEXT;
ALTER TABLE stories ADD COLUMN IF NOT EXISTS moderation_reason TEXT;
ALTER TABLE stories ADD COLUMN IF NOT EXISTS moderation_policy_version TEXT;

-- Soft deletion, so a deleted story stops being served without the row (and
-- its media reference) vanishing from under an in-flight decision.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

-- ── 3. Retire every legacy row ─────────────────────────────────────────────
--
-- Module 4 approval, Decision 3: expire and make non-publishable, do not
-- grandfather. There is no moderation evidence for any pre-existing story, so
-- approving them would be asserting a fact nobody established.
--
-- `manual_review` rather than `rejected`: these are held, not judged. Nothing
-- is deleted, so a human can still triage them.
--
-- Clearing is_highlight matters independently. A highlight deliberately
-- outlives the 24-hour expiry, so a legacy highlight left set would preserve
-- exactly the unreviewed content this step exists to retire — permanently, and
-- past its expiry.
UPDATE stories
   SET moderation_state = 'manual_review',
       is_highlight     = FALSE,
       highlight_group  = NULL,
       expires_at       = LEAST(expires_at, now())
 WHERE moderation_state = 'pending'
   AND media_id IS NULL;

-- ── 4. Constrain the state and the new-row contract ────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'stories_moderation_state_chk'
    ) THEN
        ALTER TABLE stories ADD CONSTRAINT stories_moderation_state_chk
            CHECK (moderation_state IN ('pending','approved','rejected','manual_review'));
    END IF;

    -- A row may lack media_id only if it is a retired legacy row. Any row that
    -- is or could become publishable must reference canonical media.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'stories_media_or_retired_chk'
    ) THEN
        ALTER TABLE stories ADD CONSTRAINT stories_media_or_retired_chk
            CHECK (media_id IS NOT NULL OR moderation_state = 'manual_review');
    END IF;
END $$;

-- ── 5. Reference integrity against the canonical asset ─────────────────────
--
-- Same mechanism and same reasoning as migration 030: only a foreign key makes
-- the orphan-media reclaimer and a concurrent story insert interact. A
-- `WHERE NOT EXISTS` check is evaluated against the statement snapshot under
-- READ COMMITTED, so a story created between snapshot and delete is invisible
-- and its media would be reclaimed out from under it.
--
-- This FAILS LOUDLY if media_assets is absent rather than skipping, because a
-- migration recorded as applied while installing no constraint is how the
-- invariant silently disappears (see 030's header for the full account).
DO $$
DECLARE
    parent_oid oid := to_regclass('public.media_assets');
BEGIN
    IF parent_oid IS NULL THEN
        RAISE EXCEPTION
            'media_assets does not exist; post-service must not run without story media reference integrity. '
            'Start media-service first so it can bootstrap its schema, then restart post-service.';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'stories_media_id_fkey'
    ) THEN
        ALTER TABLE stories ADD CONSTRAINT stories_media_id_fkey
            FOREIGN KEY (media_id) REFERENCES public.media_assets(id) ON DELETE RESTRICT;
    END IF;
END $$;

-- ── 6. Indexes derived from the authorized queries ─────────────────────────
--
-- The feed and author queries both filter author + publishable + live, so the
-- partial index carries the moderation predicate rather than leaving it to a
-- filter step over every one of an author's historical stories.
CREATE INDEX IF NOT EXISTS idx_stories_author_live
    ON stories (author_id, created_at DESC)
    WHERE moderation_state = 'approved' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_stories_owner_status
    ON stories (author_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_stories_media_id
    ON stories (media_id) WHERE media_id IS NOT NULL;

-- ── 7. Idempotency key ─────────────────────────────────────────────────────
--
-- A retried create must return the same story rather than producing a second
-- pending story and a second moderation request. Unique per author so two
-- users cannot collide on a client-generated key.
ALTER TABLE stories ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uq_stories_author_idempotency
    ON stories (author_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
