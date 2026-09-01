-- 019 — one seller address per type.
--
-- A seller has one pickup point at a time. Without a uniqueness rule, two
-- 'pickup' rows can exist and `SellerPickupPin`'s ORDER BY becomes the thing
-- that silently decides where a courier collects from:
--
--     ORDER BY (address_type = 'pickup') DESC, is_default DESC
--     LIMIT 1
--
-- That is a tie-break, not a decision. Shipping from the wrong origin gets the
-- rate wrong and sends a courier to an address the seller does not occupy.
--
-- The index is also what makes the write path an UPSERT rather than an
-- insert-then-hope: `UpsertSellerAddress` uses ON CONFLICT (seller_id,
-- address_type), so editing a pickup address replaces it instead of
-- accumulating a second one.
--
-- ─── WHY THIS IS SAFE IN THE BOOT SET ───────────────────────────────────
--
-- No production code has ever written this table — `POST /sellers/onboard`
-- writes only the flat state/city/postal_code columns on `sellers`, and the
-- onboarding wizard leaves pickup_address_id NULL. So the table is empty in
-- every environment and the unique index cannot fail on existing data.
--
-- It is a CREATE INDEX rather than an ADD CONSTRAINT so no table rewrite or
-- validation scan is involved, and IF NOT EXISTS keeps it idempotent.

CREATE UNIQUE INDEX IF NOT EXISTS uq_seller_addresses_type
    ON seller_addresses (seller_id, address_type);

COMMENT ON INDEX uq_seller_addresses_type IS
    'One address per (seller, type). SellerPickupPin picks the origin of every shipment with an '
    'ORDER BY; without this, a duplicate pickup row would let that tie-break decide where a '
    'courier collects from.';
