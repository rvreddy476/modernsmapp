-- 051 — a retried stock adjustment applies once.
--
-- Commerce Android audit 2026-10-30 M3. PATCH /seller/variants/:id/stock
-- takes a delta. A +50 restock whose response was lost to a timeout was
-- offered again by the app, and a second Save made it +100: the shop then
-- sold 50 units that did not exist.
--
-- The route now accepts an optional Idempotency-Key, as checkout does. The
-- key is stored on the adjustment it produced, unique per seller, with the
-- level the adjustment left, so a retry with the same key and the same
-- request answers that level without moving stock, and the same key with a
-- different request is refused. A request without the header behaves as
-- before.
--
-- Expand-only: three nullable columns no older image writes, and a partial
-- unique index over rows that carry a key (there are none before this).

ALTER TABLE inventory_adjustments ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE inventory_adjustments ADD COLUMN IF NOT EXISTS total_after     INT;
ALTER TABLE inventory_adjustments ADD COLUMN IF NOT EXISTS reserved_after  INT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_inventory_adjustments_seller_idem
    ON inventory_adjustments (seller_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
