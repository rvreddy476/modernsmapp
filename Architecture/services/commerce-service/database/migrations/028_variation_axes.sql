-- 028 — a product's variation AXES become data, and a variant's options
-- become rows against them.
--
-- ─── WHAT IS WRONG TODAY ────────────────────────────────────────────────
--
-- `product_variants` carries three hard-coded option slots:
--
--     option_1_name / option_1_value
--     option_2_name / option_2_value
--     option_3_name / option_3_value
--
-- Six free-text columns, and nothing anywhere relates one variant's slots to
-- another's. So all of the following are storable, today, on one product:
--
--     variant A   option_1_name='Size'    option_1_value='L'
--     variant B   option_1_name='size'    option_1_value='l'
--     variant C   option_1_name='Colour'  option_1_value='Blue'
--     variant D   option_2_name='Size'    option_1_value='XL'
--
-- Four variants of one shirt, keyed on three spellings of two different
-- axes, one of them in the wrong slot. Nothing rejects any of it and nothing
-- afterwards can answer the two questions a variant matrix exists to answer:
--
--     "what does this product vary on?"      — there is no such fact stored
--     "are these two variants the same
--      combination?"                        — undecidable; A and B might be
--                                             the same size or two sizes
--
-- A shared catalogue makes it strictly worse. Once two sellers list the same
-- shirt, "Blue / M" has to be a thing they can BOTH offer — so the answer is
-- not a UNIQUE over the product either. And free-text values on a shared
-- catalogue mint "Blue", "blue" and "Navy Blue" as three permanent colours
-- that no filter will ever reunite.
--
-- ─── WHAT THIS FILE ADDS ────────────────────────────────────────────────
--
--   product_variation_axes      which attributes this product varies on, in
--                               order. The missing fact.
--
--   product_variant_options     one row per (variant, axis), holding a CODE.
--                               Its composite foreign key back to the axes
--                               table is the whole point of the design: a
--                               variant CANNOT carry an option on an axis
--                               the product does not declare, so "every
--                               variant agrees on the axis set" is true in
--                               the database rather than by convention.
--
--   product_variants.variation_key
--                               the canonical combination, derived, with
--                               UNIQUE(offer_id, variation_key). One seller
--                               cannot list "Blue / M" twice; two sellers
--                               both can.
--
--   variant_migration_exceptions
--                               the variants whose free-text option names
--                               this migration REFUSED to guess at, with the
--                               reason, so a human can resolve them.
--
-- ─── EXPAND ONLY ────────────────────────────────────────────────────────
--
-- Three CREATE TABLEs, one nullable ADD COLUMN, one composite UNIQUE on
-- `product_variants` that its primary key already made true, two partial
-- indexes, three functions and two triggers. The legacy `option_N_*` columns
-- are NOT dropped, NOT narrowed and NOT emptied — they are kept filled by a
-- trigger, because the bulk importer, the analytics readers and the phone's
-- shipped product screens all read them and none of those are being changed
-- here. `UNIQUE(sku)` is untouched. An old image running against this schema
-- writes exactly what it wrote before and notices nothing.

-- ─── product_variation_axes ─────────────────────────────────────────────
--
-- ─── WHY THE CAP IS TWO ─────────────────────────────────────────────────
--
-- The cap is declared, not counted: `position` is CHECKed to 1 or 2 and
-- UNIQUE per product, so a third axis has nowhere to go. No trigger, no
-- SELECT count(*), no race between two concurrent inserts each seeing one
-- existing row.
--
-- Two, because the variant matrix is a CROSS PRODUCT and a seller has to
-- price, stock and photograph every cell of it by hand. Two axes of five
-- options is 25 rows, which is a long afternoon. Three axes of five is 125,
-- and there is no UI that renders that as anything a human can check — the
-- grid stops being a grid and becomes a spreadsheet nobody fills in
-- correctly. Nobody prices 60 combinations; they price six and leave the
-- rest at whatever the form defaulted to, which is how a catalogue ends up
-- selling a large size at the small size's price. Sellers who genuinely need
-- a third dimension are better served by a second product than by a matrix
-- they will not maintain.
CREATE TABLE IF NOT EXISTS product_variation_axes (
    product_id    UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,

    -- No ON DELETE here on purpose. A definition is never deleted (025 says
    -- so and gives the reason: products carry values against it); if one ever
    -- were, taking a product's axis with it would silently un-vary the
    -- product and orphan every price its variants carry.
    definition_id UUID NOT NULL REFERENCES attribute_definitions(id),

    position      INT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- The composite key `product_variant_options` points at. It has to be a
    -- key, not merely an index, for the foreign key below to be creatable at
    -- all — which is a happy accident: the natural primary key here IS the
    -- pair the options table needs to name.
    PRIMARY KEY (product_id, definition_id),

    -- One axis per slot, and only two slots exist. Together these two are
    -- the cap; see the note above for why two.
    CONSTRAINT product_variation_axes_position_range CHECK (position IN (1, 2)),

    -- DEFERRABLE INITIALLY IMMEDIATE, which here means "checked at the end of
    -- the STATEMENT rather than after each row". Swapping two axes round is
    -- one UPDATE that sets 1→2 and 2→1; row by row that passes through a
    -- state where both rows claim the same slot, and a non-deferrable unique
    -- would refuse a swap that is correct the instant the statement finishes.
    -- Still IMMEDIATE, not DEFERRED: a transaction that leaves two axes in one
    -- slot must fail at the statement that did it, not at COMMIT, where the
    -- error names nothing useful.
    CONSTRAINT product_variation_axes_position_key   UNIQUE (product_id, position)
        DEFERRABLE INITIALLY IMMEDIATE
);

COMMENT ON TABLE product_variation_axes IS
    'Which attributes a product varies on, in order. At most two: position is CHECKed to 1 or 2 '
    'and UNIQUE per product, so the cap is declarative. Two because the variant matrix is a cross '
    'product a seller prices by hand, and nobody prices 60 combinations.';

CREATE INDEX IF NOT EXISTS idx_product_variation_axes_definition
    ON product_variation_axes(definition_id);

-- ─── product_variants gets a composite key ──────────────────────────────
--
-- `(id, product_id)` is already unique — `id` alone is the primary key — so
-- this declares nothing new about the data and can reject nothing that is
-- already stored. It exists so `product_variant_options` can carry a SECOND
-- composite foreign key proving that the `product_id` it names is the
-- variant's own product, and not some other product whose axes it fancied.
--
-- Without it, `product_id` on the options row would be a free-text claim: a
-- writer could name variant V (of product P1) alongside product P2's axis,
-- and the axis FK would be satisfied because P2 really does declare that
-- axis. The row would then be counted as one of P1's variants' options by
-- the variant join and as one of P2's by the axis join.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'product_variants_id_product_key') THEN
        ALTER TABLE product_variants
            ADD CONSTRAINT product_variants_id_product_key UNIQUE (id, product_id);
    END IF;
END $$;

-- ─── product_variant_options ────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS product_variant_options (
    variant_id    UUID NOT NULL,
    product_id    UUID NOT NULL,
    definition_id UUID NOT NULL,

    -- A CODE, never a label and never what the seller typed.
    --
    -- The CHECK is what makes `variation_key` injective: the key is built by
    -- joining `definition_code=value_code` pairs with '|', so a value
    -- containing either character could make two different combinations
    -- produce one key. Refused here rather than escaped in the builder,
    -- because an escape scheme is one more thing every reader of the key
    -- would have to implement identically.
    value_code    TEXT NOT NULL,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (variant_id, definition_id),

    CONSTRAINT product_variant_options_value_code_shape CHECK (
        value_code <> ''
        AND value_code = btrim(value_code)
        AND value_code !~ '[|=]'
        AND length(value_code) <= 128
    ),

    -- ─── THE CONSTRAINT THIS WHOLE FILE IS FOR ───────────────
    --
    -- A variant may only carry an option on an axis its product DECLARES.
    -- Not "should only" — cannot. This is what turns "every variant of a
    -- product agrees on the axis set" from a convention every write path has
    -- to remember into a fact the database refuses to break.
    --
    -- The cascade is deliberate: dropping an axis from a product is a
    -- decision about the product, and leaving its variants holding options
    -- on an axis that no longer exists is exactly the dangling state this
    -- table was created to make impossible.
    CONSTRAINT product_variant_options_axis_fk
        FOREIGN KEY (product_id, definition_id)
        REFERENCES product_variation_axes(product_id, definition_id)
        ON DELETE CASCADE,

    -- ...and the `product_id` above really is the variant's product.
    CONSTRAINT product_variant_options_variant_fk
        FOREIGN KEY (variant_id, product_id)
        REFERENCES product_variants(id, product_id)
        ON DELETE CASCADE
);

COMMENT ON TABLE product_variant_options IS
    'One (variant, axis) -> value_code. The composite FK to product_variation_axes is the point: a '
    'variant cannot carry an option on an axis its product does not declare.';

CREATE INDEX IF NOT EXISTS idx_product_variant_options_product
    ON product_variant_options(product_id, definition_id, value_code);

-- ─── product_variants.variation_key ─────────────────────────────────────
--
-- The canonical combination: the axes sorted by `position`, each written as
-- `definition_code=value_code`, joined with '|'. Sorted by position rather
-- than by code so the key reads in the order the seller sees the grid, and
-- so that reordering the axes is a visible change to the key rather than a
-- silent no-op.
--
-- DERIVED. Nothing outside the trigger below writes it, for the same reason
-- nothing outside the trigger writes the legacy option columns.
ALTER TABLE product_variants
    ADD COLUMN IF NOT EXISTS variation_key TEXT;

COMMENT ON COLUMN product_variants.variation_key IS
    'Derived canonical combination: axes by position, "definition_code=value_code", joined with "|". '
    'Maintained by product_variant_options_sync; never written directly.';

-- ─── ONE SELLER'S "Blue / M", NOT THE CATALOGUE'S ───────────────────────
--
-- Keyed on `offer_id`, which is the seller's listing of this item (migration
-- 027), NOT on `product_id`. On a shared catalogue two shops must both be
-- able to offer "Blue / M" of the same shirt — that is the entire reason the
-- offer exists — while neither may offer it twice.
--
-- Partial, on both columns being non-null, because both are legitimately
-- null today: `offer_id` is nullable until a later gated step, and
-- `variation_key` is null for every variant that has no declared axes, which
-- is nearly all of them. A full unique index would collapse every one of
-- those into a single (NULL, NULL) row — except that Postgres treats NULLs as
-- distinct, so it would in fact permit everything and be pure overhead. The
-- partial index states the intent instead of relying on that.
CREATE UNIQUE INDEX IF NOT EXISTS product_variants_offer_variation_key
    ON product_variants(offer_id, variation_key)
    WHERE offer_id IS NOT NULL AND variation_key IS NOT NULL;

-- ─── The legacy columns, kept filled ────────────────────────────────────
--
-- `option_N_name` / `option_N_value` are read by the bulk importer, by the
-- analytics queries and by product screens in a phone build that is already
-- in people's hands. They are never dropped and they must never go stale.
--
-- So they are DERIVED, not authoritative: the moment a variant has rows in
-- `product_variant_options`, this function owns all six columns for it and
-- recomputes them from the axes. A variant with no option rows is not touched
-- at all — that is every variant that predates this migration and every one
-- an old pod writes, and their legacy columns keep whatever they hold.
--
-- The NAME written is the definition's LABEL and the VALUE is the enum
-- option's LABEL when there is one, because that is the shape the phone
-- renders directly to a human — "Size" / "Large", not "size" / "l". The
-- machine-readable form lives in `variation_key` and in the options rows,
-- which is where a new reader should look.
--
-- Slot 3 is CLEARED for a managed variant. The cap is two axes, so a third
-- slot cannot be derived, and leaving whatever text was there would make the
-- six columns a mixture of derived and stale — the exact ambiguity this
-- function exists to remove.
CREATE OR REPLACE FUNCTION commerce_sync_variant_legacy_options(p_variant_id UUID)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    n1 TEXT; v1 TEXT;
    n2 TEXT; v2 TEXT;
    k  TEXT;
    n  INT;
BEGIN
    SELECT count(*),
           max(CASE WHEN a.position = 1 THEN d.label END),
           max(CASE WHEN a.position = 1 THEN COALESCE(e.label, o.value_code) END),
           max(CASE WHEN a.position = 2 THEN d.label END),
           max(CASE WHEN a.position = 2 THEN COALESCE(e.label, o.value_code) END),
           string_agg(d.code || '=' || o.value_code, '|' ORDER BY a.position)
      INTO n, n1, v1, n2, v2, k
      FROM product_variant_options o
      JOIN product_variation_axes a
        ON a.product_id = o.product_id AND a.definition_id = o.definition_id
      JOIN attribute_definitions d ON d.id = o.definition_id
      -- LEFT, because a text or integer axis has no enum rows at all and its
      -- value_code IS the human-readable value.
      LEFT JOIN attribute_enum_values e
        ON e.definition_id = o.definition_id AND e.code = o.value_code
     WHERE o.variant_id = p_variant_id;

    IF n = 0 THEN
        -- Every option row is gone. The derived columns go with them: they
        -- are derived, and leaving the last combination behind would make
        -- "this variant has no options" look like "this variant is a Large".
        UPDATE product_variants
           SET option_1_name = NULL, option_1_value = NULL,
               option_2_name = NULL, option_2_value = NULL,
               option_3_name = NULL, option_3_value = NULL,
               variation_key = NULL,
               updated_at    = NOW()
         WHERE id = p_variant_id
           AND (option_1_name, option_1_value, option_2_name,
                option_2_value, option_3_name, option_3_value, variation_key)
               IS DISTINCT FROM (NULL, NULL, NULL, NULL, NULL, NULL, NULL);
        RETURN;
    END IF;

    -- The guard on the last line is not an optimisation. A trigger that
    -- writes an identical row still bumps `updated_at`, and two of the five
    -- lifecycle paths in this service read `updated_at` to decide what has
    -- changed since a checkpoint.
    UPDATE product_variants
       SET option_1_name = n1, option_1_value = v1,
           option_2_name = n2, option_2_value = v2,
           option_3_name = NULL, option_3_value = NULL,
           variation_key = k,
           updated_at    = NOW()
     WHERE id = p_variant_id
       AND (option_1_name, option_1_value, option_2_name,
            option_2_value, option_3_name, option_3_value, variation_key)
           IS DISTINCT FROM (n1, v1, n2, v2, NULL, NULL, k);
END $$;

-- The row trigger. Note what it does NOT do: it never reads a column off the
-- variant to decide anything, so there is no path by which a legacy value
-- influences the derived one.
--
-- The UPDATE inside targets a row that may already be gone — a variant being
-- deleted cascades its option rows, and this fires during that cascade. Zero
-- rows updated is the correct outcome there, not an error, which is why the
-- function does not check the row count.
CREATE OR REPLACE FUNCTION commerce_variant_options_sync_trg()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM commerce_sync_variant_legacy_options(OLD.variant_id);
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.variant_id <> NEW.variant_id THEN
        PERFORM commerce_sync_variant_legacy_options(OLD.variant_id);
    END IF;
    PERFORM commerce_sync_variant_legacy_options(NEW.variant_id);
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS product_variant_options_sync ON product_variant_options;
CREATE TRIGGER product_variant_options_sync
    AFTER INSERT OR UPDATE OR DELETE ON product_variant_options
    FOR EACH ROW EXECUTE FUNCTION commerce_variant_options_sync_trg();

-- Reordering the axes changes every one of the product's variation keys and
-- which legacy slot each option lands in. UPDATE only: an axis DELETE
-- cascades its option rows, and their own trigger does the recompute.
CREATE OR REPLACE FUNCTION commerce_variation_axes_sync_trg()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    vid UUID;
BEGIN
    FOR vid IN
        SELECT DISTINCT variant_id FROM product_variant_options WHERE product_id = NEW.product_id
    LOOP
        PERFORM commerce_sync_variant_legacy_options(vid);
    END LOOP;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS product_variation_axes_sync ON product_variation_axes;
CREATE TRIGGER product_variation_axes_sync
    AFTER UPDATE ON product_variation_axes
    FOR EACH ROW EXECUTE FUNCTION commerce_variation_axes_sync_trg();

-- ─── variant_migration_exceptions ───────────────────────────────────────
--
-- The backfill below refuses to guess. Everything it cannot resolve lands
-- here, named, with the reason, so that the residue is a worklist somebody
-- can finish rather than a silence.
--
-- WHY REFUSING MATTERS MORE THAN COVERAGE: on a shared catalogue an axis is
-- permanent and public. Deciding that the free text "Colour" means the
-- `color` definition, on a database where `color` happens to be a Books
-- binding about cover art, mints a wrong axis onto every seller who ever
-- lists that item — and unlike a wrong value, a wrong axis cannot be
-- corrected by the seller, because the axis is a fact about the ITEM. A
-- migration that resolves 60% correctly and 40% plausibly is worse than one
-- that resolves 60% and hands over a list.
CREATE TABLE IF NOT EXISTS variant_migration_exceptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    variant_id      UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    -- Which of the three legacy slots this complaint is about. 0 means the
    -- complaint is about the variant or the product as a whole (too many
    -- axes, a duplicate combination, a sibling that did not resolve).
    option_position INT  NOT NULL CHECK (option_position BETWEEN 0 AND 3),
    option_name     TEXT,
    option_value    TEXT,
    reason          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (variant_id, option_position)
);

CREATE INDEX IF NOT EXISTS idx_variant_migration_exceptions_product
    ON variant_migration_exceptions(product_id);

COMMENT ON TABLE variant_migration_exceptions IS
    'Legacy variant options migration 028 refused to guess at, with the reason. A worklist, not a log.';

-- ─── The backfill, as a callable function ───────────────────────────────
--
-- A FUNCTION rather than a straight-line script for two reasons. It is
-- re-runnable — an operator who resolves ten exceptions by creating the
-- missing definition can run it again and pick those products up — and it is
-- testable, which a block of DO $$ inside a migration is not.
--
-- ─── HOW A NAME IS RESOLVED, AND WHERE IT STOPS ─────────────────────────
--
--   name  -> definition   case-insensitive on `code` OR `label`, active
--                         only, and the definition must actually be usable
--                         as an axis (`is_variant_axis`, and one of the
--                         three discrete data types 025 permits for one).
--                         EXACTLY ONE match: two definitions whose labels
--                         differ only in case is an ambiguity, not a
--                         tie-break.
--
--   value -> value_code   for an enum axis, case-insensitively against the
--                         active enum options' `code` or `label`, again
--                         exactly one. For an integer axis, the digits. For
--                         a text axis, the trimmed text — subject to the
--                         value_code shape CHECK.
--
-- ─── AND WHY A PRODUCT IS ALL-OR-NOTHING ────────────────────────────────
--
-- Axes are a fact about the PRODUCT. A product whose first variant resolves
-- and whose second does not cannot be half-migrated: writing the axis from
-- variant one leaves variant two unable to carry an option at all (the
-- composite FK sees to that), which is a product with declared axes and a
-- variant outside them — indistinguishable, to every later reader, from a
-- bug. So a product migrates only when EVERY variant resolves EVERY slot,
-- the slots agree on which axis sits in which position across all variants,
-- there are at most two of them, and no two variants of one offer land on
-- the same combination.
CREATE OR REPLACE FUNCTION commerce_backfill_variation_axes()
RETURNS TABLE (products_migrated INT, variants_migrated INT, exceptions_recorded INT)
LANGUAGE plpgsql
AS $$
DECLARE
    n_products INT := 0;
    n_variants INT := 0;
    n_except   INT := 0;
    n_rows     INT := 0;
BEGIN
    -- ── 1. Every legacy slot that says anything, unpacked to rows ──
    CREATE TEMP TABLE _slots ON COMMIT DROP AS
    SELECT v.id AS variant_id, v.product_id, v.offer_id, s.position,
           btrim(s.name) AS raw_name, btrim(COALESCE(s.value, '')) AS raw_value
      FROM product_variants v
     CROSS JOIN LATERAL (VALUES
            (1, v.option_1_name, v.option_1_value),
            (2, v.option_2_name, v.option_2_value),
            (3, v.option_3_name, v.option_3_value)
     ) AS s(position, name, value)
     WHERE s.name IS NOT NULL AND btrim(s.name) <> ''
       -- Products that already have axes are done. This is what makes the
       -- function re-runnable without fighting its own previous output.
       AND NOT EXISTS (SELECT 1 FROM product_variation_axes a WHERE a.product_id = v.product_id);

    -- ── 2. Resolve each slot's name and value, or record why not ──
    CREATE TEMP TABLE _resolved ON COMMIT DROP AS
    WITH def_match AS (
        SELECT s.*,
               (SELECT count(*) FROM attribute_definitions d
                 WHERE d.is_active
                   AND (lower(d.code) = lower(s.raw_name) OR lower(d.label) = lower(s.raw_name))
               ) AS n_defs,
               -- LIMIT 1, with `n_defs` beside it deciding whether the one
               -- picked is trustworthy. A scalar subquery that returned two
               -- rows would raise instead of reporting the ambiguity, which
               -- is the one thing this pass must not do.
               (SELECT d.id FROM attribute_definitions d
                 WHERE d.is_active
                   AND (lower(d.code) = lower(s.raw_name) OR lower(d.label) = lower(s.raw_name))
                 ORDER BY d.code
                 LIMIT 1
               ) AS def_id
          FROM _slots s
    ),
    def_typed AS (
        SELECT m.*, d.data_type, d.is_variant_axis
          FROM def_match m
          LEFT JOIN attribute_definitions d ON d.id = m.def_id AND m.n_defs = 1
    ),
    val_match AS (
        SELECT t.*,
               CASE
                 WHEN t.data_type = 'enum' THEN
                     (SELECT count(*) FROM attribute_enum_values e
                       WHERE e.definition_id = t.def_id AND e.is_active
                         AND (lower(e.code) = lower(t.raw_value) OR lower(e.label) = lower(t.raw_value)))
                 ELSE 1
               END AS n_vals,
               CASE
                 WHEN t.data_type = 'enum' THEN
                     (SELECT e.code FROM attribute_enum_values e
                       WHERE e.definition_id = t.def_id AND e.is_active
                         AND (lower(e.code) = lower(t.raw_value) OR lower(e.label) = lower(t.raw_value))
                       ORDER BY e.code
                       LIMIT 1)
                 ELSE t.raw_value
               END AS value_code
          FROM def_typed t
    )
    SELECT variant_id, product_id, offer_id, position, raw_name, raw_value,
           def_id, data_type, value_code,
           CASE
             WHEN n_defs = 0 THEN
                 'no attribute definition matches the option name "' || raw_name ||
                 '" on code or label; create one and re-run'
             WHEN n_defs > 1 THEN
                 'the option name "' || raw_name || '" matches ' || n_defs ||
                 ' attribute definitions case-insensitively; the match is ambiguous'
             WHEN NOT is_variant_axis THEN
                 'the attribute matching "' || raw_name ||
                 '" is not marked is_variant_axis, so it may not key a variant'
             WHEN data_type NOT IN ('enum', 'text', 'integer') THEN
                 'the attribute matching "' || raw_name || '" is a ' || data_type ||
                 ', which cannot be a variant axis'
             WHEN raw_value = '' THEN
                 'the option name "' || raw_name || '" carries no value'
             WHEN data_type = 'enum' AND n_vals = 0 THEN
                 'no active option of "' || raw_name || '" matches the value "' || raw_value ||
                 '" on code or label; add the enum option and re-run'
             WHEN data_type = 'enum' AND n_vals > 1 THEN
                 'the value "' || raw_value || '" matches ' || n_vals ||
                 ' options of "' || raw_name || '"; the match is ambiguous'
             WHEN data_type = 'integer' AND raw_value !~ '^-?[0-9]+$' THEN
                 'the value "' || raw_value || '" is not a whole number, and "' || raw_name ||
                 '" is an integer axis'
             WHEN value_code IS NULL OR value_code = '' OR value_code <> btrim(value_code)
                  OR value_code ~ '[|=]' OR length(value_code) > 128 THEN
                 'the value "' || raw_value ||
                 '" cannot be a value code: it must be non-empty, untrimmed of spaces, ' ||
                 'at most 128 characters, and contain neither "|" nor "="'
             ELSE NULL
           END AS failure
      FROM val_match;

    -- ── 3. Product-level refusals ──
    --
    -- Recorded at position 0, against the FIRST variant of the product, so
    -- the exceptions table names the product exactly once for a product-wide
    -- problem instead of once per variant.
    CREATE TEMP TABLE _product_failure ON COMMIT DROP AS
    SELECT product_id, string_agg(reason, '; ' ORDER BY reason) AS reason
      FROM (
        -- a slot that did not resolve
        SELECT product_id, 'one or more variants have an option this migration could not resolve'::text AS reason
          FROM _resolved WHERE failure IS NOT NULL
         GROUP BY product_id
        UNION
        -- more axes than the cap
        SELECT product_id,
               'this product varies on ' || count(DISTINCT def_id) ||
               ' attributes; the matrix is capped at two'
          FROM _resolved WHERE failure IS NULL
         GROUP BY product_id HAVING count(DISTINCT def_id) > 2
        UNION
        -- the same axis in different slots on different variants
        SELECT product_id,
               'variants disagree about which slot an axis occupies, so the axis order is undecidable'
          FROM (SELECT product_id, def_id, count(DISTINCT position) AS n FROM _resolved
                 WHERE failure IS NULL GROUP BY product_id, def_id) x
         WHERE n > 1
         GROUP BY product_id
        UNION
        -- two axes in one slot across variants
        SELECT product_id,
               'variants disagree about which axis occupies a slot, so the axis order is undecidable'
          FROM (SELECT product_id, position, count(DISTINCT def_id) AS n FROM _resolved
                 WHERE failure IS NULL GROUP BY product_id, position) x
         WHERE n > 1
         GROUP BY product_id
        UNION
        -- variants of one product that do not all carry the same axis set
        SELECT product_id, 'variants of this product do not all carry the same set of axes'
          FROM (SELECT r.product_id, r.variant_id, count(*) AS n FROM _resolved r
                 WHERE r.failure IS NULL GROUP BY r.product_id, r.variant_id) v
         GROUP BY product_id HAVING count(DISTINCT n) > 1
        UNION
        -- a variant with no resolvable option at all, alongside siblings that have one
        SELECT p.product_id, 'some variants of this product carry no options at all'
          FROM (SELECT DISTINCT product_id FROM _resolved WHERE failure IS NULL) p
          JOIN product_variants v ON v.product_id = p.product_id
         WHERE NOT EXISTS (SELECT 1 FROM _resolved r
                            WHERE r.variant_id = v.id AND r.failure IS NULL)
         GROUP BY p.product_id
        UNION
        -- two variants of one offer on the same combination: the unique
        -- index would refuse the second, so the product is parked whole
        SELECT product_id, 'two variants of one offer resolve to the same combination'
          FROM (SELECT r.product_id, r.offer_id, r.variant_id,
                       string_agg(r.def_id::text || '=' || r.value_code, '|' ORDER BY r.position) AS k
                  FROM _resolved r WHERE r.failure IS NULL
                 GROUP BY r.product_id, r.offer_id, r.variant_id) c
         WHERE c.offer_id IS NOT NULL
         GROUP BY product_id, offer_id, k HAVING count(*) > 1
      ) f
     GROUP BY product_id;

    -- ── 4. Park the residue ──
    INSERT INTO variant_migration_exceptions
        (product_id, variant_id, option_position, option_name, option_value, reason)
    SELECT r.product_id, r.variant_id, r.position, r.raw_name, NULLIF(r.raw_value, ''), r.failure
      FROM _resolved r
     WHERE r.failure IS NOT NULL
     ON CONFLICT (variant_id, option_position) DO UPDATE SET reason = EXCLUDED.reason;
    GET DIAGNOSTICS n_rows = ROW_COUNT;
    n_except := n_except + n_rows;

    INSERT INTO variant_migration_exceptions
        (product_id, variant_id, option_position, option_name, option_value, reason)
    SELECT pf.product_id, v.variant_id, 0, NULL, NULL, pf.reason
      FROM _product_failure pf
      JOIN LATERAL (SELECT id AS variant_id FROM product_variants
                     WHERE product_id = pf.product_id ORDER BY created_at, id LIMIT 1) v ON TRUE
     ON CONFLICT (variant_id, option_position) DO UPDATE SET reason = EXCLUDED.reason;
    GET DIAGNOSTICS n_rows = ROW_COUNT;
    n_except := n_except + n_rows;

    -- ── 5. Write the axes for the products that are wholly resolvable ──
    CREATE TEMP TABLE _ok ON COMMIT DROP AS
    SELECT r.* FROM _resolved r
     WHERE r.failure IS NULL
       AND NOT EXISTS (SELECT 1 FROM _product_failure pf WHERE pf.product_id = r.product_id);

    -- `position` is renumbered from the slot's DENSE_RANK, not copied: a
    -- product whose only option sat in legacy slot 2 gets axis position 1,
    -- because positions are CHECKed to 1..2 and a gap would be a lie about
    -- ordering rather than a record of where the text used to live.
    INSERT INTO product_variation_axes (product_id, definition_id, position)
    SELECT product_id, def_id,
           DENSE_RANK() OVER (PARTITION BY product_id ORDER BY min_pos)
      FROM (SELECT product_id, def_id, min(position) AS min_pos
              FROM _ok GROUP BY product_id, def_id) a
     ON CONFLICT (product_id, definition_id) DO NOTHING;

    INSERT INTO product_variant_options (variant_id, product_id, definition_id, value_code)
    SELECT variant_id, product_id, def_id, value_code FROM _ok
     ON CONFLICT (variant_id, definition_id) DO NOTHING;

    SELECT count(DISTINCT product_id)::int INTO n_products FROM _ok;
    SELECT count(DISTINCT variant_id)::int INTO n_variants FROM _ok;

    DROP TABLE _slots, _resolved, _product_failure, _ok;

    products_migrated   := n_products;
    variants_migrated   := n_variants;
    exceptions_recorded := n_except;
    RETURN NEXT;
END $$;

COMMENT ON FUNCTION commerce_backfill_variation_axes() IS
    'Migrates legacy option_N_* text onto product_variation_axes / product_variant_options where '
    'every variant of a product resolves; parks the rest in variant_migration_exceptions. '
    'Re-runnable: products that already have axes are skipped.';

-- ─── Run it, and say what happened ──────────────────────────────────────
--
-- A NOTICE, not an exception. Unlike 027's backfill — which had to be
-- complete, because a missing offer would have surfaced later as a listing
-- vanishing — an unresolved option here changes NOTHING. The variant keeps
-- its legacy columns, every reader keeps reading them, and the only
-- consequence is that the product cannot yet use the new matrix. Refusing to
-- boot over that would hold the whole service hostage to a definition
-- somebody has not created yet.
DO $$
DECLARE r RECORD;
BEGIN
    SELECT * INTO r FROM commerce_backfill_variation_axes();
    RAISE NOTICE '028 backfill: % product(s) and % variant(s) migrated onto declared axes; % exception(s) parked in variant_migration_exceptions',
        r.products_migrated, r.variants_migrated, r.exceptions_recorded;
END $$;
