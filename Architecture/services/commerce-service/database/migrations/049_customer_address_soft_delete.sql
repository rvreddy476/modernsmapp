-- 049 — a customer can delete a saved address that past orders reference.
--
-- Audit 2026-10-30 L5. orders.delivery_address_id (and billing_address_id,
-- return pickups, organisations) REFERENCES customer_addresses(id) with no
-- ON DELETE clause, so DeleteAddress failed with a raw foreign-key error for
-- any address ever used on an order, and the buyer could never remove it.
--
-- DeleteAddress now removes an unreferenced row as before, and HIDES a
-- referenced one: deleted_at is set and every address-book read, the default
-- pincode, update, set-default and checkout skip it. The row itself stays,
-- because orders keep their sealed delivery-address snapshot (M5) and an
-- order old enough to have none still falls back to this row.
--
-- Expand-only: a new nullable column with no default; an older image neither
-- reads nor writes it.

ALTER TABLE customer_addresses ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_cust_addresses_user_live
    ON customer_addresses (user_id) WHERE deleted_at IS NULL;
