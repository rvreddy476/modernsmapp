-- Commerce P0 — migration 007: integer paise becomes the money type.
--
-- LB-19 / v1 §5.10. commerce-service carried money as float64 rupees while
-- payments-service has been paise-native since its own migration 006. Every
-- hop between them was `int64(math.Round(x*100))`, so the two services
-- disagreed about what a rupee was and each round trip could lose a paise.
--
-- Expand phase ONLY (review §5.1). Every column added here is nullable or
-- defaulted, nothing is dropped, and no constraint is added that a replica
-- still running the old code could trip. The NUMERIC columns remain the
-- authority until reads flip; a later migration validates and contracts.
--
-- This deliberately mirrors payments migration 006 line for line, including
-- the resumable backfill predicate, so the two services fail the same way if
-- they fail at all.

-- ─── orders ──────────────────────────────────────────────────────────

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS subtotal_minor         BIGINT,
    ADD COLUMN IF NOT EXISTS discount_amount_minor  BIGINT,
    ADD COLUMN IF NOT EXISTS shipping_charges_minor BIGINT,
    ADD COLUMN IF NOT EXISTS tax_amount_minor       BIGINT,
    ADD COLUMN IF NOT EXISTS coupon_discount_minor  BIGINT,
    ADD COLUMN IF NOT EXISTS final_amount_minor     BIGINT;

-- Resumable: only rows that have not been converted are touched, so an
-- interrupted backfill picks up where it stopped.
UPDATE orders
   SET subtotal_minor         = COALESCE(subtotal_minor,         ROUND(subtotal         * 100)),
       discount_amount_minor  = COALESCE(discount_amount_minor,  ROUND(discount_amount  * 100)),
       shipping_charges_minor = COALESCE(shipping_charges_minor, ROUND(shipping_charges * 100)),
       tax_amount_minor       = COALESCE(tax_amount_minor,       ROUND(tax_amount       * 100)),
       coupon_discount_minor  = COALESCE(coupon_discount_minor,  ROUND(coupon_discount  * 100)),
       final_amount_minor     = COALESCE(final_amount_minor,     ROUND(final_amount     * 100))
 WHERE subtotal_minor IS NULL
    OR discount_amount_minor IS NULL
    OR shipping_charges_minor IS NULL
    OR tax_amount_minor IS NULL
    OR coupon_discount_minor IS NULL
    OR final_amount_minor IS NULL;

ALTER TABLE orders
    ALTER COLUMN subtotal_minor         SET DEFAULT 0,
    ALTER COLUMN discount_amount_minor  SET DEFAULT 0,
    ALTER COLUMN shipping_charges_minor SET DEFAULT 0,
    ALTER COLUMN tax_amount_minor       SET DEFAULT 0,
    ALTER COLUMN coupon_discount_minor  SET DEFAULT 0,
    ALTER COLUMN final_amount_minor     SET DEFAULT 0;

-- ─── order_items ─────────────────────────────────────────────────────

ALTER TABLE order_items
    ADD COLUMN IF NOT EXISTS unit_mrp_minor        BIGINT,
    ADD COLUMN IF NOT EXISTS unit_price_minor      BIGINT,
    ADD COLUMN IF NOT EXISTS discount_amount_minor BIGINT,
    ADD COLUMN IF NOT EXISTS tax_amount_minor      BIGINT,
    ADD COLUMN IF NOT EXISTS final_price_minor     BIGINT;

UPDATE order_items
   SET unit_mrp_minor        = COALESCE(unit_mrp_minor,        ROUND(unit_mrp        * 100)),
       unit_price_minor      = COALESCE(unit_price_minor,      ROUND(unit_price      * 100)),
       discount_amount_minor = COALESCE(discount_amount_minor, ROUND(discount_amount * 100)),
       tax_amount_minor      = COALESCE(tax_amount_minor,      ROUND(tax_amount      * 100)),
       final_price_minor     = COALESCE(final_price_minor,     ROUND(final_price     * 100))
 WHERE unit_mrp_minor IS NULL
    OR unit_price_minor IS NULL
    OR discount_amount_minor IS NULL
    OR tax_amount_minor IS NULL
    OR final_price_minor IS NULL;

ALTER TABLE order_items
    ALTER COLUMN unit_mrp_minor        SET DEFAULT 0,
    ALTER COLUMN unit_price_minor      SET DEFAULT 0,
    ALTER COLUMN discount_amount_minor SET DEFAULT 0,
    ALTER COLUMN tax_amount_minor      SET DEFAULT 0,
    ALTER COLUMN final_price_minor     SET DEFAULT 0;

-- ─── product_variants ────────────────────────────────────────────────

ALTER TABLE product_variants
    ADD COLUMN IF NOT EXISTS mrp_minor           BIGINT,
    ADD COLUMN IF NOT EXISTS selling_price_minor BIGINT,
    ADD COLUMN IF NOT EXISTS cost_price_minor    BIGINT;

UPDATE product_variants
   SET mrp_minor           = COALESCE(mrp_minor,           ROUND(mrp           * 100)),
       selling_price_minor = COALESCE(selling_price_minor, ROUND(selling_price * 100)),
       cost_price_minor    = COALESCE(cost_price_minor,    ROUND(COALESCE(cost_price,0) * 100))
 WHERE mrp_minor IS NULL OR selling_price_minor IS NULL OR cost_price_minor IS NULL;

ALTER TABLE product_variants
    ALTER COLUMN mrp_minor           SET DEFAULT 0,
    ALTER COLUMN selling_price_minor SET DEFAULT 0,
    ALTER COLUMN cost_price_minor    SET DEFAULT 0;

-- ─── coupons ─────────────────────────────────────────────────────────
--
-- discount_value is a percentage when discount_type='percentage', so only
-- the money-valued columns convert. Converting the percentage would be a
-- unit error, and is exactly the kind of blanket sweep that makes a money
-- migration dangerous.

ALTER TABLE coupons
    ADD COLUMN IF NOT EXISTS discount_value_minor      BIGINT,
    ADD COLUMN IF NOT EXISTS max_discount_amount_minor BIGINT,
    ADD COLUMN IF NOT EXISTS min_order_amount_minor    BIGINT;

UPDATE coupons
   SET discount_value_minor = COALESCE(
           discount_value_minor,
           CASE WHEN discount_type = 'percentage' THEN NULL ELSE ROUND(discount_value * 100) END),
       max_discount_amount_minor = COALESCE(max_discount_amount_minor, ROUND(max_discount_amount * 100)),
       min_order_amount_minor    = COALESCE(min_order_amount_minor,    ROUND(min_order_amount * 100))
 WHERE max_discount_amount_minor IS NULL OR min_order_amount_minor IS NULL
    OR (discount_value_minor IS NULL AND discount_type <> 'percentage');

ALTER TABLE coupons
    ALTER COLUMN min_order_amount_minor SET DEFAULT 0;

-- discount_basis_points holds a percentage rate without a float: 12.5%
-- becomes 1250. NULL for non-percentage coupons.
ALTER TABLE coupons
    ADD COLUMN IF NOT EXISTS discount_basis_points INT;

UPDATE coupons
   SET discount_basis_points = ROUND(discount_value * 100)
 WHERE discount_type = 'percentage' AND discount_basis_points IS NULL;

-- ─── cart_items ──────────────────────────────────────────────────────

ALTER TABLE cart_items
    ADD COLUMN IF NOT EXISTS price_snapshot_minor BIGINT;

UPDATE cart_items
   SET price_snapshot_minor = ROUND(price_snapshot * 100)
 WHERE price_snapshot_minor IS NULL;

ALTER TABLE cart_items
    ALTER COLUMN price_snapshot_minor SET DEFAULT 0;

-- ─── refunds ─────────────────────────────────────────────────────────

ALTER TABLE refunds
    ADD COLUMN IF NOT EXISTS amount_minor BIGINT;

UPDATE refunds
   SET amount_minor = ROUND(amount * 100)
 WHERE amount_minor IS NULL;

ALTER TABLE refunds
    ALTER COLUMN amount_minor SET DEFAULT 0;

-- ─── Verification support ────────────────────────────────────────────
--
-- Review §5.2 requires a CONTINUOUS old-vs-new comparison before reads flip,
-- not a one-time backfill. This view is what the verification job and the
-- deployment proof read; a non-empty result blocks the read flip.

CREATE OR REPLACE VIEW money_minor_drift AS
SELECT 'orders'    AS table_name, id, 'final_amount' AS column_name,
       final_amount AS major_value, final_amount_minor AS minor_value
  FROM orders
 WHERE final_amount_minor IS DISTINCT FROM ROUND(final_amount * 100)
UNION ALL
SELECT 'orders', id, 'subtotal', subtotal, subtotal_minor
  FROM orders
 WHERE subtotal_minor IS DISTINCT FROM ROUND(subtotal * 100)
UNION ALL
SELECT 'order_items', id, 'final_price', final_price, final_price_minor
  FROM order_items
 WHERE final_price_minor IS DISTINCT FROM ROUND(final_price * 100)
UNION ALL
SELECT 'product_variants', id, 'selling_price', selling_price, selling_price_minor
  FROM product_variants
 WHERE selling_price_minor IS DISTINCT FROM ROUND(selling_price * 100);
