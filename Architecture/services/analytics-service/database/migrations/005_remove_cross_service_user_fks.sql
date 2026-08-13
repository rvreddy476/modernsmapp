-- Existing shared-database installations may have applied migration 003 when
-- a public.users table happened to exist. Remove those cross-service foreign
-- keys so identity deletion cannot be blocked by an analytics projection.

ALTER TABLE IF EXISTS user_streaks
    DROP CONSTRAINT IF EXISTS user_streaks_user_id_fkey;
ALTER TABLE IF EXISTS user_badges
    DROP CONSTRAINT IF EXISTS user_badges_user_id_fkey;
ALTER TABLE IF EXISTS loyalty_points
    DROP CONSTRAINT IF EXISTS loyalty_points_user_id_fkey;
ALTER TABLE IF EXISTS point_transactions
    DROP CONSTRAINT IF EXISTS point_transactions_user_id_fkey;

