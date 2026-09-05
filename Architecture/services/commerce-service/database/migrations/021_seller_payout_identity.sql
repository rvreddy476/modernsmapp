-- 021 — one primary payout account per seller.
--
-- `SaveOnboardingPayout` has always written:
--
--     INSERT INTO seller_payout_accounts (...)
--     ON CONFLICT (seller_id) WHERE is_primary=TRUE DO UPDATE SET ...
--
-- and no index has ever matched that specification. PostgreSQL rejects the
-- statement outright:
--
--     ERROR:  there is no unique or exclusion constraint matching the
--             ON CONFLICT specification
--
-- So step 7 of seller onboarding — where a seller supplies the bank account
-- they are paid into — failed on every call, for every seller, since the
-- statement was written. Nothing caught it because no test exercised the
-- payout step and no client had ever called it.
--
-- ─── WHY IT MATTERS BEYOND THE ERROR ────────────────────────────────────
--
-- A seller with no payout account can still be submitted for review and
-- approved, because `SubmitSellerApplication` checked nothing. An approved
-- shop with no bank details can take money and has no settlement path — an
-- obligation with no way to discharge it. The readiness check added alongside
-- this migration refuses that submission; this index is what makes it
-- possible to satisfy.
--
-- ─── WHY PARTIAL ────────────────────────────────────────────────────────
--
-- A seller may legitimately hold several payout accounts — an old one kept for
-- reconciliation, a new one being switched to. Exactly one may be PRIMARY, and
-- that is the one money goes to. A plain unique index on seller_id would
-- forbid the history; this forbids only a second primary.
--
-- ─── EXPAND-ONLY ────────────────────────────────────────────────────────
--
-- CREATE UNIQUE INDEX, not ADD CONSTRAINT: no table rewrite and no validation
-- scan. IF NOT EXISTS keeps it idempotent.
--
-- It cannot fail on existing data. The only writer is the statement above,
-- which has never once succeeded, so no environment holds a row it could
-- conflict with. If a future environment somehow does, the index build fails
-- loudly on that duplicate rather than letting two accounts both claim to be
-- where the money goes — which is the correct outcome, and not something to
-- resolve by picking one.

CREATE UNIQUE INDEX IF NOT EXISTS uq_seller_payout_primary
    ON seller_payout_accounts (seller_id)
    WHERE is_primary;

COMMENT ON INDEX uq_seller_payout_primary IS
    'One primary payout account per seller. SaveOnboardingPayout upserts on '
    '(seller_id) WHERE is_primary, which had no matching index — so the payout '
    'step of onboarding failed on every call since it was written.';
