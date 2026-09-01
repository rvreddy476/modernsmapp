-- 016 — the quote carries the whole price the buyer is asked to accept.
--
-- C3-LB-2. `shipping_quotes` held only a delivery charge, so the quote
-- endpoint could return only a delivery charge. The Android checkout screen
-- was therefore handed a shipping figure and nothing else, invented a total
-- from it (`subtotal 0 + shipping`), and submitted THAT as
-- `expected_total_minor`. Checkout recomputed the real total, disagreed, and
-- returned PRICE_CHANGED — every time, for every non-empty cart. The primary
-- paid journey could not complete.
--
-- The fix is not a client fix. A buyer accepts a TOTAL, and the only party
-- entitled to state it is the server that will charge it. So the quote now
-- stores the full breakdown it computed, and the client renders it verbatim.
--
-- EXPAND-ONLY, as the boot set requires:
--   * every column is nullable, so a running old binary that INSERTs without
--     them keeps working;
--   * nothing is dropped, renamed or tightened;
--   * no constraint is added without NOT VALID.
-- The contract half (making the breakdown mandatory once every writer fills
-- it in) belongs in gated/999, not here.

ALTER TABLE shipping_quotes
    ADD COLUMN IF NOT EXISTS subtotal_minor  BIGINT,
    ADD COLUMN IF NOT EXISTS discount_minor  BIGINT,
    ADD COLUMN IF NOT EXISTS tax_minor       BIGINT,
    ADD COLUMN IF NOT EXISTS total_minor     BIGINT,
    -- The bindings. A quote is only meaningful for the exact request that
    -- produced it: a different coupon or a different payment method is a
    -- different price, and reusing the quote across them is how a buyer gets
    -- charged for a discount they no longer have.
    ADD COLUMN IF NOT EXISTS coupon_code     TEXT,
    ADD COLUMN IF NOT EXISTS payment_method  TEXT;

COMMENT ON COLUMN shipping_quotes.total_minor IS
    'C3-LB-2: the complete GST-inclusive total this quote represents, in paise. '
    'This is the number the buyer is shown and the number they send back as '
    'expected_total_minor. Checkout still recomputes under its own transaction and '
    'returns PRICE_CHANGED on any disagreement — the stored value is what was '
    'PROMISED, never what is charged.';

COMMENT ON COLUMN shipping_quotes.tax_minor IS
    'GST already contained within total_minor (D1: catalogue prices are '
    'GST-inclusive). It is extracted for display, never added on top. A client that '
    'adds it to the total charges the buyer tax twice.';

COMMENT ON COLUMN shipping_quotes.coupon_code IS
    'C3-LB-2: the coupon this price assumed. Checkout refuses a quote whose coupon '
    'differs from the one being redeemed. NULL means the quote assumed no coupon.';

COMMENT ON COLUMN shipping_quotes.payment_method IS
    'C3-LB-2: the payment method this quote was taken for, from the launch '
    'vocabulary in Architecture/shared/paymentmethod. Bound because a future method '
    'with its own surcharge must not be able to reuse a price quoted for another.';
