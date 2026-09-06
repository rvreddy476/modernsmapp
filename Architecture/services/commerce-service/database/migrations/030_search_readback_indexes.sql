-- 030 — indexes for the search read-back.
--
-- No new columns and no new tables: the search document is assembled
-- entirely from what migrations 007, 026 and 027 already put in place
-- (`attributes_doc`, `source_image_url`, the minor-unit money columns, the
-- category tree). What it needs is two indexes, one per new access pattern.
--
-- Both are CREATE INDEX IF NOT EXISTS, so this file is safe to re-apply and
-- safe on a database that already has them.

-- ─── 1. The reindex walk ────────────────────────────────────────────────
--
-- GET /v1/commerce/internal/products/search-docs pages the LIVE catalogue
-- keyset-style, ordered by (created_at, id), filtered by the shopper-facing
-- visibility rule. Without an index matching both the filter and the order,
-- every page is a sequential scan of `products` plus a sort — and a reindex
-- is the one operation that reads every page.
--
-- A PARTIAL index, because the walk only ever asks for live listings and
-- because that is a small fraction of the table on a catalogue with drafts
-- and rejections in it. The predicate is written exactly as
-- `productSummaryLive` spells the rule, so the planner can match it.
--
-- Note this is deliberately NOT `WHERE status = 'active'` alone. That is
-- the predicate the old offline backfill used, and it indexed listings
-- awaiting moderation and listings a moderator had rejected.
CREATE INDEX IF NOT EXISTS idx_products_live_keyset
    ON products (created_at, id)
    WHERE status = 'active' AND approval_status = 'approved';

COMMENT ON INDEX idx_products_live_keyset IS
    'Keyset walk for the search reindex: the live catalogue in (created_at, id) order. '
    'Partial on the shopper-facing visibility rule, which is the only set the walk reads.';

-- ─── 2. The facet definitions ───────────────────────────────────────────
--
-- GET /v1/commerce/internal/search-facets reads the active, filterable
-- attribute definitions on every faceted product query (search-service
-- caches it for ~60s, so this is not per-request, but it is per-minute
-- per-pod and the answer is a handful of rows out of a table the console
-- keeps adding to).
--
-- Partial on the same two booleans the query filters by, so the index holds
-- only the definitions an operator has actually turned into facets.
CREATE INDEX IF NOT EXISTS idx_attribute_definitions_filterable
    ON attribute_definitions (display_group, code)
    WHERE is_active AND is_filterable;

COMMENT ON INDEX idx_attribute_definitions_filterable IS
    'The facet rail: active + filterable definitions, in the order a filter rail draws them. '
    '`is_filterable` is an operator checkbox, which is what makes adding a facet a no-deploy change.';
