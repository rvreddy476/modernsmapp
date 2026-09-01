-- Commerce P0 — migration 010: GST components and a real order state machine.
--
-- LB-20 (tax) and LB-10/§4.7 (auditable transitions).

-- ─── GST components on the line (LB-20 / v1 §5.8) ────────────────────
--
-- `tax_classes` has been seeded with the five slabs since day one, products
-- carry `hsn_code` and `tax_class_id`, and pricing set `totalTax := 0.0`.
-- Every order under-collected GST and every invoice was non-compliant.
--
-- Decision D1: catalogue prices are GST-INCLUSIVE, so tax is EXTRACTED from
-- the line, never added on top. The components are snapshotted per line
-- because a refund must reverse the tax that was actually charged, not
-- whatever the tax table says months later.

ALTER TABLE order_items
    ADD COLUMN IF NOT EXISTS tax_class_id UUID REFERENCES tax_classes(id),
    ADD COLUMN IF NOT EXISTS hsn_code     TEXT,
    -- Rate in basis points: 18% is 1800. Integral, so a 2.5% CGST half
    -- never introduces a fraction.
    ADD COLUMN IF NOT EXISTS tax_rate_bp  INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS taxable_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cgst_minor    BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sgst_minor    BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS igst_minor    BIGINT NOT NULL DEFAULT 0,
    -- This line's share of the order-level coupon and delivery charge.
    -- Stored so the allocation is auditable and a refund reproduces it
    -- rather than recomputing it (A4).
    ADD COLUMN IF NOT EXISTS allocated_discount_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS allocated_shipping_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS net_inclusive_minor      BIGINT NOT NULL DEFAULT 0;

ALTER TABLE orders
    -- Place of supply decides CGST+SGST versus IGST. Snapshotted, because
    -- the seller's registered state can change.
    ADD COLUMN IF NOT EXISTS place_of_supply_state TEXT,
    ADD COLUMN IF NOT EXISTS seller_state          TEXT,
    ADD COLUMN IF NOT EXISTS is_interstate         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS cgst_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sgst_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS igst_minor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS taxable_minor BIGINT NOT NULL DEFAULT 0;

-- The identity every golden vector and property test asserts:
-- taxable + tax == the amount charged. NOT VALID because historical rows
-- were written with tax_amount = 0 and cannot satisfy it.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'order_items'::regclass AND conname = 'chk_item_tax_reconciles'
    ) THEN
        ALTER TABLE order_items
            ADD CONSTRAINT chk_item_tax_reconciles
            CHECK (taxable_minor + cgst_minor + sgst_minor + igst_minor = net_inclusive_minor)
            NOT VALID;
    END IF;
END$$;

-- Exactly one split applies. A row carrying both would mean the interstate
-- determination ran twice with different answers.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'order_items'::regclass AND conname = 'chk_item_single_tax_split'
    ) THEN
        ALTER TABLE order_items
            ADD CONSTRAINT chk_item_single_tax_split
            CHECK ((cgst_minor + sgst_minor = 0) OR (igst_minor = 0))
            NOT VALID;
    END IF;
END$$;

-- ─── Order state machine (§4.7, D6) ──────────────────────────────────
--
-- `order_status_history` recorded transitions but nothing forbade an illegal
-- one, so a bug or a replayed event could move an order backwards — for
-- example out of `refunded` and back to `paid`.

CREATE TABLE IF NOT EXISTS order_status_transitions (
    from_status TEXT NOT NULL,
    to_status   TEXT NOT NULL,
    actor_type  TEXT NOT NULL CHECK (actor_type IN ('system','customer','seller','admin')),
    PRIMARY KEY (from_status, to_status, actor_type)
);

-- D6's cancellation matrix, expressed as data.
--
-- Note what is absent: no `shipped -> cancelled` for customer or seller.
-- Once it is with the courier, only an admin may intervene, and that path
-- is an audited exception rather than a self-service button.
INSERT INTO order_status_transitions (from_status, to_status, actor_type) VALUES
    ('payment_pending','confirmed','system'),
    ('payment_pending','payment_failed','system'),
    ('payment_pending','cancelled','customer'),
    ('payment_pending','cancelled','system'),
    ('payment_pending','cancelled','admin'),
    ('payment_pending','expired','system'),
    ('payment_failed','cancelled','customer'),
    ('payment_failed','cancelled','system'),
    ('payment_failed','payment_pending','customer'),
    ('confirmed','packed','seller'),
    ('confirmed','cancelled','customer'),
    ('confirmed','cancelled','seller'),
    ('confirmed','cancelled','admin'),
    ('packed','shipped','seller'),
    ('packed','cancelled','customer'),
    ('packed','cancelled','seller'),
    ('packed','cancelled','admin'),
    ('shipped','out_for_delivery','system'),
    ('shipped','delivered','system'),
    ('shipped','cancelled','admin'),
    ('out_for_delivery','delivered','system'),
    ('out_for_delivery','cancelled','admin'),
    ('delivered','refund_pending','admin'),
    ('cancelled','refund_pending','system'),
    ('cancelled','refund_pending','admin'),
    ('refund_pending','refunded','system'),
    ('expired','cancelled','system')
ON CONFLICT DO NOTHING;

-- `expired` and `payment_failed` are new terminal-ish states introduced by
-- LB-22; the CHECK on orders.status has to admit them.
DO $$
DECLARE r RECORD;
BEGIN
    FOR r IN
        SELECT conname FROM pg_constraint
         WHERE conrelid = 'orders'::regclass
           AND contype = 'c'
           AND pg_get_constraintdef(oid) LIKE '%status%'
           AND pg_get_constraintdef(oid) NOT LIKE '%expired%'
           AND pg_get_constraintdef(oid) LIKE '%payment_pending%'
    LOOP
        EXECUTE format('ALTER TABLE orders DROP CONSTRAINT %I', r.conname); -- expand-only: replaced immediately below by a strict SUPERSET that adds 'expired' and 'payment_failed'
    END LOOP;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'orders'::regclass AND conname = 'orders_status_check_v2'
    ) THEN
        ALTER TABLE orders
            ADD CONSTRAINT orders_status_check_v2 -- expand-only: strict superset of the constraint dropped above; every value the old image writes is still permitted
            CHECK (status IN (
                'created','payment_pending','payment_failed','expired','paid','confirmed',
                'packed','shipped','out_for_delivery','delivered','cancelled',
                'return_requested','return_approved','return_rejected','return_picked_up',
                'returned','refund_pending','refunded','awaiting_approval'
            ));
    END IF;
END$$;

-- The trigger. It refuses an illegal transition and writes the history row
-- in the SAME statement, so history cannot drift from state.
CREATE OR REPLACE FUNCTION enforce_order_transition() RETURNS TRIGGER AS $$
DECLARE
    actor TEXT;
BEGIN
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    actor := COALESCE(current_setting('commerce.actor_type', true), 'system');

    IF NOT EXISTS (
        SELECT 1 FROM order_status_transitions
         WHERE from_status = OLD.status
           AND to_status   = NEW.status
           AND actor_type  = actor
    ) THEN
        RAISE EXCEPTION
            'illegal order transition % -> % by %', OLD.status, NEW.status, actor
            USING ERRCODE = 'check_violation';
    END IF;

    INSERT INTO order_status_history (id, order_id, from_status, to_status, actor_type, created_at)
    VALUES (gen_random_uuid(), NEW.id, OLD.status, NEW.status, actor, NOW());

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- B11 — the TRIGGER ATTACHMENT moved to gated/998_contract_triggers_and_fences.sql.
--
-- The function above is defined here because defining a function attaches
-- nothing and rejects nothing, so it is expand-only. Attaching the trigger is
-- not: it refuses any status transition absent from
-- `order_status_transitions`, and during a rolling deploy an old replica
-- performing a legacy transition would have its UPDATE rejected mid-checkout
-- — a customer-facing failure caused by deploying rather than by either
-- image.
--
-- The attachment therefore runs behind the drained-fleet gate:
--
--	commerce-migrate -gated
--
-- The CHECK widening above stays here deliberately: adding 'expired' and
-- 'payment_failed' to the allowed set cannot reject an old writer, because
-- every value the old image produces is still permitted.
