-- Module 1 fixes-v1 / Codex P1-3 + P1-4.
--
-- P1-3: immediate publication only READ the draft row; there was no
-- compare-and-set, so publish-vs-delete and publish-vs-edit could
-- interleave (a delete could land after the publisher's read and the post
-- would still be created; an edit could publish stale content). Every
-- publication path now takes the same atomic claim, and finalize/block/
-- release are conditional on holding that claim token.
ALTER TABLE post_drafts
    ADD COLUMN IF NOT EXISTS claim_token UUID;

-- P1-4: the quota was COUNT(*) then INSERT in a plain transaction, so two
-- concurrent creates could both observe 99 and both insert. A per-author
-- advisory lock serializes the check-and-insert without locking the table.
-- (Kept as a comment for reviewers: the lock is taken in code via
-- pg_advisory_xact_lock(hashtext('post_drafts:' || author_id)).)

-- P1-4: draft media must be reclaimable. Track what a draft references so
-- deleting it can release media that no published post and no other draft
-- still uses.
CREATE TABLE IF NOT EXISTS post_draft_media (
    draft_id   UUID NOT NULL REFERENCES post_drafts(id) ON DELETE CASCADE,
    media_id   UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (draft_id, media_id)
);
CREATE INDEX IF NOT EXISTS idx_post_draft_media_media ON post_draft_media (media_id);
