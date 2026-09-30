-- Migration 010: the orders preference category (MStore, lane C1, 2026-09-30).
--
-- commerce-service's order lifecycle now reaches people: the buyer hears
-- that an order is confirmed (on payment, not on placement), shipped,
-- delivered, cancelled, refunded or that its payment failed; the seller hears
-- about a new paid order. One pair of toggles covers all of them, split as in
-- 005: inapp_ gates the inbox row and realtime event, push_ the device push.
-- Default TRUE on both: an order update is something the person is waiting for.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_orders  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_orders BOOLEAN NOT NULL DEFAULT TRUE;
