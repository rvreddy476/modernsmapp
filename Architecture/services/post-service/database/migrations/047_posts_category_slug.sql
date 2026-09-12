-- Migration 047: posts.category and reel_drafts.category in slug form (2026-09-12).
--
-- posts.category was stored exactly as the client sent it, so dev held
-- "Education" and "education", "Technology" and "tech", as separate values.
-- feed-service's category pages (GET /v1/feed/videos?category=) and
-- post-service's GetRecentPosts filter compare the stored string exactly, so
-- mixed-case uploads fell into separate buckets and some pages came up empty.
--
-- From this migration on, every write path runs service.NormalizeCategory:
-- trim, lowercase, and collapse any internal run of whitespace to one hyphen
-- (a filter value can only ever be a `[a-z0-9][a-z0-9_-]*` slug, so a stored
-- value with a space inside could never be asked for). This file applies the
-- same rule to the rows already stored, so the code rule and the data agree.
--
-- Synonyms are NOT folded ("Technology" is left as "technology", not mapped
-- to "tech"): no synonym list exists, and a guess here would be a silent
-- remap nobody asked for. Fold them by hand if and when a list is agreed.
--
-- Empty stays '' rather than NULL, on purpose. Migration 006 declared both
-- columns `TEXT DEFAULT ''`, and the Go side scans posts.category into a
-- plain string (postScanDestinations in internal/store/postgres/posts.go);
-- pgx cannot scan NULL into *string, so a NULL here would make every read of
-- an uncategorised post fail. Whitespace-only values are folded to '' so
-- they stop looking like a category to the filter.
--
-- Idempotent: each UPDATE only touches rows the rule would change, so a
-- re-run is a no-op.

UPDATE posts
   SET category = regexp_replace(lower(btrim(category)), '\s+', '-', 'g')
 WHERE category IS NOT NULL
   AND category <> regexp_replace(lower(btrim(category)), '\s+', '-', 'g');

UPDATE reel_drafts
   SET category = regexp_replace(lower(btrim(category)), '\s+', '-', 'g')
 WHERE category IS NOT NULL
   AND category <> regexp_replace(lower(btrim(category)), '\s+', '-', 'g');

-- No index is added. The only SQL that filters on posts.category is
-- GetRecentPosts (`AND category = $n`, a plain equality on the already
-- normalised value), which feed-service calls for its first-page discovery
-- fill; feed-service's own category filter runs in memory after hydration,
-- against the post it fetched from post-service, not against a Scylla or
-- OpenSearch column. A functional lower(category) index would therefore be
-- unused. If that fill query ever shows in slow logs, the useful index is a
-- plain partial btree on posts(category, created_at DESC) for approved public
-- long videos, decided against EXPLAIN output rather than added blind here.
