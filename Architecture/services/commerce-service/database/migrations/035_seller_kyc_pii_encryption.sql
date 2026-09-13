-- 035 — seller KYC identifiers become ciphertext; raw Aadhaar is refused.
--
-- THE FINDING. Three identity values sat in plaintext columns:
--
--   seller_payout_accounts.account_number   the FULL bank account number — the
--                                           value a fraudster needs to redirect
--                                           a seller's settlement
--   sellers.pan_number                      returned in full on GET /sellers/me
--                                           and the admin seller view
--   organizations.pan                       same, for B2B buyers
--
-- and `seller_documents.document_number` would store a raw 12-digit Aadhaar
-- number, which commerce is not permitted to hold at all.
--
-- THE SHAPE. Exactly the address cutover (011 / 015 / 017 / gated 1000), under
-- its own key scope (`kyc`) and its OWN cutover: COMMERCE_KYC_PII_CUTOVER,
-- `pii_kyc_backfill_progress`, `pii_kyc_cutover_state`, gated 1002. Separate so
-- the address cutover is never blocked on this one, and either can be rolled
-- back alone.
--
--   1. this migration + the dual-write image   (COMMERCE_KYC_PII_CUTOVER=dual)
--   2. piibackfill -set=kyc to completion, every row verified
--   3. the ciphertext-authoritative image      (COMMERCE_KYC_PII_CUTOVER=ciphertext)
--   4. drain old writers, stamp pii_kyc_cutover_state
--   5. backup, then gated/1002 clears the plaintext
--
-- What stays plaintext, deliberately: IFSC, account holder name, bank name,
-- the last four digits, and the masked PAN — what a seller needs to recognise
-- their own account, and what identifies nobody on its own. GSTIN stays too:
-- it is public by law and printed on every invoice. (It also CONTAINS the PAN,
-- characters 3-12, so for a GST-registered seller the PAN is not secret. That
-- is a property of the GSTIN, recorded here so nobody mistakes this migration
-- for having hidden it.)
--
-- EXPAND-ONLY, with one deliberate exception called out at the Aadhaar trigger.

-- ─── Ciphertext, display and lookup columns ──────────────────────────

ALTER TABLE seller_payout_accounts
    ADD COLUMN IF NOT EXISTS account_number_enc   BYTEA,
    ADD COLUMN IF NOT EXISTS account_number_last4 TEXT,
    -- Salted HMAC of (IFSC, number). Written NOW because after the scrub it
    -- could only be computed by decrypting every account — and "is this bank
    -- account already paying out to another seller?" is a fraud question
    -- someone will want answered.
    ADD COLUMN IF NOT EXISTS account_number_hash  TEXT,
    ADD COLUMN IF NOT EXISTS pii_key_version      INT;

CREATE INDEX IF NOT EXISTS idx_payout_accounts_number_hash
    ON seller_payout_accounts (account_number_hash) WHERE account_number_hash IS NOT NULL;

ALTER TABLE sellers
    ADD COLUMN IF NOT EXISTS pan_enc         BYTEA,
    ADD COLUMN IF NOT EXISTS pan_masked      TEXT,
    ADD COLUMN IF NOT EXISTS pan_hash        TEXT,
    ADD COLUMN IF NOT EXISTS pan_key_version INT;

CREATE INDEX IF NOT EXISTS idx_sellers_pan_hash
    ON sellers (pan_hash) WHERE pan_hash IS NOT NULL;

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS pan_enc         BYTEA,
    ADD COLUMN IF NOT EXISTS pan_masked      TEXT,
    ADD COLUMN IF NOT EXISTS pan_hash        TEXT,
    ADD COLUMN IF NOT EXISTS pan_key_version INT;

CREATE INDEX IF NOT EXISTS idx_organizations_pan_hash
    ON organizations (pan_hash) WHERE pan_hash IS NOT NULL;

-- ─── Backfill progress and cutover state (the KYC cutover's own) ─────
--
-- Same columns as pii_backfill_progress after 017, in a separate table: the
-- address scrub (gated 1000) and the tighten (gated 999) both refuse while ANY
-- row of pii_backfill_progress is incomplete, so KYC rows there would hold the
-- address cutover hostage to this one.

CREATE TABLE IF NOT EXISTS pii_kyc_backfill_progress (
    table_name      TEXT PRIMARY KEY,
    total_rows      BIGINT NOT NULL DEFAULT 0,
    encrypted_rows  BIGINT NOT NULL DEFAULT 0,
    verified        BIGINT NOT NULL DEFAULT 0,
    failed          BIGINT NOT NULL DEFAULT 0,
    last_id         UUID,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    last_error_id   UUID,
    last_error_at   TIMESTAMPTZ,
    last_error_kind TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO pii_kyc_backfill_progress (table_name) VALUES
    ('seller_payout_accounts'), ('sellers'), ('organizations')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS pii_kyc_cutover_state (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    ciphertext_authoritative_since TIMESTAMPTZ,
    old_writers_drained_at         TIMESTAMPTZ,
    scrubbed_at                    TIMESTAMPTZ,
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO pii_kyc_cutover_state (id) VALUES (TRUE) ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE pii_kyc_cutover_state IS
    '035: the two operator assertions gated/1002 requires before clearing seller KYC plaintext — '
    'the ciphertext-authoritative image (COMMERCE_KYC_PII_CUTOVER=ciphertext) is live, and every '
    'old writer is drained. Separate from pii_cutover_state so the address and KYC cutovers move independently.';

INSERT INTO pii_retention_policy (scope, retain_days, shred_enabled, approval_status, notes) VALUES
    ('kyc', NULL, FALSE, 'pending_legal_review',
     'Seller payout accounts and seller/organization PANs. Tax and settlement-dispute retention; '
     'shred stays DISABLED until legal rules.')
ON CONFLICT DO NOTHING;

-- ─── Stale ciphertext: an old writer must not leave the wrong account sealed ──
--
-- During the dual-write window an OLD image is still running. Its payout
-- upsert does `SET account_number = EXCLUDED.account_number` and knows nothing
-- about `account_number_enc` — so a seller who changes bank account through a
-- not-yet-replaced pod gets NEW plaintext beside the OLD ciphertext. After the
-- cutover the service reads the ciphertext, and the seller is paid into the
-- account they left.
--
-- So: when plaintext changes to a non-blank value and the ciphertext did NOT
-- change with it, the ciphertext is stale — clear it, and the backfill
-- re-seals the row from the plaintext that is still there. A write from the
-- new image always changes `_enc` (every seal draws a fresh nonce), so it is
-- never touched. The scrub blanks plaintext, which is excluded by the
-- non-blank condition, so the scrub can never clear ciphertext.

CREATE OR REPLACE FUNCTION commerce_payout_account_stale_ciphertext() RETURNS trigger
LANGUAGE plpgsql AS $fn$
BEGIN
    IF btrim(COALESCE(NEW.account_number, '')) = '' THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.account_number_enc IS NULL THEN
            NEW.account_number_last4 := right(btrim(NEW.account_number), 4);
        END IF;
        RETURN NEW;
    END IF;
    IF (NEW.account_number IS DISTINCT FROM OLD.account_number
        OR NEW.ifsc_code IS DISTINCT FROM OLD.ifsc_code)
       AND NEW.account_number_enc IS NOT DISTINCT FROM OLD.account_number_enc THEN
        NEW.account_number_enc   := NULL;
        NEW.pii_key_version      := NULL;
        NEW.account_number_hash  := NULL;
        NEW.account_number_last4 := right(btrim(NEW.account_number), 4);
    END IF;
    RETURN NEW;
END
$fn$;

DROP TRIGGER IF EXISTS trg_payout_account_stale_ciphertext ON seller_payout_accounts; -- expand-only: recreated immediately below, so re-running the migration is idempotent
CREATE TRIGGER trg_payout_account_stale_ciphertext
    BEFORE INSERT OR UPDATE OF account_number, ifsc_code, account_number_enc ON seller_payout_accounts
    FOR EACH ROW EXECUTE FUNCTION commerce_payout_account_stale_ciphertext();

CREATE OR REPLACE FUNCTION commerce_mask_pan(v TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $fn$
    SELECT CASE
        WHEN v IS NULL OR btrim(v) = '' THEN NULL
        WHEN length(btrim(v)) <= 4 THEN repeat('X', length(btrim(v)))
        ELSE repeat('X', length(btrim(v)) - 4) || right(upper(btrim(v)), 4)
    END
$fn$;

CREATE OR REPLACE FUNCTION commerce_seller_pan_stale_ciphertext() RETURNS trigger
LANGUAGE plpgsql AS $fn$
BEGIN
    IF btrim(COALESCE(NEW.pan_number, '')) = '' THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.pan_enc IS NULL THEN
            NEW.pan_masked := commerce_mask_pan(NEW.pan_number);
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.pan_number IS DISTINCT FROM OLD.pan_number
       AND NEW.pan_enc IS NOT DISTINCT FROM OLD.pan_enc THEN
        NEW.pan_enc         := NULL;
        NEW.pan_key_version := NULL;
        NEW.pan_hash        := NULL;
        NEW.pan_masked      := commerce_mask_pan(NEW.pan_number);
    END IF;
    RETURN NEW;
END
$fn$;

DROP TRIGGER IF EXISTS trg_seller_pan_stale_ciphertext ON sellers; -- expand-only: recreated immediately below, so re-running the migration is idempotent
CREATE TRIGGER trg_seller_pan_stale_ciphertext
    BEFORE INSERT OR UPDATE OF pan_number, pan_enc ON sellers
    FOR EACH ROW EXECUTE FUNCTION commerce_seller_pan_stale_ciphertext();

CREATE OR REPLACE FUNCTION commerce_organization_pan_stale_ciphertext() RETURNS trigger
LANGUAGE plpgsql AS $fn$
BEGIN
    IF btrim(COALESCE(NEW.pan, '')) = '' THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.pan_enc IS NULL THEN
            NEW.pan_masked := commerce_mask_pan(NEW.pan);
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.pan IS DISTINCT FROM OLD.pan
       AND NEW.pan_enc IS NOT DISTINCT FROM OLD.pan_enc THEN
        NEW.pan_enc         := NULL;
        NEW.pan_key_version := NULL;
        NEW.pan_hash        := NULL;
        NEW.pan_masked      := commerce_mask_pan(NEW.pan);
    END IF;
    RETURN NEW;
END
$fn$;

DROP TRIGGER IF EXISTS trg_organization_pan_stale_ciphertext ON organizations; -- expand-only: recreated immediately below, so re-running the migration is idempotent
CREATE TRIGGER trg_organization_pan_stale_ciphertext
    BEFORE INSERT OR UPDATE OF pan, pan_enc ON organizations
    FOR EACH ROW EXECUTE FUNCTION commerce_organization_pan_stale_ciphertext();

-- ─── Aadhaar: a reference, never a number ────────────────────────────
--
-- A seller KYC document of type `aadhaar` is stored as its uploaded media
-- reference ONLY: `document_number` must be empty. And no document of any
-- other type may carry a value shaped like an Aadhaar number (12 digits, first
-- 2-9, valid Verhoeff check digit), because a seller typing their Aadhaar into
-- the "other document" box is how one realistically arrives. The one
-- exemption is `cancelled_cheque`, whose number is a bank account and would
-- otherwise be refused one time in ten.
--
-- The service refuses these with a 400 before they reach Postgres
-- (kyc.LooksLikeAadhaar). This trigger is the floor under it, for every other
-- writer — including an OLD image during rollout.
--
-- THE EXCEPTION TO EXPAND-ONLY. This refuses writes an old replica would have
-- made. That is the point: the only writes it refuses are raw Aadhaar numbers,
-- which no writer is permitted to store, so an old image returning an error
-- instead of persisting one is the correct outcome. Existing rows are not
-- touched here (no environment holds any); gated/1002 clears any that exist.
--
-- `commerce_looks_like_aadhaar` must agree with kyc.LooksLikeAadhaar; the
-- integration suite checks them against each other.

CREATE OR REPLACE FUNCTION commerce_looks_like_aadhaar(v TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE AS $fn$
DECLARE
    -- Verhoeff D5 multiplication table, row-major, 10x10.
    d INT[] := ARRAY[
        0,1,2,3,4,5,6,7,8,9,
        1,2,3,4,0,6,7,8,9,5,
        2,3,4,0,1,7,8,9,5,6,
        3,4,0,1,2,8,9,5,6,7,
        4,0,1,2,3,9,5,6,7,8,
        5,9,8,7,6,0,4,3,2,1,
        6,5,9,8,7,1,0,4,3,2,
        7,6,5,9,8,2,1,0,4,3,
        8,7,6,5,9,3,2,1,0,4,
        9,8,7,6,5,4,3,2,1,0];
    -- Verhoeff position permutation, row-major, 8x10.
    p INT[] := ARRAY[
        0,1,2,3,4,5,6,7,8,9,
        1,5,7,6,2,8,3,0,9,4,
        5,8,0,3,7,9,6,1,4,2,
        8,9,1,6,0,4,3,5,2,7,
        9,4,5,3,1,2,6,8,7,0,
        4,2,8,6,5,7,3,9,0,1,
        2,7,9,3,8,0,6,4,1,5,
        7,0,4,6,9,1,3,2,5,8];
    s TEXT;
    c INT := 0;
    digit INT;
BEGIN
    IF v IS NULL THEN
        RETURN FALSE;
    END IF;
    s := translate(v, ' -', '');
    IF s !~ '^[2-9][0-9]{11}$' THEN
        RETURN FALSE;
    END IF;
    FOR i IN 0..11 LOOP
        digit := substr(s, 12 - i, 1)::INT;
        c := d[c * 10 + p[(i % 8) * 10 + digit + 1] + 1];
    END LOOP;
    RETURN c = 0;
END
$fn$;

CREATE OR REPLACE FUNCTION commerce_refuse_raw_aadhaar() RETURNS trigger
LANGUAGE plpgsql AS $fn$
BEGIN
    IF btrim(COALESCE(NEW.document_number, '')) <> ''
       AND (NEW.document_type = 'aadhaar'
            OR (NEW.document_type <> 'cancelled_cheque'
                AND commerce_looks_like_aadhaar(NEW.document_number))) THEN
        -- No value in the message: an error log is exactly where a leaked
        -- Aadhaar number would otherwise land.
        RAISE EXCEPTION USING
            ERRCODE    = 'check_violation',
            CONSTRAINT = 'seller_documents_no_raw_aadhaar',
            MESSAGE    = 'an Aadhaar number may not be stored; keep the uploaded document reference only';
    END IF;
    RETURN NEW;
END
$fn$;

DROP TRIGGER IF EXISTS trg_seller_documents_no_raw_aadhaar ON seller_documents; -- expand-only: recreated immediately below, so re-running the migration is idempotent
CREATE TRIGGER trg_seller_documents_no_raw_aadhaar
    BEFORE INSERT OR UPDATE OF document_number, document_type ON seller_documents
    FOR EACH ROW EXECUTE FUNCTION commerce_refuse_raw_aadhaar();
