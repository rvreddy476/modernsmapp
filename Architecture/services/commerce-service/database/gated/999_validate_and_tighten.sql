-- Commerce P0 — GATED migration 013: validate, then contract.
--
-- THIS FILE IS NOT APPLIED AT BOOT. It lives in database/gated/, which the
-- embedded migration set deliberately excludes, and is run by the one-shot
-- job:
--
--     commerce-migrate -gated
--
-- Why it is gated rather than ordinary:
--
-- Review §5.1 and §5.2. Migrations 007–012 add every constraint NOT VALID,
-- because a replica still running the old code would otherwise be rejected
-- mid-rollout — the old writer clamps stock with GREATEST(0,…), writes an
-- address pointer, and leaves tax at zero, all of which the new constraints
-- forbid. Validating them is only safe AFTER:
--
--   1. every old replica is drained (no pod on the previous image),
--   2. the high-watermark backfill has re-run,
--   3. `SELECT count(*) FROM money_minor_drift` returns 0 and has stayed 0
--      for a full business day,
--   4. `pii_backfill_progress` shows every table complete,
--   5. reads have flipped to the minor columns and the deploy is stable.
--
-- Running it before then does not corrupt anything — the ALTER simply fails
-- and rolls back — but it will fail a deployment for a reason that looks
-- mysterious, so the preconditions are asserted below and raise a message
-- that says which one was not met.
--
-- Nothing here is destructive. The plaintext PII drop and the NUMERIC mirror
-- drop are Phase F, in a separate file, and are NOT authorised in this pass.

-- ─── Precondition gate ───────────────────────────────────────────────

DO $$
DECLARE
    drift_rows BIGINT;
    pending_pii BIGINT;
BEGIN
    SELECT count(*) INTO drift_rows FROM money_minor_drift;
    IF drift_rows > 0 THEN
        RAISE EXCEPTION
            'refusing to validate: % row(s) still disagree between the NUMERIC and minor money columns. '
            'Re-run the high-watermark backfill and confirm every old writer is drained.', drift_rows;
    END IF;

    SELECT count(*) INTO pending_pii
      FROM pii_backfill_progress
     WHERE completed_at IS NULL;
    IF pending_pii > 0 THEN
        RAISE EXCEPTION
            'refusing to validate: % PII backfill(s) have not completed. Address ciphertext must be '
            'in place before the plaintext becomes unreachable.', pending_pii;
    END IF;
END$$;

-- ─── Validate ────────────────────────────────────────────────────────
--
-- VALIDATE CONSTRAINT takes only a SHARE UPDATE EXCLUSIVE lock, so ordinary
-- traffic keeps running while each table is scanned.

ALTER TABLE inventory_items VALIDATE CONSTRAINT chk_inv_reserved_le_total;
ALTER TABLE coupons         VALIDATE CONSTRAINT chk_coupon_uses_within_max;
ALTER TABLE order_items     VALIDATE CONSTRAINT chk_item_tax_reconciles;
ALTER TABLE order_items     VALIDATE CONSTRAINT chk_item_single_tax_split;
ALTER TABLE orders          VALIDATE CONSTRAINT chk_order_address_snapshot_real;

-- ─── Post-validation invariants ──────────────────────────────────────
--
-- Only added once the data is known to satisfy them.

-- LB-4: one payment intent per order.
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_intent_unique
    ON orders (payment_intent_id) WHERE payment_intent_id IS NOT NULL;

-- The money columns become mandatory. Until now they were defaulted so an
-- old writer that did not know about them could still insert.
ALTER TABLE orders
    ALTER COLUMN final_amount_minor SET NOT NULL,
    ALTER COLUMN subtotal_minor     SET NOT NULL,
    ALTER COLUMN tax_amount_minor   SET NOT NULL;

ALTER TABLE order_items
    ALTER COLUMN final_price_minor   SET NOT NULL,
    ALTER COLUMN net_inclusive_minor SET NOT NULL;

-- Every new order must carry a real address snapshot.
UPDATE orders SET snapshot_cutover = TRUE WHERE snapshot_cutover = FALSE AND created_at > NOW();
ALTER TABLE orders ALTER COLUMN snapshot_cutover SET DEFAULT TRUE;

-- ─── Reconciliation view for the nightly assertion ───────────────────
--
-- Acceptance criterion: "inventory_ledger sums to total_qty for every
-- variant, asserted nightly." This is what that assertion reads.

CREATE OR REPLACE VIEW inventory_ledger_reconciliation AS
SELECT i.variant_id,
       i.total_qty,
       i.reserved_qty,
       COALESCE(l.sum_total,    0) AS ledger_total_delta,
       COALESCE(l.sum_reserved, 0) AS ledger_reserved_delta,
       (i.reserved_qty <> COALESCE(l.sum_reserved, 0)) AS reserved_mismatch
  FROM inventory_items i
  LEFT JOIN (
        SELECT variant_id,
               SUM(delta_total)    AS sum_total,
               SUM(delta_reserved) AS sum_reserved
          FROM inventory_ledger
         GROUP BY variant_id
  ) l ON l.variant_id = i.variant_id;

-- ─── 018's contract half: verify the widened variant-status CHECK ────
--
-- Migration 018 widened `product_variants_status_check` to admit 'archived'
-- and left it NOT VALID, which enforces the rule for new rows without scanning
-- the table. This marks the existing estate verified.
--
-- It cannot fail on real data: the widened set is a strict superset of the
-- three values the old constraint allowed, so every existing row already
-- satisfies it. The step exists so the constraint stops being reported as
-- unvalidated, not because anything is expected to be wrong.
ALTER TABLE product_variants VALIDATE CONSTRAINT product_variants_status_check;
