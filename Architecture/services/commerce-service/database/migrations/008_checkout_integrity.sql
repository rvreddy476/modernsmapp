-- Commerce P0 — migration 008: the checkout transaction's storage.
--
-- Covers LB-14 through LB-18 and LB-22, plus A4's persisted quote. Expand
-- phase only: every constraint that could reject an old writer is created
-- NOT VALID and validated in 013 once the fleet is drained (review §5.1).

-- ─── Address snapshot (LB-18 / v1 §5.7) ──────────────────────────────
--
-- `delivery_address_snapshot` exists but Checkout wrote {"address_id":…}
-- into it — a POINTER, so editing a saved address silently rewrote the
-- delivery record of every past order, including delivered ones and their
-- GST invoices.
--
-- R-6: an old order's true delivery address CANNOT be manufactured. The
-- address row it pointed at may have been edited or deleted since, so
-- copying today's value in and calling it history is a fabrication. Legacy
-- rows are therefore marked, not invented.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS address_snapshot_provenance TEXT
        DEFAULT 'snapshot'
        CHECK (address_snapshot_provenance IN ('snapshot','legacy_unverified','invoice_backfill'));

-- Everything that predates this migration is unverifiable by definition.
UPDATE orders
   SET address_snapshot_provenance = 'legacy_unverified'
 WHERE address_snapshot_provenance IS NULL
    OR delivery_address_snapshot IS NULL
    OR NOT (delivery_address_snapshot ? 'address_line_1');

-- New rows must carry a real snapshot. Scoped to rows created after the
-- cutover so it cannot reject history (review §5.1, R-6).
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS snapshot_cutover BOOLEAN NOT NULL DEFAULT FALSE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'orders'::regclass AND conname = 'chk_order_address_snapshot_real'
    ) THEN
        ALTER TABLE orders
            ADD CONSTRAINT chk_order_address_snapshot_real
            CHECK (
                snapshot_cutover = FALSE
                OR (delivery_address_snapshot ? 'address_line_1'
                    AND delivery_address_snapshot ? 'postal_code'
                    AND delivery_address_snapshot ? 'contact_name')
            ) NOT VALID;
    END IF;
END$$;

-- ─── Cart versioning (LB-14, C9) ─────────────────────────────────────
--
-- Checkout must be able to prove the cart it priced is the cart it is
-- charging for. Without a version, a mutation racing checkout can slip a
-- line in between pricing and commit.

ALTER TABLE carts
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION bump_cart_version() RETURNS TRIGGER AS $$
BEGIN
    UPDATE carts
       SET version = version + 1, updated_at = NOW()
     WHERE id = COALESCE(NEW.cart_id, OLD.cart_id);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_cart_items_version ON cart_items;
CREATE TRIGGER trg_cart_items_version -- expand-only: AFTER trigger that bumps carts.version; it RETURNs NULL and raises nothing, so it cannot reject an old replica's write
    AFTER INSERT OR UPDATE OR DELETE ON cart_items
    FOR EACH ROW EXECUTE FUNCTION bump_cart_version();

-- A5: single-seller carts. Recorded on the cart so add-to-cart can reject a
-- second seller cheaply and checkout can re-assert it under lock.
ALTER TABLE carts
    ADD COLUMN IF NOT EXISTS seller_id UUID;

-- ─── Idempotency fingerprint (LB-15 / M-7) ───────────────────────────
--
-- The unique index on (customer_user_id, idempotency_key) stopped a double
-- order, but the retry path returned the EXISTING order without checking
-- that the request matched. A client that retried the same key after
-- changing address, cart or payment method silently received an order built
-- from the old request.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;

CREATE INDEX IF NOT EXISTS idx_orders_idem_fingerprint
    ON orders (customer_user_id, idempotency_key, request_fingerprint)
    WHERE idempotency_key IS NOT NULL;

-- ─── Terms / consent (D8-adjacent, v1 §4.5) ──────────────────────────

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS terms_version TEXT,
    ADD COLUMN IF NOT EXISTS consent_at    TIMESTAMPTZ;

-- ─── Persisted shipping quote (A4 / review R-4) ──────────────────────
--
-- LB-14 says no network call happens before the checkout commit; D7 says
-- Shiprocket owns the rate. Both can only be true if the quote is obtained
-- BEFORE the transaction and consumed inside it.
--
-- The quote is bound to everything that could change its validity. If any of
-- them moved, the quote is stale and checkout re-quotes instead of charging
-- yesterday's ₹70 for today's ₹170 delivery.

CREATE TABLE IF NOT EXISTS shipping_quotes (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    cart_id          UUID NOT NULL,
    cart_version     BIGINT NOT NULL,
    address_id       UUID NOT NULL,
    -- Hash of the address CONTENT, not just its id: editing an address in
    -- place must invalidate the quote even though the id is unchanged.
    address_hash     TEXT NOT NULL,
    seller_id        UUID NOT NULL,
    -- Hash of (variant_id, qty, weight) across the cart, ordered by
    -- variant_id, so a quantity change invalidates the quote too.
    items_hash       TEXT NOT NULL,
    total_weight_g   INT NOT NULL DEFAULT 0,
    destination_pin  TEXT NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    shipping_minor   BIGINT NOT NULL CHECK (shipping_minor >= 0),
    cod_available    BOOLEAN NOT NULL DEFAULT FALSE,
    courier_code     TEXT,
    provider_payload JSONB NOT NULL DEFAULT '{}',
    expires_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ,
    consumed_by_order UUID,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shipping_quotes_user ON shipping_quotes (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_shipping_quotes_live ON shipping_quotes (expires_at) WHERE consumed_at IS NULL;

-- ─── Payment linkage (LB-4, LB-5) ────────────────────────────────────

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS payment_intent_id UUID,
    ADD COLUMN IF NOT EXISTS payment_currency  TEXT NOT NULL DEFAULT 'INR';

CREATE INDEX IF NOT EXISTS idx_orders_payment_intent
    ON orders (payment_intent_id) WHERE payment_intent_id IS NOT NULL;

-- LB-4: one payment intent per order. Commerce authors the amount, so a
-- second intent for the same order is either a bug or an attempt to pay a
-- different number.
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_one_intent
    ON orders (payment_intent_id) WHERE payment_intent_id IS NOT NULL;

-- ─── Commerce-side payment inbox (A3 / review R-1) ───────────────────
--
-- The shared Kafka consumer marks a Redis dedupe key BEFORE invoking the
-- handler and commits the offset after. If commerce dies between the SETNX
-- and the DB commit, the restarted consumer sees the key, skips the event
-- and commits the offset — the captured payment is dropped permanently and
-- the customer's money is stuck until someone notices by hand.
--
-- Redis becomes advisory. THIS table is the authority, and it is written in
-- the same transaction as the order effect it authorises.

CREATE TABLE IF NOT EXISTS payment_event_inbox (
    event_id     TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    intent_id    TEXT,
    order_id     UUID,
    amount_minor BIGINT,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_inbox_event_id_not_blank CHECK (length(btrim(event_id)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_payment_inbox_order ON payment_event_inbox (order_id);
CREATE INDEX IF NOT EXISTS idx_payment_inbox_time  ON payment_event_inbox (processed_at DESC);

-- ─── Refund commands, commerce side (LB-8 / v1 §5.13) ────────────────
--
-- CancelOrder had three separate `slog.Warn` + `return nil` branches: no
-- payments client, no intent found, refund call failed. All three reported
-- success to the caller and left the order looking cancelled-and-refunded
-- while no money moved and nothing remembered the debt.

CREATE TABLE IF NOT EXISTS order_refund_commands (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL REFERENCES orders(id),
    intent_id     TEXT,
    amount_minor  BIGINT NOT NULL CHECK (amount_minor > 0),
    reason        TEXT NOT NULL,
    -- Deterministic, and unique, so a double-tapped cancel collapses to one
    -- refund rather than two.
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','submitted','succeeded','failed','needs_attention')),
    attempts        INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_order_refund_cmds_due
    ON order_refund_commands (next_attempt_at) WHERE status IN ('pending','submitted');
CREATE INDEX IF NOT EXISTS idx_order_refund_cmds_order
    ON order_refund_commands (order_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_order_refund_cmds_unsettled
    ON order_refund_commands (created_at) WHERE status <> 'succeeded';
