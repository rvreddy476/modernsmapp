-- Cross-service media claims (2026-10-25).
--
-- Services that store a media id in their OWN tables (channel-service's
-- channel_updates.media_ids, broadcast_channels.avatar_media_id, ...) carry
-- no foreign key into media_assets and take no lock on its row. The owner's
-- DELETE /v1/media/{id} could therefore remove an asset another service still
-- shows, leaving that reference dangling.
--
-- A claim is media-service's own record of such a reference, written through
-- the keyed internal route POST /v1/media/internal/claims before the claimant
-- writes its row, and removed through /claims/release after it stops
-- referencing the asset. The owner delete refuses while any claim exists.
--
-- The foreign key is ON DELETE RESTRICT and load-bearing: inserting a claim
-- takes a key-share lock on the media row, so a claim and a physical delete
-- serialise on that row — one of them wins, never both.
--
--   claimant    the service holding the reference (e.g. 'channel-service').
--   claim_kind  what holds it there ('channel_update', 'channel_avatar', ...).
--   claim_ref   the id of the holding row in the claimant's database.
--
-- One row per (asset, holder): registering the same claim twice is a no-op.
CREATE TABLE IF NOT EXISTS media_claims (
    media_id   UUID        NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    claimant   TEXT        NOT NULL CHECK (claimant <> ''),
    claim_kind TEXT        NOT NULL CHECK (claim_kind <> ''),
    claim_ref  TEXT        NOT NULL CHECK (claim_ref <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_id, claimant, claim_kind, claim_ref)
);

-- Release is by holder ("everything update X holds"), not by asset.
CREATE INDEX IF NOT EXISTS idx_media_claims_holder
    ON media_claims (claimant, claim_kind, claim_ref);
