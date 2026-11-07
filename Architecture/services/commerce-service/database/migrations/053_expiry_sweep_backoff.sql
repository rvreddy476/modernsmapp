-- 053 — the reservation-expiry sweep makes progress past an order it cannot
-- expire.
--
-- ExpireStaleOrders read up to LIMIT candidates with no ORDER BY and skipped
-- an order whose expiry failed with `continue`. An order that fails every
-- time (a broken row, a trigger refusing it) was picked again on the next
-- tick, so a LIMIT's worth of them starved every other lapsed hold: their
-- stock stayed reserved and their coupons stayed claimed.
--
-- The sweep now takes candidates oldest expiry first and, when one fails,
-- pushes it back:
--
--   expiry_attempts         failed expiry attempts on this order
--   expiry_next_attempt_at  not a sweep candidate before this time (NULL =
--                           now); bumped by a growing back-off on each failure
--
-- Expand-only: both columns are nullable or carry a constant default, so ADD
-- COLUMN is catalog-only (no table rewrite). The ALTER runs only when a column
-- is missing, with lock_timeout 5s, so an ALTER queued behind a long
-- transaction fails the boot (the pod retries, old pods keep serving) instead
-- of stalling every order write behind it. Old pods never read the columns.
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF to_regclass('orders') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('orders')
          AND attname = 'expiry_next_attempt_at' AND NOT attisdropped
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE orders
            ADD COLUMN IF NOT EXISTS expiry_attempts        INT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS expiry_next_attempt_at TIMESTAMPTZ;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;
