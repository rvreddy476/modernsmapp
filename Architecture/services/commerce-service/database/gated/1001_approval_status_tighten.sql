-- Commerce P0 — GATED: contract the product approval vocabulary.
--
-- THIS FILE IS NOT APPLIED AT BOOT. It lives in database/gated/, which the
-- embedded migration set deliberately excludes, and is run by the one-shot
-- job:
--
--     commerce-migrate -gated
--
-- ─── WHAT IT FINISHES ───────────────────────────────────────────────────
--
-- `ApproveProductByAdmin` wrote approval_status='live' while both sale gates
-- require 'approved', so every product the moderation queue approved was
-- visible in the catalogue and refused at add-to-cart. Boot migration 022
-- converts the existing rows — the expand half, safe under a rolling deploy.
--
-- This is the contract half: it removes 'live' from the allowed set so the
-- two spellings cannot drift apart again. It is gated because narrowing a
-- CHECK rejects writes from any pod still running the old image, and during
-- a rollout those pods are still approving products the old way. Run it once
-- the fleet is fully on the new image.
--
-- Running it early does not corrupt anything: the precondition below fails
-- and the transaction rolls back, installing nothing.
--
-- ─── WHY THE PRECONDITION COUNTS ROWS RATHER THAN TRUSTING 022 ──────────
--
-- 022 having run is not sufficient. An old pod can approve a product AFTER
-- 022 converted the backlog, so a fresh 'live' row can appear between the two
-- files. That row is the signal that the old image is still serving, which is
-- exactly when this file must refuse.
--
-- It reports the count and does not convert them itself. Converting here
-- would hide the fact that the old image is still writing, and the rollout
-- would look complete while it was not.

BEGIN;

DO $$
DECLARE
    stragglers BIGINT;
BEGIN
    SELECT COUNT(*) INTO stragglers
      FROM products
     WHERE approval_status = 'live';

    IF stragglers > 0 THEN
        RAISE EXCEPTION
            'approval_status precondition failed: % product(s) still carry ''live''. '
            'Boot migration 022 converts the backlog, so rows remaining here were written '
            'by a pod still running the old ApproveProductByAdmin. Complete the rollout, '
            're-run 022, then re-run this file.', stragglers;
    END IF;
END $$;

-- expand-only: not a widening, and deliberately so — this is the gated
-- contract half. The precondition above proves no row violates the narrower
-- set before it is installed.
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_approval_status_check;
ALTER TABLE products ADD CONSTRAINT products_approval_status_check
    CHECK (approval_status IN (
        'draft','submitted','under_review','pending','approved','rejected',
        'flagged','changes_requested','hidden','archived'
    ));

COMMENT ON CONSTRAINT products_approval_status_check ON products IS
    'Review outcome. ''approved'' is the ONLY sale-eligible value and is what both the '
    'add-to-cart and the locked checkout gate require; ''live'' was a second spelling that '
    'browse accepted and the sale path refused, so approved products were visible and unbuyable.';

COMMIT;
