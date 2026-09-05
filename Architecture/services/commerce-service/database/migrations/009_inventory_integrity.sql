-- Commerce P0 — migration 009: inventory becomes auditable and un-oversellable.
--
-- LB-21, LB-22, LB-23. Three defects, one schema answer.
--
--   * Reservations were released by `DELETE … LIMIT 1`, which PostgreSQL
--     rejects outright, AND the predicate required `order_id IS NULL`, which
--     never matches a checkout reservation. Fixing the syntax alone would
--     still have released nothing (v1 §5.9, review §2.1).
--   * Stock arithmetic used `GREATEST(0, total_qty - qty)`, so an oversell
--     was clamped to zero and hidden rather than raised.
--   * Nothing recorded WHY stock moved, so a discrepancy could not be traced
--     after the fact.
--
-- Expand phase: the invariant constraint is NOT VALID here and validated in
-- 013 once every old writer is drained.

-- ─── Reservation identity (LB-21) ────────────────────────────────────
--
-- A checkout reservation is now addressable by exactly (order_id, variant_id),
-- which is what release needs in order to release the right rows and only
-- the right rows.

CREATE UNIQUE INDEX IF NOT EXISTS idx_reservation_order_variant
    ON inventory_reservations (order_id, variant_id)
    WHERE order_id IS NOT NULL;

ALTER TABLE inventory_reservations
    ADD COLUMN IF NOT EXISTS released_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS committed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS release_reason TEXT;

-- Only live reservations count toward reserved_qty; the row is kept for the
-- audit trail rather than deleted, so a released hold can still be explained.
CREATE INDEX IF NOT EXISTS idx_reservations_live
    ON inventory_reservations (variant_id, expires_at)
    WHERE released_at IS NULL AND committed_at IS NULL;

-- ─── Inventory ledger (LB-23) ────────────────────────────────────────
--
-- Append-only. Every movement carries its reason and its cause, so
-- "reserved + delta history == current" is checkable rather than assumed,
-- and the nightly assertion in the acceptance criteria has something to
-- assert against.

CREATE TABLE IF NOT EXISTS inventory_ledger (
    id          BIGSERIAL PRIMARY KEY,
    variant_id  UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    order_id    UUID,
    reservation_id UUID,
    -- Positive adds availability, negative removes it.
    delta_total    INT NOT NULL DEFAULT 0,
    delta_reserved INT NOT NULL DEFAULT 0,
    reason TEXT NOT NULL CHECK (reason IN (
        'checkout_reserve',
        'checkout_release_cancel',
        'checkout_release_expiry',
        'checkout_release_payment_failed',
        'payment_commit',
        'seller_adjust',
        'return_restock',
        'correction'
    )),
    actor_id   UUID,
    actor_type TEXT NOT NULL DEFAULT 'system'
        CHECK (actor_type IN ('system','customer','seller','admin')),
    notes      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inventory_ledger_variant ON inventory_ledger (variant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_inventory_ledger_order   ON inventory_ledger (order_id) WHERE order_id IS NOT NULL;

-- ─── The invariant (LB-23) ───────────────────────────────────────────
--
-- `chk_inv_qty` already forbids negatives. This adds the one that actually
-- prevents an oversell: you cannot reserve more than exists. With it in
-- place the checkout transaction does not need to trust its own arithmetic —
-- a concurrent reservation that would breach the invariant raises, and the
-- whole checkout rolls back.
--
-- NOT VALID: an old replica that still clamps could otherwise be rejected
-- mid-rollout. Validated in 013.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'inventory_items'::regclass AND conname = 'chk_inv_reserved_le_total'
    ) THEN
        ALTER TABLE inventory_items
            ADD CONSTRAINT chk_inv_reserved_le_total
            CHECK (reserved_qty <= total_qty) NOT VALID;
    END IF;
END$$;

-- ─── Terminal order expiry (LB-22 / review M-5) ──────────────────────
--
-- The expiry sweeper released reservations while the order was still
-- `payment_pending`, and a late capture then applied anyway with its stock
-- errors merely logged. A's hold expires, B buys the last unit, A's delayed
-- capture arrives — A is charged and two orders exist against one unit.
--
-- Expiry now TERMINATES the order, and this column is what makes that
-- decision durable and visible to the late-capture path.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS reservation_expired_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS late_capture_refund_id UUID;

CREATE INDEX IF NOT EXISTS idx_orders_reservation_expired
    ON orders (reservation_expired_at) WHERE reservation_expired_at IS NOT NULL;

-- ─── Coupon capacity (LB-16 / review M-6) ────────────────────────────
--
-- Caps were read during pricing and incremented after the order was created,
-- with the increment's error ignored. Fifty concurrent checkouts all passed
-- a one-use ₹500 coupon.
--
-- `uses_count` becomes a claimed counter, incremented conditionally inside
-- the checkout transaction, and this constraint makes over-redemption
-- impossible rather than unlikely.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'coupons'::regclass AND conname = 'chk_coupon_uses_within_max'
    ) THEN
        ALTER TABLE coupons
            ADD CONSTRAINT chk_coupon_uses_within_max
            CHECK (max_uses IS NULL OR uses_count <= max_uses) NOT VALID;
    END IF;
END$$;

-- Per-user cap, enforced by the unique index rather than by a read.
CREATE UNIQUE INDEX IF NOT EXISTS idx_coupon_usage_unique
    ON coupon_usages (coupon_id, order_id);
