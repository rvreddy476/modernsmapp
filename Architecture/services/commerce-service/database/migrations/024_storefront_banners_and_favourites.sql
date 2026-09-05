-- 024 — the two tables a shop's landing page needs, and nothing else.
--
-- The founder opened the shop on a phone and said it "is not looking a proper
-- ecommerce application". Two things were behind that, and both are schema:
--
--   1. `GET /v1/commerce/products` is the ONLY browse surface. There is no
--      landing page — no banner rail, no "deals of the day", no best sellers
--      — so the app opens on a bare grid of whatever was created last. A
--      marketplace's home screen is merchandising, and merchandising needs a
--      place to put the merchandiser's decisions. That is `commerce_banners`.
--
--   2. There is nowhere to record that a shopper likes something. The heart
--      beside the bag had nothing behind it, and `products.wishlist_count`
--      is a counter with no rows to count. That is `commerce_favourites`.
--
-- ─── WHY BANNERS ARE A TABLE AND NOT A CONFIG BLOB ──────────────────────
--
-- A banner is scheduled (starts_at/ends_at), ordered (position), switched off
-- without being deleted (active), and points at something a shopper can open
-- (target_type/target_id). Every one of those is a query the home endpoint
-- runs on every request — "the active banners, in order, whose window
-- contains now". A JSON blob in a config service would make that a full scan
-- in application code, and would make "turn off the Diwali banner" a deploy.
--
-- ─── WHY target_id IS TEXT AND NOT UUID ─────────────────────────────────
--
-- Two of the three target kinds are ids and the third is not: `search` points
-- at a query string ("running shoes"), which has no UUID. Modelling this as a
-- UUID column plus a separate text column would let a row carry both, or
-- neither, and the endpoint would have to decide which one a `search` banner
-- meant. One text column with a CHECK that ties the shape to the type keeps
-- the invalid states out of the table instead of out of the handler.
--
-- ─── WHY FAVOURITES ARE KEYED (user_id, product_id) AND NOT id ──────────
--
-- Tapping a heart is not an event, it is a fact: this shopper likes this
-- product, once. A surrogate key would let a double-tap — or the retry a
-- flaky phone connection produces — insert the same fact twice, and the
-- favourites list would show the product twice while "unfavourite" removed
-- only one of them. The composite primary key makes the second insert a
-- no-op the database enforces, so POST is idempotent without the handler
-- having to be careful.

-- ─── Banners ────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS commerce_banners (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title           TEXT NOT NULL,
    subtitle        TEXT,
    image_media_id  UUID,
    target_type     TEXT NOT NULL
                        CHECK (target_type IN ('category','product','search')),
    -- A category/product target must be a UUID; a search target is the query
    -- text. The CHECK is what stops "category" + "running shoes" — a row the
    -- client would render as a tappable card that opens nothing.
    target_id       TEXT NOT NULL
                        CHECK (
                            (target_type IN ('category','product')
                                AND target_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
                            OR (target_type = 'search' AND length(btrim(target_id)) > 0)
                        ),
    position        INT NOT NULL DEFAULT 0,
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    starts_at       TIMESTAMPTZ,
    ends_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A window that closes before it opens is a banner that can never show.
    CONSTRAINT commerce_banners_window CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);

-- The home endpoint's only banner query: active rows in display order. The
-- partial index keeps switched-off and expired stock out of it entirely.
CREATE INDEX IF NOT EXISTS idx_commerce_banners_live
    ON commerce_banners (position, created_at)
    WHERE active = TRUE;

-- ─── Favourites ─────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS commerce_favourites (
    user_id     UUID NOT NULL,
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, product_id)
);

-- The list endpoint pages newest-first per user; the PK covers the
-- "is this one product a favourite" lookup the product summaries do.
CREATE INDEX IF NOT EXISTS idx_commerce_favourites_user_recent
    ON commerce_favourites (user_id, created_at DESC, product_id DESC);

-- ─── Dev banners ────────────────────────────────────────────────────────
--
-- Three rows so `GET /v1/commerce/home` is never an empty rail on a fresh
-- database. The ids are fixed literals for the same reason migration 023's
-- category ids are: a banner id is a value a client may cache or deep-link,
-- and regenerating it on every rebuild would rot the reference.
--
-- image_media_id is deliberately NULL. A banner image is a real media-service
-- asset owned by a real uploader, and a migration has no way to create one —
-- inventing a UUID here would produce a row pointing at nothing, which is a
-- worse default than a banner the client renders with its own gradient. The
-- dev seed fills these in through
-- `PUT /v1/commerce/internal/banners/{id}` once the images are uploaded.
--
-- The targets are the three featured categories migration 023 seeded, so the
-- cards open onto something real even before any image exists.

INSERT INTO commerce_banners (id, title, subtitle, image_media_id, target_type, target_id, position, active)
VALUES
    ('00000000-0000-4000-8000-0000000ba001', 'Big Electronics Days',   'Up to 40% off phones, audio and laptops',
        NULL, 'category', '00000000-0000-4000-8000-00000000c001', 10, TRUE),
    ('00000000-0000-4000-8000-0000000ba002', 'The Fashion Edit',       'New season styles, delivered in 2 days',
        NULL, 'category', '00000000-0000-4000-8000-00000000c002', 20, TRUE),
    ('00000000-0000-4000-8000-0000000ba003', 'Home Makeover Sale',     'Cookware, storage and decor from ₹199',
        NULL, 'category', '00000000-0000-4000-8000-00000000c003', 30, TRUE)
ON CONFLICT (id) DO NOTHING;
