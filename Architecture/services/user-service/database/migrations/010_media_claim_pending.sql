-- 010_media_claim_pending.sql: holders whose media-service claims need
-- settling (2026-10-25).
--
-- user-service registers every media id its rows show (channels' avatar and
-- banner, portfolio_items.media_id, business_pages' avatar and cover) as a
-- claim in media-service, so the uploader cannot delete the asset out from
-- under the row (internal/service/media_claims.go). Channels and portfolio
-- items are HARD-deleted: once the row is gone nothing here remembers that a
-- claim was ever taken, so a release lost to a media-service outage would pin
-- the owner's media forever.
--
-- A row here means "reconcile this holder's claims with what the table holds
-- now". It is written in the SAME transaction as the delete (or the page
-- disable, or the account purge), and whenever a best-effort release fails;
-- the in-server drain and cmd/media-claims settle it and remove it. One row
-- per holder; a repeat bumps enqueued_at, which the drain uses so it never
-- removes an entry written after the one it settled.
CREATE TABLE IF NOT EXISTS media_claim_pending (
    claim_kind  TEXT        NOT NULL CHECK (claim_kind <> ''),
    claim_ref   UUID        NOT NULL,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (claim_kind, claim_ref)
);

CREATE INDEX IF NOT EXISTS idx_media_claim_pending_enqueued
    ON media_claim_pending (enqueued_at);
