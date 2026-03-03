-- Remove duplicate follows, keeping the oldest entry.
-- (The primary key on (follower_id, followee_id) should already prevent duplicates,
--  but this guards against any data inconsistency before adding the explicit constraint.)
DELETE FROM follows a
USING follows b
WHERE a.ctid > b.ctid
  AND a.follower_id = b.follower_id
  AND a.followee_id = b.followee_id;

-- Add unique constraint (idempotent via DO block for Postgres < 16 compatibility).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'follows_unique'
    ) THEN
        ALTER TABLE follows ADD CONSTRAINT follows_unique UNIQUE (follower_id, followee_id);
    END IF;
END$$;
