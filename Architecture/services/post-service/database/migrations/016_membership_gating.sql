-- Tier 3c: Membership gating on posts.
--
-- A creator can mark a post as "members-only at tier X" by setting
-- tier_required_id to one of their own creator_tiers rows. Reads then
-- redact the post body for callers who don't have an active
-- subscription that meets-or-exceeds that tier price.
--
-- The column is nullable; NULL means public (current default behaviour).
-- No FK to creator_tiers because that table lives in monetization-db,
-- a separate logical database. Integrity is enforced at write time by
-- the post-service handler, which calls monetization-service to
-- validate the tier belongs to the post author.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS tier_required_id UUID;

CREATE INDEX IF NOT EXISTS idx_posts_tier_required
    ON posts (tier_required_id)
    WHERE tier_required_id IS NOT NULL;
