-- Commerce P0 — the CONTRACT half of migrations 010 and 012.
--
-- B11. Everything in this file was previously applied at SERVICE BOOT, by
-- migrations 010 and 012, which made the ordinary 007–013 chain not
-- expand-only despite the CI job that claims to enforce exactly that. The CI
-- grep only banned `DROP COLUMN`/`DROP TABLE`/`VALIDATE CONSTRAINT`, so a
-- narrowing CHECK and a write-rejecting TRIGGER passed it untouched.
--
-- Why it matters, concretely. During a rolling deploy both images run at
-- once, against one database:
--
--   * `trg_order_transition` (from 010) rejects any status transition that is
--     not in `order_status_transitions`. An old replica performing a legacy
--     transition gets its UPDATE refused mid-checkout.
--   * `orders_payment_method_prepaid_only` (from 012) narrows the allowed
--     payment methods and DROPS 'cod'. An old replica writing a COD order —
--     which its code still does — gets its INSERT refused.
--   * the `trg_fence_*` triggers (from 012) refuse INSERTs outright on
--     twelve tables an old replica may still write to.
--
-- Each of those is a live customer-facing failure caused by deploying, not
-- by any defect in either image. So they move here, behind the same drained
-- fleet gate as 999.
--
-- WHAT STAYED IN THE BOOT SET, and why it is genuinely expand-only:
--
--   * `CREATE OR REPLACE FUNCTION` for enforce_order_transition(),
--     refuse_fenced_surface() and refuse_fenced_return(). Defining a function
--     attaches nothing and rejects nothing.
--   * the WIDENING of orders_status_check to admit 'expired' and
--     'payment_failed'. Widening a CHECK cannot reject an old writer: every
--     value the old image produces is still permitted.
--   * the `fenced_legacy_orders` quarantine table and the `fenced_surfaces`
--     inventory view, which are additive.
--
-- ORDERING. Run this BEFORE 999. This file installs the rejecting
-- constraints and triggers; 999 validates the NOT VALID constraints and
-- tightens the money columns. Running 999 first would validate a schema this
-- file has not yet tightened.
--
--   commerce-migrate -gated
--
-- PRECONDITION. The old fleet must be drained. This file asserts what it can
-- (below) and refuses loudly rather than half-applying; the drain itself is
-- not observable from inside PostgreSQL and remains a deploy-procedure
-- obligation, stated in the handover rather than pretended away here.

BEGIN;

-- ─── Precondition: nothing in flight that this would reject ──────────
--
-- A COD order still in a non-terminal state means an old replica is (or very
-- recently was) writing them. Applying the narrowed CHECK now would leave
-- that order unworkable.
DO $$
DECLARE n INT;
BEGIN
    SELECT count(*) INTO n
      FROM orders
     WHERE payment_method = 'cod'
       AND status NOT IN ('delivered','cancelled','refunded','returned','expired');
    IF n > 0 THEN
        RAISE EXCEPTION
            'refusing to contract: % live COD order(s) are still in flight. An old replica is still '
            'writing COD, so narrowing orders.payment_method now would refuse its writes. Drain the '
            'old fleet and settle these orders first.', n
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
END$$;

-- ─── 010's contract half: the transition trigger ─────────────────────

DROP TRIGGER IF EXISTS trg_order_transition ON orders;
CREATE TRIGGER trg_order_transition
    BEFORE UPDATE OF status ON orders
    FOR EACH ROW EXECUTE FUNCTION enforce_order_transition();

-- ─── 012's contract half: prepaid-only ───────────────────────────────

DO $$
DECLARE
    r         RECORD;
    offenders BIGINT;
BEGIN
    -- C3-LB-3 PRECONDITION.
    --
    -- This tightens the CHECK from (upi, card, net_banking) to (upi, card).
    -- Tightening a constraint over existing data fails loudly if any row
    -- violates it, which is correct but unhelpful at 3am, so the precondition
    -- is asserted FIRST, with a message that names the problem.
    --
    -- net_banking was never payable: commerce accepted it, reserved stock and
    -- committed the order, and payments-service then refused to open an intent
    -- for it. Any row carrying it is therefore an order that was ALREADY
    -- unpayable. It must be resolved (cancelled with its stock released, or
    -- migrated to a supported method) before this runs. This script will not
    -- decide which, because that is a money and customer decision.
    SELECT count(*) INTO offenders
      FROM orders
     WHERE payment_method IS NOT NULL
       AND payment_method NOT IN ('upi','card');
    IF offenders > 0 THEN
        RAISE EXCEPTION
            'C3-LB-3 precondition failed: % order(s) carry a payment_method outside (upi, card). '
            'These orders were never payable - payments-service has refused every method except '
            'upi and card since B6. Resolve them (cancel and release stock, or migrate to a '
            'supported method) before tightening this constraint.', offenders;
    END IF;

    FOR r IN
        SELECT conname FROM pg_constraint
         WHERE conrelid = 'orders'::regclass
           AND contype = 'c'
           AND pg_get_constraintdef(oid) LIKE '%payment_method%'
    LOOP
        EXECUTE format('ALTER TABLE orders DROP CONSTRAINT %I', r.conname);
    END LOOP;

    ALTER TABLE orders
        ADD CONSTRAINT orders_payment_method_prepaid_only
        CHECK (payment_method IS NULL OR payment_method IN ('upi','card'));
END$$;

COMMENT ON CONSTRAINT orders_payment_method_prepaid_only ON orders IS
    'Commerce P0 A5 / C3-LB-3: the launch vocabulary is exactly (upi, card), and it is defined once '
    'in Architecture/shared/paymentmethod. Re-enabling COD is a founder scope change that must also '
    'add eligibility, value cap, fraud controls, failed-delivery restock, cash remittance ownership '
    'and reconciliation — not merely a wider CHECK. B6 additionally removed ''wallet'' here: '
    'payments-service skipped provider order creation for it, producing an intent with a blank '
    'provider reference that can never be captured, refunded or reconciled.';

-- ─── 012's contract half: the fence triggers ─────────────────────────

DROP TRIGGER IF EXISTS trg_fence_returns ON return_requests;
CREATE TRIGGER trg_fence_returns
    BEFORE INSERT ON return_requests
    FOR EACH ROW EXECUTE FUNCTION refuse_fenced_return();

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'rfqs', 'rfq_items', 'rfq_quotes',
        'organizations', 'organization_members', 'organization_invites',
        'product_import_jobs',
        'payout_batches', 'payout_transactions',
        'product_price_tiers',
        'stock_alerts',
        'return_pickups'
    ] LOOP
        IF EXISTS (SELECT 1 FROM information_schema.tables
                    WHERE table_schema = 'public' AND table_name = t) THEN
            EXECUTE format('DROP TRIGGER IF EXISTS trg_fence_%I ON %I', t, t);
            EXECUTE format(
                'CREATE TRIGGER trg_fence_%I BEFORE INSERT ON %I FOR EACH ROW EXECUTE FUNCTION refuse_fenced_surface()',
                t, t);
        END IF;
    END LOOP;
END$$;

DROP TRIGGER IF EXISTS trg_fence_reviews ON reviews;
CREATE TRIGGER trg_fence_reviews
    BEFORE INSERT ON reviews
    FOR EACH ROW EXECUTE FUNCTION refuse_fenced_surface();

-- ─── Post-condition: the fences are actually installed ───────────────
--
-- A silent no-op here would leave the launch unfenced while reporting
-- success, which is the failure mode `fenced_surfaces` exists to make
-- visible. Assert it rather than trusting the loop.
DO $$
DECLARE n INT;
BEGIN
    SELECT count(*) INTO n FROM fenced_surfaces;
    IF n < 10 THEN
        RAISE EXCEPTION
            'contract migration installed only % fence trigger(s); expected at least 10. The fence '
            'list and the tables present in this database disagree.', n
            USING ERRCODE = 'object_not_in_prerequisite_state';
    END IF;
END$$;

COMMIT;
