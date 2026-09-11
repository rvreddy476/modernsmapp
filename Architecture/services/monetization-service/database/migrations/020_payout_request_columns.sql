-- 020: what a withdrawal request records (plan Phase 3A).
--
-- Until this phase nothing in the service wrote to payout_requests. The
-- live path (Service.RequestPayout) now runs every gate — minimum, KYC,
-- payout method, ledger lock, new-account hold, velocity, TDS — inside
-- one transaction and ends with the first ever INSERT into this table.
-- Three columns carry what that insert knows and the old shape did not:
--
--   tds_paise        what was withheld under section 194-O (0 below the
--                    yearly gross threshold; the tds_ledger row is keyed
--                    to this request by reference_id)
--   net_paise        amount - tds_paise; NULL on a held request, which
--                    has not been priced because it has not been allowed
--   idempotency_key  'payout:<user>:<client key>' when the caller sent
--                    X-Idempotency-Key, so a retried request lands once;
--                    NULL otherwise. Partial unique index: rows without a
--                    key do not collide.
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS tds_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS net_paise BIGINT;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uq_payout_requests_idempotency_key
    ON payout_requests (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- A HELD request (new-account hold, velocity) is recorded before any
-- money moves: no transactions row exists for it yet, so transaction_id
-- has to be nullable. A 'requested' row always carries one.
ALTER TABLE payout_requests ALTER COLUMN transaction_id DROP NOT NULL;

-- 'requested' is the state a withdrawal starts in once it has passed the
-- gates; migration 008's CHECK predates it. The rest of the list is kept
-- exactly as it was: the collapse to the Phase 4 state machine is that
-- phase's migration (021), with its data migration, not this one.
ALTER TABLE payout_requests DROP CONSTRAINT IF EXISTS payout_requests_status_check;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('requested','pending','kyc_check','approved','batched','in_flight','settled','failed','returned','held','processing','paid'));

-- The velocity gate counts a creator's requests in the last 24 hours.
CREATE INDEX IF NOT EXISTS idx_payout_requests_user_requested
    ON payout_requests (user_id, requested_at DESC);
