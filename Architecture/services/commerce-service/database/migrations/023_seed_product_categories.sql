-- 023 — seed the marketplace's top-level category taxonomy.
--
-- `GET /v1/commerce/categories` answered `null`. Not an empty array, not an
-- error: the table has existed since setup.sql and has never had a row put
-- in it, so ListCategories returned a nil slice and the client rendered
-- nothing. Everything downstream of it was decorative as a result —
-- `POST /products` takes an optional `category_id`, the browse endpoints take
-- `?category_id=`, and the catalogue's LATERAL price/stock joins are indexed
-- on `products.category_id`. A filter with nothing behind it cannot be used,
-- and a seller had no legal value to send.
--
-- ─── WHY THE IDS ARE FIXED, NOT gen_random_uuid() ───────────────────────
--
-- A category id is a value CLIENTS keep: a saved filter, a deep link, a
-- product row that already points at one. Generating ids here would make
-- them differ between dev, staging and production and between one rebuild of
-- a dev database and the next, so every stored reference would rot. These
-- are deterministic literals in a reserved 0000-0000-0000-0000-c0mm3rc3xxxx
-- shape, so "Electronics" is the same id everywhere, forever.
--
-- ─── WHY IT IS AN UPSERT ON slug ────────────────────────────────────────
--
-- The migration runner applies a file once, but a database restored from a
-- snapshot taken after a manual insert, or one where an operator has already
-- created "Electronics" by hand, must not fail on the UNIQUE(slug). So the
-- insert converges on slug: an existing row keeps its id (that is the whole
-- point — its products still point at it) and has its name and ordering
-- brought in line. Nothing is deleted and nothing is deactivated: a category
-- an operator added deliberately is not this file's business.
--
-- Twelve top-level categories, chosen for an Indian marketplace and ordered
-- the way the browse grid should read. Sub-categories are deliberately NOT
-- seeded — the depth a catalogue needs is a merchandising decision, and
-- `parent_id` is there for when someone makes it.

INSERT INTO product_categories (id, parent_id, name, slug, description, display_order, is_active, is_featured)
VALUES
    ('00000000-0000-4000-8000-00000000c001', NULL, 'Electronics',            'electronics',            'Phones, laptops, audio, cameras and accessories',        10, TRUE, TRUE),
    ('00000000-0000-4000-8000-00000000c002', NULL, 'Fashion',                'fashion',                'Clothing, footwear and accessories for men and women',   20, TRUE, TRUE),
    ('00000000-0000-4000-8000-00000000c003', NULL, 'Home & Kitchen',         'home-and-kitchen',       'Cookware, furnishings, storage and home improvement',    30, TRUE, TRUE),
    ('00000000-0000-4000-8000-00000000c004', NULL, 'Beauty & Personal Care', 'beauty-and-personal-care','Skincare, haircare, grooming and fragrances',           40, TRUE, TRUE),
    ('00000000-0000-4000-8000-00000000c005', NULL, 'Grocery & Gourmet',      'grocery-and-gourmet',    'Staples, snacks, beverages and packaged foods',          50, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c006', NULL, 'Health & Wellness',      'health-and-wellness',    'Nutrition, fitness, medical devices and ayurveda',       60, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c007', NULL, 'Sports & Fitness',       'sports-and-fitness',     'Sportswear, equipment and outdoor gear',                 70, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c008', NULL, 'Books & Stationery',     'books-and-stationery',   'Books, office supplies and school stationery',           80, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c009', NULL, 'Toys & Baby',            'toys-and-baby',          'Toys, games, baby care and kids essentials',             90, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c00a', NULL, 'Jewellery & Watches',    'jewellery-and-watches',  'Fine and fashion jewellery, watches and eyewear',       100, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c00b', NULL, 'Automotive',             'automotive',             'Car and two-wheeler accessories, spares and care',      110, TRUE, FALSE),
    ('00000000-0000-4000-8000-00000000c00c', NULL, 'Handicrafts & Decor',    'handicrafts-and-decor',  'Handmade crafts, art, festive and home decor',          120, TRUE, TRUE)
ON CONFLICT (slug) DO UPDATE SET
    name          = EXCLUDED.name,
    description   = EXCLUDED.description,
    display_order = EXCLUDED.display_order,
    is_featured   = EXCLUDED.is_featured,
    updated_at    = NOW();
