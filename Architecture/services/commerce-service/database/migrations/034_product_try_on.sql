-- Commerce — migration 034: which products can be tried on, and with what.
--
-- Face AR try-on needs three facts a client cannot derive on its own: that
-- THIS product is meant to be tried on, WHICH AR effect renders it, and which
-- of the product's colourways map to which parameters inside that effect.
--
-- ─── WHY THIS IS NOT A product_attributes ROW ───────────────────────────
--
-- Migration 025 built the typed attribute system, and a shade set with swatch
-- artwork would fit `attribute_enum_values` neatly. It is still the wrong
-- home. An attribute is a filterable FACT about a product that a buyer might
-- search on — an author, a page count, a fabric. A try-on descriptor is
-- rendering metadata for one client capability: nobody filters a catalogue by
-- "effect slug", and putting it in the attribute system would make it a
-- published, category-bound, searchable field that then has to be versioned
-- through `attribute_schema_state` every time an effect is renamed.
--
-- ─── WHY CAPABILITY IS PER PRODUCT AND NEVER DERIVED FROM CATEGORY ──────
--
-- The obvious shortcut is to call every product under `beauty-and-personal-care`
-- try-on capable. That would be a promise the platform cannot keep: an AR
-- effect is authored content, one bundle per look, and a category has
-- thousands of products and no bundles. A "Try on" button that opens a camera
-- and renders nothing is worse than no button, so capability is opt-in, one
-- row per product, and the absence of a row is the absence of the button.
--
-- Category still has a say, but only as a FENCE: `kind` must be one the
-- product's category tree admits, so a lipstick cannot be published as
-- eyewear. That check lives in the service, where the category tree is
-- already walked, rather than in a trigger that cannot see ancestors cheaply.
--
-- ─── THE SHAPE OF `variants` ────────────────────────────────────────────
--
-- A JSONB array of {id, label, hex, js?} objects, ordered as the client
-- should present them. `id` is the product variant id when the look maps to a
-- purchasable colourway, so the try-on strip and the buy strip agree; it may
-- also be a free label for a look that is not separately purchasable. `js` is
-- the optional effect-specific call when a hex alone does not drive the
-- effect. JSONB rather than a child table because the whole array is read and
-- written as one unit, is never joined on, and is authored by whoever
-- prepares the effect bundle — a child table would add a migration to every
-- new effect parameter and buy nothing.
--
-- Expand-only: a new table plus a foreign key. No existing column changes, no
-- existing write path narrows, and a product with no row behaves exactly as
-- it does today.

CREATE TABLE IF NOT EXISTS product_try_on (
    product_id  UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('eyewear', 'makeup', 'jewellery', 'watch')),
    effect_slug TEXT NOT NULL CHECK (effect_slug <> '' AND effect_slug ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    variants    JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(variants) = 'array'),
    -- Who last published this descriptor. A try-on look is merchandising
    -- content that a buyer sees on their own face; "who put this here" has to
    -- be answerable without reconstructing it from application logs.
    updated_by  UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The read is always by product id, which the primary key already serves.
-- This index serves the opposite question — "every product using this effect"
-- — which is what a bundle swap or an effect retirement has to ask.
CREATE INDEX IF NOT EXISTS idx_product_try_on_effect
    ON product_try_on (effect_slug);
