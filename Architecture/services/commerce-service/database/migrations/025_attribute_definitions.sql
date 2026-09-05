-- 025 — category-specific product attributes: the vocabulary, the definitions,
-- and the binding that makes a category ask for them.
--
-- `product_attributes` has always existed: (product_id, name, value, unit,
-- sort_order), free text on both sides. Nothing declares what a category is
-- allowed to ask for, so `PUT /products/:id/attributes` accepts any name a
-- client invents. Two sellers listing the same textbook can store `author`,
-- `Author`, `writer` and `by`, and a filter over any of them returns three of
-- the four books. There is no form to render either: a create screen that
-- wants to ask "how many pages?" has nothing to read to discover that the
-- question exists, which is why every catalogue field beyond title/price is
-- typed into a free-text box today.
--
-- This file adds the missing half — the DEFINITION side. Nothing reads it yet
-- and no existing behaviour changes: `product_attributes` is untouched, no
-- column on it is constrained, and no write path is narrowed. It is pure
-- expansion, which is what lets it live in the boot set at all.
--
-- ─── THE FIVE TABLES, AND WHY EACH IS SEPARATE ──────────────────────────
--
--   attribute_unit_families / attribute_units
--       "grams" is not a fact about a product; it is a fact about mass. A
--       weight typed in kg and one typed in lb have to be comparable, so the
--       conversion factor lives beside the unit rather than in whichever
--       client happened to render the form.
--
--   attribute_definitions
--       The field itself: its code, its type, its bounds. `code` is the join
--       key to `product_attributes.name`, which is why it is constrained to a
--       machine-safe shape and is UNIQUE across the whole catalogue — the same
--       question asked in two categories must be the same field, or the filter
--       over it splits in two.
--
--   attribute_enum_values
--       An enum's options, as ROWS. Storing them as a JSON array inside the
--       definition would make "deactivate `spiral` without breaking the six
--       products that already say spiral" an edit to a blob, and would leave
--       swatch artwork nowhere to live.
--
--   category_attributes
--       Which categories ask which questions. A category does NOT own its
--       fields — it binds them, and a child inherits every binding from its
--       ancestors unless it overrides or excludes one. That is why `Books`
--       can be given `author` once instead of once per leaf.
--
--   attribute_definition_revisions
--       Every write, before and after. A definition is a contract with every
--       product already stored against it; "who tightened this bound and when"
--       has to be answerable after the fact, not reconstructed from a diff of
--       whatever the row says now.
--
-- ─── WHY THERE IS A published_version AT ALL ────────────────────────────
--
-- Definitions are edited by a person, in a form, one field at a time. Without
-- a publish step a half-typed definition — a label typed but the enum options
-- not yet added, a regex saved mid-thought — is live to every client the
-- moment it is saved, because the schema endpoint reads the same rows the
-- editor writes. `attribute_schema_state` holds one row: edits set
-- `draft_dirty`, Publish bumps `published_version` and clears it. The version
-- is also the cache key clients validate against, so a publish is the single
-- event that invalidates every cached form.
--
-- ─── EXPAND-ONLY ────────────────────────────────────────────────────────
--
-- Six CREATE TABLEs, one ADD COLUMN with a DEFAULT that preserves today's
-- behaviour (`is_listable` TRUE — every category is listable, which is what
-- is true now), and seed rows. No constraint is added to an existing table, no
-- existing constraint is narrowed, and no existing column changes type or
-- nullability. An old image running against this schema cannot notice it.

-- ─── pg_trgm ────────────────────────────────────────────────────────────
--
-- Wanted for label/lookup search over definitions and enum values once the
-- list is long enough to need one. Creating an extension needs a role the
-- application user may not be, and the migration runner wraps this file in a
-- single transaction — an uncaught permission error would abort the whole
-- file and take the service's boot with it, over an index nothing yet reads.
-- So it is attempted, and its absence is a NOTICE rather than a failure. The
-- trigram indexes below are created only if it actually landed.
DO $$
BEGIN
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_trgm';
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE '025: pg_trgm not installed (%); attribute label search falls back to prefix matching', SQLERRM;
END $$;

-- ─── Units ──────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS attribute_unit_families (
    code       TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    -- The unit every value in this family is normalised to before it is
    -- compared, sorted or filtered. Stored, not inferred, because "the one
    -- with factor 1" is a coincidence a future family need not honour.
    base_unit  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS attribute_units (
    family         TEXT NOT NULL REFERENCES attribute_unit_families(code) ON DELETE CASCADE,
    code           TEXT NOT NULL,
    label          TEXT NOT NULL,
    -- Multiply a value in this unit by this to get the family's base unit.
    -- NUMERIC(24,10) because 28.349523125 (oz→g) is exact and 28.3495 is not,
    -- and a shipping weight computed from the rounded figure is a different
    -- number from the one the seller typed.
    factor_to_base NUMERIC(24,10) NOT NULL CHECK (factor_to_base > 0),
    sort_order     INT NOT NULL DEFAULT 0,
    PRIMARY KEY (family, code)
);

INSERT INTO attribute_unit_families (code, label, base_unit) VALUES
    ('mass',   'Weight', 'g'),
    ('length', 'Length', 'mm'),
    ('volume', 'Volume', 'ml'),
    ('count',  'Count',  'unit')
ON CONFLICT (code) DO UPDATE SET label = EXCLUDED.label, base_unit = EXCLUDED.base_unit;

INSERT INTO attribute_units (family, code, label, factor_to_base, sort_order) VALUES
    ('mass',   'g',     'grams',       1.0,           10),
    ('mass',   'kg',    'kilograms',   1000.0,        20),
    ('mass',   'mg',    'milligrams',  0.001,         30),
    ('mass',   'lb',    'pounds',      453.59237,     40),
    ('mass',   'oz',    'ounces',      28.349523125,  50),
    ('length', 'mm',    'millimetres', 1.0,           10),
    ('length', 'cm',    'centimetres', 10.0,          20),
    ('length', 'm',     'metres',      1000.0,        30),
    ('length', 'in',    'inches',      25.4,          40),
    ('length', 'ft',    'feet',        304.8,         50),
    ('volume', 'ml',    'millilitres', 1.0,           10),
    ('volume', 'l',     'litres',      1000.0,        20),
    ('count',  'unit',  'units',       1.0,           10),
    -- A pack is one saleable thing whose contents the seller declares
    -- separately, so it converts 1:1. Giving it a made-up multiple here would
    -- silently restate every "1 pack" as some number of loose items.
    ('count',  'pack',  'packs',       1.0,           20),
    ('count',  'dozen', 'dozens',      12.0,          30)
ON CONFLICT (family, code) DO UPDATE SET
    label = EXCLUDED.label, factor_to_base = EXCLUDED.factor_to_base, sort_order = EXCLUDED.sort_order;

-- ─── Definitions ────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS attribute_definitions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The join key to product_attributes.name. Lower snake case, starts with
    -- a letter, 2–49 characters. UNIQUE across the catalogue, not per
    -- category: `author` must mean one thing everywhere or a filter over it
    -- returns a subset of the books that have an author.
    code          TEXT NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9_]{1,48}$'),
    label         TEXT NOT NULL,
    help_text     TEXT,
    placeholder   TEXT,
    data_type     TEXT NOT NULL CHECK (data_type IN (
                      'text','long_text','integer','decimal','money_minor','boolean',
                      'enum','multi_enum','date','measure','media','gtin')),
    unit_family   TEXT REFERENCES attribute_unit_families(code),
    default_unit  TEXT,
    min_num       NUMERIC(20,6),
    max_num       NUMERIC(20,6),
    min_len       INT,
    max_len       INT,
    regex         TEXT,
    -- Ceiling on how many values a multi_enum / media field may hold.
    max_values    INT CHECK (max_values IS NULL OR max_values > 0),
    -- Which fieldset the form draws this in. A closed list rather than free
    -- text: a typo'd group name renders as a lonely extra section, and the
    -- seller reads it as a bug in the form.
    display_group TEXT NOT NULL DEFAULT 'Product Details' CHECK (display_group IN (
                      'Product Identity','Description','Product Details','Offer',
                      'Safety & Compliance','Logistics')),
    -- 'item' is a fact about the goods (page count, author). 'offer' is a fact
    -- about this seller's listing of them (condition, warranty). The same
    -- textbook listed by two sellers shares every item attribute and shares
    -- none of the offer ones, so a client that merges listings needs to know
    -- which is which before it merges anything.
    applies_to    TEXT NOT NULL DEFAULT 'item' CHECK (applies_to IN ('item','offer')),
    is_variant_axis BOOLEAN NOT NULL DEFAULT FALSE,
    is_filterable   BOOLEAN NOT NULL DEFAULT FALSE,
    is_searchable   BOOLEAN NOT NULL DEFAULT FALSE,
    -- Definitions are never deleted. Products already carry values against
    -- them and a deleted definition would leave those values unreadable.
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    version         INT NOT NULL DEFAULT 1,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A measure with no family is a number with no meaning: 2.5 of what?
    CONSTRAINT attribute_definitions_measure_needs_family
        CHECK (data_type <> 'measure' OR unit_family IS NOT NULL),
    -- A variant axis becomes a column in the variant matrix, so its values
    -- have to be discrete and comparable. A long_text or a date axis produces
    -- one variant per keystroke.
    CONSTRAINT attribute_definitions_axis_types
        CHECK (NOT is_variant_axis OR data_type IN ('enum','text','integer')),
    CONSTRAINT attribute_definitions_num_bounds
        CHECK (min_num IS NULL OR max_num IS NULL OR min_num <= max_num),
    CONSTRAINT attribute_definitions_len_bounds
        CHECK (min_len IS NULL OR max_len IS NULL OR min_len <= max_len),
    CONSTRAINT attribute_definitions_default_unit_needs_family
        CHECK (default_unit IS NULL OR unit_family IS NOT NULL),
    -- MATCH SIMPLE: satisfied whenever either column is NULL, so a
    -- non-measure definition is unaffected. When both are set the pair must
    -- name a unit that actually exists in that family.
    CONSTRAINT attribute_definitions_default_unit_fk
        FOREIGN KEY (unit_family, default_unit) REFERENCES attribute_units(family, code)
);

CREATE INDEX IF NOT EXISTS idx_attribute_definitions_active
    ON attribute_definitions(is_active, display_group, code);

CREATE TABLE IF NOT EXISTS attribute_enum_values (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    definition_id   UUID NOT NULL REFERENCES attribute_definitions(id) ON DELETE CASCADE,
    code            TEXT NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    label           TEXT NOT NULL,
    -- Colour swatches for the ones that are colours; artwork for the ones
    -- that are patterns. Both nullable — most enums are neither.
    swatch_hex      TEXT CHECK (swatch_hex IS NULL OR swatch_hex ~ '^#[0-9A-Fa-f]{6}$'),
    swatch_media_id UUID,
    sort_order      INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (definition_id, code)
);

CREATE INDEX IF NOT EXISTS idx_attribute_enum_values_definition
    ON attribute_enum_values(definition_id, sort_order, code);

-- ─── Bindings ───────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS category_attributes (
    category_id   UUID NOT NULL REFERENCES product_categories(id) ON DELETE CASCADE,
    definition_id UUID NOT NULL REFERENCES attribute_definitions(id) ON DELETE CASCADE,
    is_required   BOOLEAN NOT NULL DEFAULT FALSE,
    -- The override that means "my ancestor asks this and I do not". Without
    -- it a leaf could only escape an inherited field by the parent dropping
    -- it for every sibling too.
    is_excluded   BOOLEAN NOT NULL DEFAULT FALSE,
    -- NULL means "whatever the definition says". A category may promote a
    -- field to a variant axis (size matters for shoes) without forcing every
    -- other category that uses it to agree.
    is_variant_axis BOOLEAN,
    display_group TEXT CHECK (display_group IS NULL OR display_group IN (
                      'Product Identity','Description','Product Details','Offer',
                      'Safety & Compliance','Logistics')),
    sort_order    INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (category_id, definition_id)
);

CREATE INDEX IF NOT EXISTS idx_category_attributes_definition
    ON category_attributes(definition_id);

-- ─── Audit ──────────────────────────────────────────────────────────────
--
-- Deliberately WITHOUT a foreign key to attribute_definitions. An audit row
-- that cascades away with the thing it audits is not an audit row, and the
-- one operation this trail exists to explain is the one nobody will admit to.
CREATE TABLE IF NOT EXISTS attribute_definition_revisions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    definition_id UUID NOT NULL,
    version       INT NOT NULL,
    before        JSONB,
    after         JSONB,
    actor_user_id UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_attribute_definition_revisions_definition
    ON attribute_definition_revisions(definition_id, version DESC);

-- ─── Publish state ──────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS attribute_schema_state (
    -- One row, enforced by the type system rather than by convention: the PK
    -- can only be TRUE, and the CHECK refuses FALSE, so a second row is a
    -- primary-key violation instead of a silently divergent second opinion.
    singleton         BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    published_version INT NOT NULL DEFAULT 1,
    draft_dirty       BOOLEAN NOT NULL DEFAULT FALSE,
    published_at      TIMESTAMPTZ
);

INSERT INTO attribute_schema_state (singleton, published_version, draft_dirty, published_at)
VALUES (TRUE, 1, FALSE, NOW())
ON CONFLICT (singleton) DO NOTHING;

-- ─── Trigram indexes, only if the extension actually landed ─────────────
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_attribute_definitions_label_trgm '
                'ON attribute_definitions USING gin (label gin_trgm_ops)';
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_attribute_enum_values_label_trgm '
                'ON attribute_enum_values USING gin (label gin_trgm_ops)';
    END IF;
END $$;

-- ─── product_categories.is_listable ─────────────────────────────────────
--
-- Whether a seller may list directly ON this node, as against having to pick
-- a leaf beneath it. "Books" is a browse heading; "Books › Textbooks" is where
-- a listing belongs. DEFAULT TRUE preserves exactly today's behaviour — every
-- existing category is listable, because nothing has ever asked.
ALTER TABLE product_categories
    ADD COLUMN IF NOT EXISTS is_listable BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN product_categories.is_listable IS
    'Whether a seller may list a product directly on this node. FALSE marks a browse-only '
    'heading whose children carry the listings.';

-- ─── The worked example ─────────────────────────────────────────────────
--
-- Books › Textbooks, and the seven fields a book listing needs. Bound to
-- BOOKS, not to Textbooks: the point of the binding table is that a child
-- inherits, and a seed that binds straight to the leaf would demonstrate
-- nothing about inheritance and would have to be repeated for Fiction,
-- Reference and every other child added later.
--
-- Ids are fixed literals for the reason migration 023 gives: a category id and
-- a definition id are values clients keep, and generating them here would make
-- them differ between every environment and every rebuild.

INSERT INTO product_categories (id, parent_id, name, slug, description, display_order, is_active, is_featured, is_listable)
SELECT '00000000-0000-4000-8000-00000000c101', parent.id,
       'Textbooks', 'books-textbooks',
       'Academic and school textbooks, guides and reference sets',
       10, TRUE, FALSE, TRUE
  FROM product_categories parent
 WHERE parent.slug = 'books-and-stationery'
ON CONFLICT (slug) DO UPDATE SET
    parent_id     = EXCLUDED.parent_id,
    name          = EXCLUDED.name,
    description   = EXCLUDED.description,
    display_order = EXCLUDED.display_order,
    updated_at    = NOW();

-- Books itself becomes browse-only: its children carry the listings. This is
-- the one seeded row whose is_listable is not the default. Keyed on the slug,
-- which 023 made the convergence key, so an operator who renamed the row is
-- still pointed at the right one.
UPDATE product_categories SET is_listable = FALSE, updated_at = NOW()
 WHERE slug = 'books-and-stationery';

INSERT INTO attribute_definitions
    (id, code, label, help_text, placeholder, data_type, unit_family, default_unit,
     min_num, max_num, min_len, max_len, regex, max_values,
     display_group, applies_to, is_variant_axis, is_filterable, is_searchable)
VALUES
    ('00000000-0000-4000-8000-00000000a001', 'gtin', 'ISBN / EAN',
     'The barcode number printed on the back cover. 10 to 14 digits, no spaces or hyphens.',
     '9788126558643', 'gtin', NULL, NULL,
     NULL, NULL, 10, 14, '^[0-9]{10,14}$', NULL,
     'Product Identity', 'item', FALSE, FALSE, TRUE),

    ('00000000-0000-4000-8000-00000000a002', 'author', 'Author',
     'The name as printed on the cover. Separate multiple authors with a comma.',
     'R. K. Narayan', 'text', NULL, NULL,
     NULL, NULL, 1, 200, NULL, NULL,
     'Product Details', 'item', FALSE, TRUE, TRUE),

    ('00000000-0000-4000-8000-00000000a003', 'binding', 'Binding',
     'How the book is bound. Buyers filter on this and it usually changes the price, so it is a variant axis.',
     NULL, 'enum', NULL, NULL,
     NULL, NULL, NULL, NULL, NULL, NULL,
     'Product Details', 'item', TRUE, TRUE, FALSE),

    ('00000000-0000-4000-8000-00000000a004', 'pages', 'Number of pages',
     'Printed pages, excluding the cover.',
     '328', 'integer', NULL, NULL,
     1, 10000, NULL, NULL, NULL, NULL,
     'Product Details', 'item', FALSE, TRUE, FALSE),

    ('00000000-0000-4000-8000-00000000a005', 'item_weight', 'Item weight',
     'Weight of the book itself, without packaging. Used to quote shipping.',
     NULL, 'measure', 'mass', 'g',
     0, NULL, NULL, NULL, NULL, NULL,
     'Logistics', 'item', FALSE, FALSE, FALSE),

    ('00000000-0000-4000-8000-00000000a006', 'language', 'Language',
     'Every language the text is printed in.',
     NULL, 'multi_enum', NULL, NULL,
     NULL, NULL, NULL, NULL, NULL, 5,
     'Product Details', 'item', FALSE, TRUE, FALSE),

    ('00000000-0000-4000-8000-00000000a007', 'publication_date', 'Publication date',
     'The date this edition was published.',
     NULL, 'date', NULL, NULL,
     NULL, NULL, NULL, NULL, NULL, NULL,
     'Product Identity', 'item', FALSE, FALSE, FALSE)
ON CONFLICT (code) DO NOTHING;   -- an operator's own edits to a seeded definition are not this file's to undo

-- Enum options and bindings resolve the definition through `code`, not through
-- the literal id above. On a database where an operator had already created
-- `binding` by hand, the DO NOTHING left that row's own id in place, and a
-- literal here would fail the foreign key on a schema that is otherwise fine.
INSERT INTO attribute_enum_values (definition_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
  FROM attribute_definitions d
  JOIN (VALUES
        ('binding',  'hardcover', 'Hardcover',    10),
        ('binding',  'paperback', 'Paperback',    20),
        ('binding',  'spiral',    'Spiral bound', 30),
        ('language', 'en', 'English', 10),
        ('language', 'hi', 'Hindi',   20),
        ('language', 'te', 'Telugu',  30),
        ('language', 'ta', 'Tamil',   40),
        ('language', 'bn', 'Bengali', 50)
       ) AS v(def_code, code, label, sort_order) ON v.def_code = d.code
ON CONFLICT (definition_id, code) DO NOTHING;

-- Bound to Books. Textbooks inherits all seven and overrides none, which is
-- the case the schema endpoint is demonstrated on.
INSERT INTO category_attributes (category_id, definition_id, is_required, sort_order)
SELECT c.id, d.id, b.is_required, b.sort_order
  FROM product_categories c
  JOIN (VALUES
        ('gtin',             TRUE,  10),
        ('publication_date', FALSE, 20),
        ('author',           FALSE, 10),
        ('binding',          FALSE, 20),
        ('pages',            FALSE, 30),
        ('language',         FALSE, 40),
        ('item_weight',      FALSE, 10)
       ) AS b(def_code, is_required, sort_order) ON TRUE
  JOIN attribute_definitions d ON d.code = b.def_code
 WHERE c.slug = 'books-and-stationery'
ON CONFLICT (category_id, definition_id) DO NOTHING;
