-- 031 — a SKU stops being a global name and becomes a shop's own code.
--
-- ─── WHAT IS WRONG WITH UNIQUE(sku) ─────────────────────────────────────
--
-- `product_variants.sku` has been UNIQUE across the whole table since the
-- schema was first written. That reads as a data-integrity rule and is not
-- one. A SKU is a string a SHOP chooses to name its own stock: two shops
-- selling the same textbook both derive a code from the same ISBN, two shops
-- running the same inventory software both emit `SKU-00017`, and neither has
-- done anything wrong. A global unique index makes whichever of them typed it
-- second wrong, for no reason a seller could be told.
--
-- It is also the constraint that makes a SHARED CATALOGUE impossible. The
-- whole point of splitting `product_offers` out in 027 is that two shops can
-- offer the same catalogue item — and a shop's offer is made of variants, and
-- its variants carry its SKUs. With `UNIQUE(sku)` the second shop to list an
-- item cannot create the variants that would make its offer sellable.
--
-- ─── WHAT REPLACES IT ───────────────────────────────────────────────────
--
--     UNIQUE NULLS NOT DISTINCT (offer_id, sku)
--
-- One shop cannot use one code twice; two shops can use the same code. That
-- is the rule sellers already believe is in force.
--
-- NULLS NOT DISTINCT is not decoration. `product_variants.offer_id` is still
-- nullable — 027 left it that way for the length of a rolling deploy, and a
-- pod on an older image can still insert a variant without one. Under the
-- default (NULLS DISTINCT) every such row would compare unequal to every
-- other, so a table full of offer-less variants would have NO sku constraint
-- at all, and the migration that was supposed to narrow a rule would have
-- quietly removed one. NULLS NOT DISTINCT keeps the offer-less rows unique
-- among themselves, which is the strongest thing still true about them.
-- Postgres 15+; this stack runs 16.
--
-- ─── WHY IT IS SAFE TO WIDEN THIS TODAY, AND WAS NOT BEFORE ─────────────
--
-- Widening a uniqueness rule removes a guard, and something was leaning on
-- this one. The bulk importer used to resolve a SKU without naming a seller:
-- it asked "which variant in the world has this code" and updated whatever
-- came back. What stopped that from being a catalogue takeover by file upload
-- was not the importer — it was `UNIQUE(sku)`, which made the second shop's
-- insert fail before it could reach anybody else's listing.
--
-- Step 10 fixed the importer: `ResolveSKUForSeller` scopes the lookup to the
-- caller, `SKUMatch.TakenByAnother` names the third outcome, and
-- `updateExistingVariant` re-asserts the ownership before it writes. That
-- re-check was run against this schema again before this file was written.
-- The order matters and only works one way round: scope the importer, THEN
-- widen the constraint. Widened first, an unscoped importer stops failing and
-- starts overwriting, and the failure is silent.
--
-- ─── EXPAND, THEN CONTRACT, IN THAT ORDER ───────────────────────────────
--
-- The new index is created BEFORE the old constraint is dropped, so there is
-- no instant in this transaction when a duplicate SKU could be inserted. The
-- creation cannot fail on existing data: `sku` was globally unique a moment
-- ago, so every (offer_id, sku) pair — offer-less rows included — is already
-- distinct.
--
-- The drop is the part that does not roll back cleanly. Once duplicate SKUs
-- exist across shops, re-adding `UNIQUE(sku)` will fail, and it should: by
-- then the duplicates are legitimate listings and the constraint is the thing
-- that was wrong. Rolling BACK the service image is unaffected — no code
-- depends on the insert failing; `asDuplicateSKU` matches on a constraint
-- name containing "sku", and the new index is named so that it still does.

-- ─── The new rule ───────────────────────────────────────────────────────

CREATE UNIQUE INDEX IF NOT EXISTS product_variants_offer_sku_key
    ON product_variants (offer_id, sku) NULLS NOT DISTINCT;

-- ─── The old one ────────────────────────────────────────────────────────

ALTER TABLE product_variants
    DROP CONSTRAINT IF EXISTS product_variants_sku_key;

-- `idx_variants_sku`, the plain btree on `sku` alone, is deliberately KEPT.
-- It was never the uniqueness rule; it is what `ResolveSKUForSeller` looks a
-- code up on, and that lookup gets MORE important once a code can appear more
-- than once, not less.

-- ─── The assertion ──────────────────────────────────────────────────────
--
-- Two things have to be true for this file to have done what it says: the new
-- index exists, and the old constraint is gone. Asserted rather than assumed
-- because the failure mode is silent in both directions — a create that ought
-- to have been refused, or a second seller still unable to list — and neither
-- shows up until somebody tries.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_indexes
                    WHERE tablename = 'product_variants'
                      AND indexname = 'product_variants_offer_sku_key') THEN
        RAISE EXCEPTION '031: product_variants_offer_sku_key was not created';
    END IF;

    IF EXISTS (SELECT 1 FROM pg_constraint
                WHERE conname = 'product_variants_sku_key') THEN
        RAISE EXCEPTION '031: the global UNIQUE(sku) is still in force';
    END IF;

    RAISE NOTICE '031: sku is now unique per offer, not per catalogue';
END $$;
