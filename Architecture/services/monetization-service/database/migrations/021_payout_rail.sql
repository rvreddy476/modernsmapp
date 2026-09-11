-- 021: the payout rail (plan Phase 4A, M-03).
--
-- Until this migration payout_requests carried eleven overlapping status
-- values from three generations of design, and nothing moved a row
-- between them except a worker that marked it paid after a two-second
-- sleep. This collapses the vocabulary to one state machine:
--
--   requested -> reserved -> submitted -> processing -> paid | failed | reversed
--   plus held (review), reachable from requested and reserved.
--
-- The machine is enforced in exactly one place in code
-- (store.TransitionPayoutRequest: UPDATE ... WHERE id=$1 AND status=$2,
-- zero rows is an illegal transition) and here by the CHECK, so an old
-- value cannot come back through any path.

-- ---------------------------------------------------------------------------
-- payout_requests: what the rail records about a request
-- ---------------------------------------------------------------------------
--
--   provider_status     RazorpayX's own word for where the payout is
--                       (queued, pending, processing, processed, ...)
--   submitted_at        when CreatePayout returned, or when an existing
--                       payout was adopted by reference after a timeout
--   last_reconciled_at  when the reconciler or a webhook last converged
--                       this row against the provider
--   utr                 the bank's Unique Transaction Reference, captured
--                       on processed; the creator's proof of payment
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS provider_status TEXT;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS submitted_at TIMESTAMPTZ;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS last_reconciled_at TIMESTAMPTZ;
ALTER TABLE payout_requests ADD COLUMN IF NOT EXISTS utr TEXT;

-- Data migration: every old value has one home in the new machine.
--   pending, kyc_check  -> requested   (recorded, not yet allowed to go)
--   approved, batched   -> reserved    (allowed, not yet at the provider)
--   in_flight           -> submitted   (at the provider)
--   settled             -> paid
--   returned            -> reversed
--   processing, paid, failed, held, requested keep their names.
UPDATE payout_requests SET status = CASE status
    WHEN 'pending'   THEN 'requested'
    WHEN 'kyc_check' THEN 'requested'
    WHEN 'approved'  THEN 'reserved'
    WHEN 'batched'   THEN 'reserved'
    WHEN 'in_flight' THEN 'submitted'
    WHEN 'settled'   THEN 'paid'
    WHEN 'returned'  THEN 'reversed'
    ELSE status END
WHERE status IN ('pending', 'kyc_check', 'approved', 'batched', 'in_flight', 'settled', 'returned');

ALTER TABLE payout_requests DROP CONSTRAINT IF EXISTS payout_requests_status_check;
ALTER TABLE payout_requests ADD CONSTRAINT payout_requests_status_check
    CHECK (status IN ('requested', 'reserved', 'submitted', 'processing', 'paid', 'failed', 'reversed', 'held'));

-- One provider payout belongs to at most one request. Partial: rows that
-- have not reached the provider carry NULL and do not collide.
CREATE UNIQUE INDEX IF NOT EXISTS uq_payout_requests_provider_reference
    ON payout_requests (provider_reference)
    WHERE provider_reference IS NOT NULL;

-- The submitter reads requested (past the review window) and reserved;
-- the reconciler reads submitted and processing. Migration 004's partial
-- index on status = 'pending' indexes nothing now.
DROP INDEX IF EXISTS idx_payout_requests_pending;
CREATE INDEX IF NOT EXISTS idx_payout_requests_submitter
    ON payout_requests (status, requested_at)
    WHERE status IN ('requested', 'reserved');
CREATE INDEX IF NOT EXISTS idx_payout_requests_reconciler
    ON payout_requests (status, last_reconciled_at, submitted_at)
    WHERE status IN ('submitted', 'processing');

-- ---------------------------------------------------------------------------
-- payout_provider_events: every webhook delivery, exactly once
-- ---------------------------------------------------------------------------
--
-- (provider, event_id) is the primary key: a redelivery is an INSERT ...
-- ON CONFLICT DO NOTHING that affects zero rows, and the handler stops
-- there. consumed_at is set ONLY when a payout_requests row was actually
-- updated by the event; an event for a payout we could not match (the
-- provider reference not yet persisted) stays unconsumed and visible.
CREATE TABLE IF NOT EXISTS payout_provider_events (
    provider           TEXT NOT NULL,
    event_id           TEXT NOT NULL,
    event_type         TEXT NOT NULL DEFAULT '',
    provider_reference TEXT,
    payload            JSONB,
    received_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at        TIMESTAMPTZ,
    payout_request_id  UUID,
    PRIMARY KEY (provider, event_id)
);
CREATE INDEX IF NOT EXISTS idx_payout_provider_events_unconsumed
    ON payout_provider_events (provider, provider_reference)
    WHERE consumed_at IS NULL;

-- ---------------------------------------------------------------------------
-- creator_payout_accounts: the creator's RazorpayX contact
-- ---------------------------------------------------------------------------
--
-- One contact per creator, looked up by our user id as the provider's
-- reference_id and cached here so the submitter does not round-trip for
-- it on every payout.
CREATE TABLE IF NOT EXISTS creator_payout_accounts (
    user_id        UUID PRIMARY KEY,
    rzp_contact_id TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- payout_methods: a bank account the rail can pay
-- ---------------------------------------------------------------------------
--
-- is_default is the column the store has selected and ordered by since
-- the payout-methods routes were written, and that no migration ever
-- added: GET /payout-methods answered 500 on every call. Wave 3 found it
-- and left it for this migration.
ALTER TABLE payout_methods ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false;

-- Bank capture (plan Phase 4D). The last four digits, the IFSC and the
-- holder name are stored in the clear for display and for the fund
-- account; the full account number goes into the existing
-- details_encrypted column, encrypted. verified_at is set when RazorpayX's
-- fund-account validation (a penny drop) reports the account active.
ALTER TABLE payout_methods ADD COLUMN IF NOT EXISTS rzp_fund_account_id TEXT;
ALTER TABLE payout_methods ADD COLUMN IF NOT EXISTS ifsc TEXT;
ALTER TABLE payout_methods ADD COLUMN IF NOT EXISTS account_last4 TEXT;
ALTER TABLE payout_methods ADD COLUMN IF NOT EXISTS holder_name TEXT;
ALTER TABLE payout_methods ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ;

ALTER TABLE payout_methods DROP CONSTRAINT IF EXISTS payout_methods_method_type_check;
ALTER TABLE payout_methods ADD CONSTRAINT payout_methods_method_type_check
    CHECK (method_type IN ('upi', 'bank_transfer', 'bank_account', 'paypal'));
