-- count-duplicate-matches.sql — READ-ONLY review of duplicate open dating matches.
--
-- Run against staging/prod BEFORE a deploy that sets
-- DATING_DEDUPE_OPEN_MATCHES=true:
--
--   psql "$DATING_POSTGRES_DSN" -f scripts/count-duplicate-matches.sql
--
-- A pair (user_a, user_b) is a duplicate group when it holds more than one
-- open match (status matched / conversing / quiet). extra_rows is what the
-- dating-service boot cleanup would close: every open match in the group
-- except the one kept (the match with a conversation, then the most recent
-- activity, then the lowest id). The first result is the same aggregate as
-- store.CountDuplicateOpenMatches.
--
-- Output is counts and opaque ids only (match ids and the pair's user ids):
-- no names, profile fields, messages or other personal data. Plain SQL with
-- no psql meta-commands, inside a READ ONLY transaction, so it cannot write.

BEGIN TRANSACTION READ ONLY;
SET LOCAL statement_timeout = '120s';

-- 1. Summary.
SELECT COUNT(*)::int                          AS duplicate_groups,
       COALESCE(SUM(open_count - 1), 0)::int  AS extra_rows
FROM (
    SELECT COUNT(*) AS open_count
    FROM dating_matches
    WHERE status IN ('matched','conversing','quiet')
    GROUP BY user_a, user_b
    HAVING COUNT(*) > 1
) dup;

-- 2. Groups by size (how many pairs hold 2, 3, ... open matches).
SELECT open_count, COUNT(*)::int AS pairs
FROM (
    SELECT COUNT(*) AS open_count
    FROM dating_matches
    WHERE status IN ('matched','conversing','quiet')
    GROUP BY user_a, user_b
    HAVING COUNT(*) > 1
) dup
GROUP BY open_count
ORDER BY open_count;

-- 3. Up to 20 example groups: the pair ids, the match kept and the matches
--    the cleanup would close.
WITH ranked AS (
    SELECT id, user_a, user_b,
           COUNT(*) OVER (PARTITION BY user_a, user_b) AS open_count,
           row_number() OVER (
               PARTITION BY user_a, user_b
               ORDER BY (conversation_id IS NOT NULL) DESC,
                        COALESCE(last_message_at, matched_at) DESC,
                        id) AS rn
    FROM dating_matches
    WHERE status IN ('matched','conversing','quiet')
)
SELECT user_a,
       user_b,
       MAX(open_count)::int                               AS open_count,
       (array_agg(id) FILTER (WHERE rn = 1))[1]           AS kept_match_id,
       array_agg(id ORDER BY rn) FILTER (WHERE rn > 1)    AS would_close_match_ids
FROM ranked
WHERE open_count > 1
GROUP BY user_a, user_b
ORDER BY MAX(open_count) DESC, user_a, user_b
LIMIT 20;

-- 4. Whether the unique index already exists (true means no duplicates can
--    exist and no cleanup is needed).
SELECT to_regclass('uq_dating_matches_open_pair') IS NOT NULL AS open_pair_index_exists;

ROLLBACK;
