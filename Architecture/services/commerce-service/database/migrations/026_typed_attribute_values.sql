-- 026 — the VALUE side of category attributes, and the end of the
-- `source_image_url` abuse.
--
-- 025 added the definition registry: what a category is allowed to ask for,
-- with types, bounds, enum options and unit vocabulary. Nothing stores a
-- product's ANSWERS in a way that respects any of it. `product_attributes` is
-- still what it was on day one:
--
--     (product_id, name TEXT, value TEXT, unit TEXT, sort_order INT)
--
-- Every answer is TEXT. `pages` is the string '328', so a filter for "under
-- 400 pages" compares '328' < '400' lexically and quietly excludes '99'. There
-- is no link from a row to the definition it answers, so nothing can tell
-- `author` typed against the real definition from `author` a client invented.
-- There is no uniqueness on (product, name), so "replace the value" is a
-- delete-and-insert that races with itself and leaves two rows.
--
-- ─── AND IT IS NOT EVEN ONLY AN ATTRIBUTE TABLE ─────────────────────────
--
-- A product's fallback image URL is stashed here as a row with
-- `name = 'source_image_url'` — a fact about the product masquerading as one
-- of its specifications. Three live buyer-facing reads carry a correlated
-- subquery to dig it back out, once per product per surface:
--
--     internal/store/postgres/store.go       product detail
--     internal/store/postgres/batch.go       the cart's product batch
--     internal/store/postgres/storefront.go  the shared summary projection,
--                                            i.e. home, browse, favourites
--                                            and the seller's catalogue
--
-- So the browse grid runs one extra index-less scan of `product_attributes`
-- per tile, and the "spec block" a seller sees on their own product contains
-- a CDN URL they never typed. It gets a real column here.
--
-- ─── SHAPE OF THE CHANGE ────────────────────────────────────────────────
--
-- Expand only. `name` and `value` stay, keep their types, keep their
-- nullability, and keep working. The split is by `definition_id`:
--
--     definition_id IS NULL      a legacy / free-text row. Exactly what is
--                                there today, written by exactly the code
--                                that writes it today. Untouched, unconstrained.
--
--     definition_id IS NOT NULL  a typed row, answering a known definition,
--                                with its value in the column its data type
--                                calls for.
--
-- Every constraint added below is predicated on `definition_id IS NOT NULL`,
-- and `definition_id` is created by THIS file, so it is NULL on every row that
-- already exists. A pod still running the previous image writes name/value
-- with no definition_id and no unit_code, which is on the legacy side of every
-- one of them. There is therefore no ordering requirement between this
-- migration and the deploy, in either direction.

-- ─── product_attributes: the typed columns ──────────────────────────────

ALTER TABLE product_attributes
    -- The definition this row answers. Nullable forever: the legacy rows have
    -- no definition and are not going to acquire one.
    ADD COLUMN IF NOT EXISTS definition_id   UUID REFERENCES attribute_definitions(id),

    -- Which OFFER this value belongs to, for a definition whose `applies_to`
    -- is 'offer' — condition, warranty, the facts that differ between two
    -- sellers listing the same textbook. NULL means the value is a fact about
    -- the item itself.
    --
    -- Deliberately no foreign key: `product_offers` does not exist yet. A
    -- column with no FK is a column that can hold a dangling id, which is why
    -- nothing writes it until the table it points at is created and the
    -- constraint goes on in the same step.
    ADD COLUMN IF NOT EXISTS offer_id        UUID,

    ADD COLUMN IF NOT EXISTS value_text      TEXT,
    -- NUMERIC, not float. `money_minor` and `integer` values live here, and a
    -- double cannot hold a 19-digit paise figure exactly. (24,6) covers a
    -- measure's fractional precision without inviting a price into it.
    ADD COLUMN IF NOT EXISTS value_num       NUMERIC(24,6),
    ADD COLUMN IF NOT EXISTS value_bool      BOOLEAN,
    ADD COLUMN IF NOT EXISTS value_date      DATE,
    ADD COLUMN IF NOT EXISTS value_media_id  UUID,

    -- The unit this row's number is expressed in, e.g. 'kg'. Separate from
    -- the legacy free-text `unit` column, which is whatever a client typed;
    -- this one is checked against the definition's family in the service
    -- layer before the row is written.
    ADD COLUMN IF NOT EXISTS unit_code       TEXT,

    -- Ordinal WITHIN one (product, definition). It is 0 for every
    -- single-valued field. A `multi_enum` — "which languages is this printed
    -- in" — is stored as one row per selected option, and this is what makes
    -- those rows distinguishable to the unique index below while still being
    -- one answer to one question.
    ADD COLUMN IF NOT EXISTS position        INT NOT NULL DEFAULT 0,

    ADD COLUMN IF NOT EXISTS created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW();

COMMENT ON COLUMN product_attributes.definition_id IS
    'The attribute_definitions row this value answers. NULL marks a legacy free-text row '
    '(including the source_image_url abuse migration 026 retired); every typed-value constraint '
    'is predicated on this being NOT NULL.';
COMMENT ON COLUMN product_attributes.position IS
    'Ordinal within one (product_id, definition_id). 0 for a single-valued field; 0..n-1 for the '
    'selected options of a multi_enum.';

-- ─── Exactly one value column, and where a unit is allowed ──────────────
--
-- WHY THIS SHAPE, AND WHAT THE "EXCEPT" IN THE BRIEF ACTUALLY IS
--
-- Each data type has exactly one column that can hold it:
--
--     text, long_text, gtin, enum, multi_enum   value_text
--                                               (enum and multi_enum store the
--                                               enum OPTION CODE, not its label
--                                               — the label is presentation and
--                                               renaming it must not rewrite
--                                               every product)
--     integer, decimal, money_minor, measure    value_num
--     boolean                                   value_bool
--     date                                      value_date
--     media                                     value_media_id
--
-- So the rule for a typed row is "exactly one of the five value_* columns is
-- non-null" — never zero (a row that answers nothing is not an answer; the
-- absence of an answer is the absence of a row, which is what `required`
-- checks look for), and never two (two answers to one question, and every
-- reader picks a different one).
--
-- `unit_code` is NOT one of the five. It qualifies a number rather than being
-- a value, so it does not weaken the exactly-one rule — it needs its own,
-- second constraint saying where it may appear. A measure pairs value_num
-- with unit_code; that pairing is legal precisely because unit_code is
-- outside the count. `250` alone is not a weight, and `'red' kg` is not
-- anything, so a unit is allowed if and only if there is a number for it to
-- qualify.
--
-- multi_enum is the other half of the brief's exception, and it is not an
-- exception to THIS constraint at all — each of its rows populates value_text
-- and satisfies exactly-one on its own. What multi_enum needs is permission
-- for several ROWS to share a (product, definition), and that is granted by
-- `position` being part of the unique index below rather than by loosening
-- anything here. Writing the exception into this constraint instead would
-- have let a single-valued field store two contradictory answers.
--
-- Added VALID rather than NOT VALID, against this codebase's usual habit for
-- constraints on existing tables (007–012). The reason the habit exists is a
-- mixed fleet: an old pod writes rows the new constraint would reject. Here
-- the predicate is guarded by `definition_id IS NOT NULL`, and definition_id
-- is added by this same file — so it is NULL on every pre-existing row and on
-- every row an old pod can write. The validation scan is provably incapable of
-- finding a violation, and leaving it NOT VALID would only defer a scan that
-- cannot fail while costing a later gated migration to remember it.

ALTER TABLE product_attributes
    DROP CONSTRAINT IF EXISTS product_attributes_one_typed_value;
ALTER TABLE product_attributes
    ADD CONSTRAINT product_attributes_one_typed_value CHECK (
        definition_id IS NULL
        OR (
            (value_text     IS NOT NULL)::int
          + (value_num      IS NOT NULL)::int
          + (value_bool     IS NOT NULL)::int
          + (value_date     IS NOT NULL)::int
          + (value_media_id IS NOT NULL)::int
        ) = 1
    );

-- Guarded by `definition_id IS NOT NULL` for the same reason as the constraint
-- above, and it is worth saying why explicitly because the guard looks
-- redundant here: `unit_code` is a column THIS file adds, so no old writer can
-- populate it and an unguarded constraint would appear to be just as safe.
--
-- It is not the same claim. The guard is what makes "every constraint 026 adds
-- applies only to typed rows" true as a RULE rather than true by coincidence
-- of which columns happen to be new. Without it, the legacy half of the table
-- has one constraint on it, and the next person to reason about what a
-- pre-026 pod may write has to check each constraint individually instead of
-- reading the one invariant at the top of this file.
ALTER TABLE product_attributes
    DROP CONSTRAINT IF EXISTS product_attributes_unit_needs_number;
ALTER TABLE product_attributes
    ADD CONSTRAINT product_attributes_unit_needs_number CHECK (
        definition_id IS NULL
        OR unit_code IS NULL
        OR value_num IS NOT NULL
    );

-- ─── Indexes ────────────────────────────────────────────────────────────
--
-- One value per (product, definition, position), and only for typed rows. A
-- plain UNIQUE would forbid the legacy duplicates that legitimately exist —
-- the abuse rows are not unique on (product, name) and never were — so the
-- index is partial on the same predicate every constraint above uses.
--
-- This is what makes "replace this field's value" an UPSERT instead of a
-- delete-then-insert. The delete-then-insert is what allowed two concurrent
-- writes to leave two rows for one field.
CREATE UNIQUE INDEX IF NOT EXISTS uq_product_attributes_typed_value
    ON product_attributes (product_id, definition_id, position)
    WHERE definition_id IS NOT NULL;

-- "Which products answer this definition" — the query behind the impact count
-- a narrowing edit has to acknowledge, and behind any future facet build.
-- Without it that is a sequential scan of every attribute row in the
-- catalogue.
CREATE INDEX IF NOT EXISTS idx_product_attributes_definition
    ON product_attributes (definition_id)
    WHERE definition_id IS NOT NULL;

-- The product's own attribute block. setup.sql declares this index too, so on
-- a database built from setup.sql it is already present and IF NOT EXISTS
-- makes this a no-op — but a database that predates that line has no index on
-- the table at all, and every read of a product's specs is a full scan.
CREATE INDEX IF NOT EXISTS idx_product_attrs_product
    ON product_attributes (product_id);

-- ─── products: the read projection and three real columns ───────────────

ALTER TABLE products
    -- The denormalised code→value projection of this product's typed
    -- attributes, kept in step with the rows inside the same transaction that
    -- writes them (store.rebuildAttributesDocTx). It exists for SEARCH and
    -- FILTERING — one GIN lookup instead of a join per facet. It is NOT the
    -- source of truth and nothing user-facing reads it: product detail reads
    -- the typed rows, because the doc has no labels, no display groups and no
    -- ordering, and a projection that can drift must never be the thing a
    -- buyer sees.
    ADD COLUMN IF NOT EXISTS attributes_doc JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- The fallback image URL, at last a column. Backfilled below from the
    -- `product_attributes` rows that have been standing in for it.
    ADD COLUMN IF NOT EXISTS source_image_url TEXT,

    -- The barcode — ISBN, EAN, UPC. Promoted out of the attribute rows for
    -- the same reason as the image: it is an identity fact about the product,
    -- it needs an index to be looked up by, and an EAV row cannot have one.
    ADD COLUMN IF NOT EXISTS gtin TEXT,

    -- Which published attribute-schema version this product's values were
    -- last validated against. 0 means "never validated" — every product that
    -- exists today, since nothing has validated anything yet. A definition
    -- that tightens its bounds can then be reconciled against the products
    -- behind it without re-checking the whole catalogue.
    ADD COLUMN IF NOT EXISTS schema_version INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN products.attributes_doc IS
    'Denormalised code->value projection of the typed product_attributes rows, for search and '
    'filtering only. Rebuilt in the same transaction as any value write. Not the source of truth '
    'and not read by any buyer-facing surface.';
COMMENT ON COLUMN products.source_image_url IS
    'Fallback image URL. Replaces the product_attributes row with name=''source_image_url''.';

CREATE INDEX IF NOT EXISTS idx_products_attributes_doc
    ON products USING gin (attributes_doc);

CREATE INDEX IF NOT EXISTS idx_products_gtin
    ON products (gtin) WHERE gtin IS NOT NULL;

-- ─── Backfill: source_image_url into its column ─────────────────────────
--
-- The three readers being retired all say
--
--     SELECT value FROM product_attributes
--      WHERE product_id = … AND name = 'source_image_url'
--      ORDER BY sort_order LIMIT 1
--
-- so the backfill must pick the SAME row for a product that has more than one
-- — and products imported more than once do. DISTINCT ON with the same
-- ORDER BY reproduces it exactly, plus `id` as a final tiebreak: among rows of
-- equal sort_order the readers' LIMIT 1 picks whichever the executor happened
-- to reach first, and a backfill that is merely as arbitrary as the thing it
-- replaces is not reproducible. Ties are the only case where the two can
-- differ, and there the old answer was not stable to begin with.
--
-- `definition_id IS NULL` because only a legacy row can be one of these; a
-- typed row named by a definition is not the abuse and must not be harvested.
--
-- IS DISTINCT FROM keeps the UPDATE from rewriting rows that already agree,
-- which matters because this file is re-runnable and the column may already
-- be populated by the dual-write below.
UPDATE products p
   SET source_image_url = a.value
  FROM (
        SELECT DISTINCT ON (pa.product_id) pa.product_id, pa.value
          FROM product_attributes pa
         WHERE pa.name = 'source_image_url'
           AND pa.definition_id IS NULL
           AND pa.value <> ''
         ORDER BY pa.product_id, pa.sort_order, pa.id
       ) a
 WHERE a.product_id = p.id
   AND p.source_image_url IS DISTINCT FROM a.value;

-- ─── The old rows STAY ──────────────────────────────────────────────────
--
-- Not deleted here, and deliberately so. This migration lands with a rolling
-- deploy: for as long as it takes to drain, a pod on the previous image is
-- still writing `source_image_url` into `product_attributes` and still reading
-- it back from there. Deleting the rows now would blank the image on every
-- product served by an old pod, and the new writers dual-write to both places
-- precisely so that neither generation of pod sees a gap.
--
-- Removing them is a separate, GATED migration — the same discipline 007–012
-- use — run once the fleet is drained, the dual-write is confirmed for a full
-- deploy cycle, and `products.source_image_url` has been shown to match the
-- EAV row for every product. Until then the EAV row is the rollback path, and
-- a rollback path you have already deleted is not one.
