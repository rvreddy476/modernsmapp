-- Commerce — migration 038: MStore coupons (seller and platform) and the
-- bank-offer record on an order.
--
-- Founder, 1 Oct 2026: COUPONS = "Both" — seller coupons now; platform
-- coupons built behind COMMERCE_PLATFORM_COUPONS_ENABLED, which stays OFF
-- until the tax adviser confirms GST on platform-funded discounts. BANK
-- OFFERS = "Yes, via Razorpay Offers" (payments-service migration 014 owns
-- the registry and the capture rule; commerce only records what the
-- payment.succeeded event says the offer did, for display).
--
-- ─── COUPONS: THE PAISE COLUMNS ALREADY EXIST ───────────────────────────
--
-- Migration 007 added the integer columns the money path reads:
--
--   discount_value_minor       flat discount, paise
--   discount_basis_points      percentage discount, basis points (12.5% = 1250)
--   max_discount_amount_minor  cap, paise
--   min_order_amount_minor     minimum, paise
--
-- The contract's names (max_discount_minor, min_order_minor) are the WIRE
-- names; adding a second copy of each column would give the money path two
-- truths to disagree about. This migration only re-runs 007's exact ×100
-- backfill for any row written since through the rupee columns alone (the
-- NUMERIC(10,2) × 100 is exact, so ROUND changes nothing; it is there to
-- make the cast to BIGINT explicit).
--
-- ─── NEW ────────────────────────────────────────────────────────────────
--
--   coupons.is_public   shown on product pages and in the bag's list
--                       (TRUE) or a secret code typed by hand (FALSE)
--   coupons.funded_by   'seller' | 'platform', DERIVED from seller_id
--                       (NULL = platform) and held to it by a CHECK, so the
--                       two can never disagree
--   coupons.created_by, updated_at
--
--   orders.offer_id, offer_title, offer_discount_minor, offer_funded_by,
--   captured_minor      what payment.succeeded says a bank offer did. The
--                       ORDER VALUE (final_amount_minor) is unchanged; the
--                       buyer paid captured_minor through the bank's offer.
--   orders.platform_discount_minor
--                       a platform-funded coupon's discount, recorded apart
--                       from the seller's (adviser-pending; written only
--                       while COMMERCE_PLATFORM_COUPONS_ENABLED is on)
--
-- And the admin audit log learns the two coupon actions.

-- ── coupons ─────────────────────────────────────────────────────────────

UPDATE coupons
   SET discount_value_minor = ROUND(discount_value * 100)::bigint
 WHERE discount_value_minor IS NULL AND discount_type <> 'percentage';
UPDATE coupons
   SET discount_basis_points = ROUND(discount_value * 100)::int
 WHERE discount_basis_points IS NULL AND discount_type = 'percentage';
UPDATE coupons
   SET max_discount_amount_minor = ROUND(max_discount_amount * 100)::bigint
 WHERE max_discount_amount_minor IS NULL AND max_discount_amount IS NOT NULL;
UPDATE coupons
   SET min_order_amount_minor = ROUND(min_order_amount * 100)::bigint
 WHERE min_order_amount_minor IS NULL;

ALTER TABLE coupons
    ADD COLUMN IF NOT EXISTS is_public  BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS funded_by  TEXT,
    ADD COLUMN IF NOT EXISTS created_by UUID,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE coupons
   SET funded_by = CASE WHEN seller_id IS NULL THEN 'platform' ELSE 'seller' END
 WHERE funded_by IS NULL;

ALTER TABLE coupons ALTER COLUMN funded_by SET NOT NULL;

-- funded_by is DERIVED: every insert, and every change of seller_id, sets it
-- from seller_id, so no writer can state it wrongly (and one that does not
-- state it at all still satisfies NOT NULL).
CREATE OR REPLACE FUNCTION coupons_derive_funded_by()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.funded_by := CASE WHEN NEW.seller_id IS NULL THEN 'platform' ELSE 'seller' END;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_coupons_derive_funded_by ON coupons;
CREATE TRIGGER trg_coupons_derive_funded_by
    BEFORE INSERT OR UPDATE OF seller_id, funded_by ON coupons
    FOR EACH ROW EXECUTE FUNCTION coupons_derive_funded_by();

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'coupons'::regclass AND conname = 'chk_coupon_funded_by') THEN
        ALTER TABLE coupons ADD CONSTRAINT chk_coupon_funded_by
            CHECK ((funded_by = 'platform' AND seller_id IS NULL)
                OR (funded_by = 'seller'   AND seller_id IS NOT NULL));
    END IF;
    -- Every coupon a buyer can redeem carries an integer amount: a
    -- percentage its basis points (1..10000), a flat coupon its paise.
    -- NOT VALID because free_shipping / buy_x_get_y rows from the original
    -- schema may carry neither; new rows are checked from now on.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'coupons'::regclass AND conname = 'chk_coupon_amount_minor') THEN
        ALTER TABLE coupons ADD CONSTRAINT chk_coupon_amount_minor
            CHECK (discount_type NOT IN ('percentage','flat')
                OR (discount_type = 'percentage' AND discount_basis_points BETWEEN 1 AND 10000)
                OR (discount_type = 'flat' AND discount_value_minor > 0)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'coupons'::regclass AND conname = 'chk_coupon_limits_minor') THEN
        ALTER TABLE coupons ADD CONSTRAINT chk_coupon_limits_minor
            CHECK ((max_discount_amount_minor IS NULL OR max_discount_amount_minor > 0)
               AND (min_order_amount_minor IS NULL OR min_order_amount_minor >= 0)
               AND (max_uses IS NULL OR max_uses > 0)
               AND max_uses_per_user > 0) NOT VALID;
    END IF;
END$$;

-- The product page asks "the best live public coupon of this seller".
CREATE INDEX IF NOT EXISTS idx_coupons_seller_public_live
    ON coupons (seller_id)
 WHERE is_active = TRUE AND is_public = TRUE AND seller_id IS NOT NULL;

-- ── orders: the bank offer, as payment.succeeded reported it ────────────

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS offer_id                UUID,
    ADD COLUMN IF NOT EXISTS offer_title             TEXT,
    ADD COLUMN IF NOT EXISTS offer_discount_minor    BIGINT,
    ADD COLUMN IF NOT EXISTS offer_funded_by         TEXT,
    ADD COLUMN IF NOT EXISTS captured_minor          BIGINT,
    ADD COLUMN IF NOT EXISTS platform_discount_minor BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'orders'::regclass AND conname = 'chk_order_offer_minor') THEN
        ALTER TABLE orders ADD CONSTRAINT chk_order_offer_minor
            CHECK ((offer_discount_minor IS NULL OR offer_discount_minor > 0)
               AND (captured_minor IS NULL OR captured_minor > 0)
               AND (platform_discount_minor IS NULL OR platform_discount_minor >= 0));
    END IF;
END$$;

-- ── the admin audit log learns the coupon actions ───────────────────────
--
-- 036's CHECKs were inline, so they carry Postgres's generated names. They
-- are replaced, not loosened: the old values are all still allowed.

ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS commerce_admin_audit_log_action_check;
ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS commerce_admin_audit_log_target_type_check;
ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS chk_admin_audit_action;
ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS chk_admin_audit_target_type;
ALTER TABLE commerce_admin_audit_log ADD CONSTRAINT chk_admin_audit_action
    CHECK (action IN ('cod_remittance_settle','seller_kyc_verify',
                      'banner_create','banner_update','banner_delete',
                      'coupon_create','coupon_update'));
ALTER TABLE commerce_admin_audit_log ADD CONSTRAINT chk_admin_audit_target_type
    CHECK (target_type IN ('cod_remittance','seller','banner','coupon'));
