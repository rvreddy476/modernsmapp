-- Migration 067: channel names are unique, ignoring case (founder, 2026-10-21).
--
-- Handles were already unique (channels.handle UNIQUE). Names now are too:
-- "Raghu Reddy" and "raghu reddy" are the same name. The service answers a
-- clash with 409 CHANNEL_NAME_TAKEN and a free suggestion; a new account's
-- default channel takes the first free variant ("Name 2", "Name 3", ...).
--
-- Existing duplicates: the oldest channel keeps the name, each later one gets
-- " <n>" (cut so the result stays within the 40-character limit). Idempotent:
-- with no duplicates left the UPDATE touches nothing and the index exists.

WITH ranked AS (
    SELECT id, name,
           row_number() OVER (PARTITION BY lower(name) ORDER BY created_at, id) AS rn
    FROM channels
)
UPDATE channels c
SET name = rtrim(left(r.name, 40 - length(' ' || r.rn::text))) || ' ' || r.rn::text,
    updated_at = NOW()
FROM ranked r
WHERE c.id = r.id AND r.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS uq_channels_name_lower ON channels (lower(name));
