-- Remove legacy foreign keys that tied the monetization database to a
-- coincidentally shared public.users table. Identity lifecycle is event-driven
-- and must not block on financial projections.

ALTER TABLE IF EXISTS affiliate_links
    DROP CONSTRAINT IF EXISTS affiliate_links_creator_id_fkey;
ALTER TABLE IF EXISTS fundraisers
    DROP CONSTRAINT IF EXISTS fundraisers_creator_id_fkey;
ALTER TABLE IF EXISTS donations
    DROP CONSTRAINT IF EXISTS donations_donor_id_fkey;

