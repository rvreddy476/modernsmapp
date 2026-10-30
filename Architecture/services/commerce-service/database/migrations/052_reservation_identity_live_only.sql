-- 052 — one LIVE hold per (order, variant), not one hold ever.
--
-- Migration 009 made a checkout reservation addressable by (order_id,
-- variant_id) with a unique index over every row. Releases do not delete the
-- row (it is kept, released_at set, so a released hold can still be
-- explained), so once payment.failed released an order's holds, the payment
-- retry (RetryPaymentReservation, which re-holds the same lines) could never
-- insert its new hold: every retry of a checked-out order failed with
-- 23505 idx_reservation_order_variant. Found by the coupon-release
-- integration test (commerce audit 2026-10-30 M3); the earlier retry tests
-- used a fixture order with no reservation rows and never reached the insert.
--
-- The identity release needs is "the live hold for this order and variant",
-- so the uniqueness now covers rows that are not released. A released row
-- stays as history beside the new live one.
--
-- Expand-only: the new index is a strict relaxation of the old one (every
-- set of rows the old index admitted, this one admits), so no writer of any
-- image is refused. It is built before the old one is dropped, so there is
-- no moment with neither. No code uses the index for ON CONFLICT.

CREATE UNIQUE INDEX IF NOT EXISTS idx_reservation_order_variant_live
    ON inventory_reservations (order_id, variant_id)
    WHERE order_id IS NOT NULL AND released_at IS NULL;

DROP INDEX IF EXISTS idx_reservation_order_variant;
