-- 027 — the catalogue row and the seller's offer stop being the same row.
--
-- ─── THE CONFLATION ─────────────────────────────────────────────────────
--
-- `products` currently holds two different kinds of fact in one row:
--
--   THE ITEM        title, description, brand, HSN code, country of origin,
--                   dimensions, category, typed attributes. Facts about the
--                   THING. A 2019 paperback of Midnight's Children has one
--                   ISBN, one page count, one publisher, whoever is selling
--                   it.
--
--   ONE SELLER'S    seller_id, status, visibility, approval_status,
--   OFFER           rejection_reason, published_at, condition, and the
--                   handling time. Facts about ONE SHOP'S WILLINGNESS TO
--                   SELL IT. Two shops listing that same paperback disagree
--                   about every one of them, and must be allowed to.
--
-- Because the two live in one row, a second seller listing an item the
-- catalogue already knows has to create a SECOND, duplicate item — its own
-- copy of the title, the description, the dimensions, the attributes. That is
-- how a catalogue ends up with eleven rows for one book, no two of them
-- spelled the same, none of them shareable, and a buyer comparing prices
-- across shops with no way to know they are looking at the same thing.
--
-- ─── WHAT THIS FILE DOES, AND DELIBERATELY DOES NOT DO ──────────────────
--
-- EXPAND ONLY. Nothing is dropped, nothing is narrowed, nothing stops being
-- written, and NOTHING READS `product_offers` after this migration. Every
-- legacy column on `products` keeps its value, keeps its NOT NULL, and keeps
-- being written by the service — the write paths dual-write from here on.
--
-- That is not timidity, it is the only order in which this is checkable. The
-- offer rows are created here and maintained by the dual-write; a later step
-- moves the readers over, and it can only do that safely once the two copies
-- have been shown to agree row for row over real data. A consistency checker
-- ships with the dual-write for exactly that purpose. Flipping the readers in
-- the same change as the writers would mean the first evidence that the copy
-- was wrong arrived as a buyer seeing the wrong price.
--
-- A pod still running the previous image writes `products` and not
-- `product_offers`, which leaves a product with no offer — visible to the
-- checker, repairable by re-running the backfill below, and invisible to
-- every reader, because no reader reads offers yet. There is therefore no
-- ordering requirement between this migration and the deploy in either
-- direction.

-- ─── product_offers ─────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS product_offers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The catalogue item being offered.
    product_id         UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,

    -- The shop offering it. No FK to `sellers` in this file: `products`
    -- itself carries none today, and adding one here would make this
    -- migration fail on any environment whose product estate predates the
    -- seller row it names. The pairing is enforced by the backfill's
    -- assertion and by the checker, not by a constraint that could reject
    -- data the legacy column already accepts.
    seller_id          UUID NOT NULL,

    -- The lifecycle. Same vocabularies as `products`, deliberately: this is
    -- a MOVE, and a second spelling of 'approved' on the new side is exactly
    -- the drift the checker exists to catch. The CHECK lists are copied from
    -- products_status_check / products_visibility_check /
    -- products_approval_status_check as they stand at 027.
    status             TEXT NOT NULL DEFAULT 'draft'
                           CHECK (status IN ('draft','active','paused','archived')),
    visibility         TEXT NOT NULL DEFAULT 'public'
                           CHECK (visibility IN ('public','private','password')),
    approval_status    TEXT NOT NULL DEFAULT 'draft'
                           CHECK (approval_status IN ('draft','submitted','under_review','pending',
                                                      'approved','rejected','flagged','changes_requested',
                                                      'live','hidden','archived')),
    rejection_reason   TEXT,
    published_at       TIMESTAMPTZ,

    -- Condition is an OFFER fact, not an item fact: the same book is "new"
    -- from one shop and "used" from another. It lives on `products` today
    -- and is backfilled from there.
    condition          TEXT NOT NULL DEFAULT 'new'
                           CHECK (condition IN ('new','refurbished','used')),

    -- Days between a paid order and handover to the courier. New here and
    -- nullable: it has no legacy column to inherit from, so a NULL means
    -- "this shop has not said", which is the truth for every backfilled row
    -- and must not be dressed up as a number nobody chose.
    handling_time_days INT,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One offer per shop per catalogue item. A shop wanting to sell the same
    -- item at two prices does that with two VARIANTS under one offer, not two
    -- offers — otherwise "this seller's price for this item" has no answer.
    CONSTRAINT product_offers_product_seller_key UNIQUE (product_id, seller_id)
);

-- "everything this shop offers" — the seller dashboard, the seller catalogue
-- and the payout joins.
CREATE INDEX IF NOT EXISTS idx_product_offers_seller
    ON product_offers(seller_id);

-- "who is selling this item, and which of them are live" — the buy box.
CREATE INDEX IF NOT EXISTS idx_product_offers_product_status
    ON product_offers(product_id, status);

-- ─── product_variants.offer_id ──────────────────────────────────────────
--
-- A variant is a thing one shop actually ships: a size, a colour, a SKU, a
-- price, a stock count. It belongs to an OFFER, not to the catalogue item —
-- two shops selling the same shirt each have their own "Large / Blue" with
-- their own SKU and their own stock.
--
-- Nullable for now. Every existing variant is pointed at its product's single
-- backfilled offer below, so it is nullable only in the sense that a pod on
-- the previous image can still insert a variant without one. The column is
-- narrowed to NOT NULL in a later, gated step, once nothing writes a variant
-- without an offer.
ALTER TABLE product_variants
    ADD COLUMN IF NOT EXISTS offer_id UUID REFERENCES product_offers(id);

CREATE INDEX IF NOT EXISTS idx_product_variants_offer
    ON product_variants(offer_id) WHERE offer_id IS NOT NULL;

-- ─── product_attributes.offer_id gets its foreign key ───────────────────
--
-- 026 added this column WITHOUT a reference, and said why: `product_offers`
-- did not exist, and a column with no FK is a column that can hold a dangling
-- id. It exists now, and every value in the column is NULL (nothing has ever
-- written it), so the constraint is added VALID with nothing to validate.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
                WHERE table_name = 'product_attributes' AND column_name = 'offer_id')
       AND NOT EXISTS (SELECT 1 FROM pg_constraint
                        WHERE conname = 'product_attributes_offer_id_fkey')
    THEN
        ALTER TABLE product_attributes
            ADD CONSTRAINT product_attributes_offer_id_fkey
            FOREIGN KEY (offer_id) REFERENCES product_offers(id);
    END IF;
END $$;

-- ─── The 1:1 backfill ───────────────────────────────────────────────────
--
-- Every product that exists today was listed by exactly one seller, so every
-- product becomes a catalogue row with exactly ONE offer carrying the
-- lifecycle values it has right now.
--
-- ON CONFLICT DO NOTHING rather than DO UPDATE: this file is re-runnable, and
-- on a re-run the offer rows may already have diverged legitimately — a
-- second seller's offer, a lifecycle change since. Overwriting them from the
-- legacy columns would silently undo the very thing the later steps move to.
-- Repair is the checker's job and it reports rather than rewrites.
INSERT INTO product_offers (
        id, product_id, seller_id, status, visibility, approval_status,
        rejection_reason, published_at, condition, created_at, updated_at)
SELECT gen_random_uuid(), p.id, p.seller_id, p.status, p.visibility, p.approval_status,
       p.rejection_reason, p.published_at, p.condition, p.created_at, p.updated_at
  FROM products p
 ON CONFLICT (product_id, seller_id) DO NOTHING;

-- Every existing variant points at its product's offer. A product has exactly
-- one offer at this point, so the join is unambiguous; the seller_id predicate
-- is belt-and-braces against a re-run in a world where it is not.
UPDATE product_variants v
   SET offer_id = o.id
  FROM product_offers o
  JOIN products p ON p.id = o.product_id
 WHERE v.product_id = o.product_id
   AND o.seller_id = p.seller_id
   AND v.offer_id IS NULL;

-- ─── The assertion ──────────────────────────────────────────────────────
--
-- A silent partial backfill is the failure mode that matters here. It does
-- not break anything today — nothing reads offers — so it would sit,
-- undetected, until the step that flips the readers over, and then present
-- itself as a subset of the catalogue disappearing, or as a buyer being shown
-- someone else's price. Six weeks later, reported by a customer.
--
-- So this migration refuses to be recorded as applied unless the counts line
-- up. The whole file is one transaction (the runner wraps it), so a RAISE
-- here rolls the table, the column and the backfill back together and the
-- operator sees which count disagreed.
DO $$
DECLARE
    n_products        BIGINT;
    n_offers          BIGINT;
    n_without_offer   BIGINT;
    n_mismatched      BIGINT;
    n_variants        BIGINT;
    n_variants_unset  BIGINT;
BEGIN
    SELECT count(*) INTO n_products FROM products;
    SELECT count(*) INTO n_offers   FROM product_offers;

    SELECT count(*) INTO n_without_offer
      FROM products p
     WHERE NOT EXISTS (SELECT 1 FROM product_offers o
                        WHERE o.product_id = p.id AND o.seller_id = p.seller_id);

    -- Every offer must agree with the row it was copied from. This catches a
    -- re-run over a half-written estate, where the INSERT skipped rows that
    -- ON CONFLICT matched but whose values no longer correspond.
    SELECT count(*) INTO n_mismatched
      FROM products p
      JOIN product_offers o
        ON o.product_id = p.id AND o.seller_id = p.seller_id
     WHERE (o.status, o.visibility, o.approval_status)
           IS DISTINCT FROM (p.status, p.visibility, p.approval_status)
        OR o.published_at IS DISTINCT FROM p.published_at;

    SELECT count(*) INTO n_variants FROM product_variants;
    SELECT count(*) INTO n_variants_unset FROM product_variants WHERE offer_id IS NULL;

    IF n_without_offer <> 0 THEN
        RAISE EXCEPTION
            '027 backfill incomplete: % of % product(s) have no offer for their own seller',
            n_without_offer, n_products;
    END IF;

    IF n_offers < n_products THEN
        RAISE EXCEPTION
            '027 backfill incomplete: % offer(s) for % product(s)', n_offers, n_products;
    END IF;

    IF n_mismatched <> 0 THEN
        RAISE EXCEPTION
            '027 backfill inconsistent: % offer(s) disagree with the product row they were copied from',
            n_mismatched;
    END IF;

    IF n_variants_unset <> 0 THEN
        RAISE EXCEPTION
            '027 backfill incomplete: % of % variant(s) still have no offer_id',
            n_variants_unset, n_variants;
    END IF;

    RAISE NOTICE '027 backfill: % product(s), % offer(s), % variant(s) linked',
        n_products, n_offers, n_variants;
END $$;
