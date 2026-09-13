-- 1002 — clear the seller KYC plaintext: bank account numbers, PANs, and any
-- raw Aadhaar number.
--
-- Migration 035's cutover, step 5. The only destructive step, and it cannot be
-- undone: after it runs, the ciphertext is the sole copy of every seller's full
-- bank account number and every seller's and organization's PAN. A row whose
-- ciphertext is absent, wrong or unopenable loses that value here — for a
-- payout account, that is a seller who cannot be paid.
--
-- Mirrors gated/1000 (addresses), against the KYC cutover's own state:
-- `pii_kyc_cutover_state` and `pii_kyc_backfill_progress`.
--
-- ─── WHAT IS AND IS NOT CLEARED ─────────────────────────────────────────
--
-- CLEARED: seller_payout_accounts.account_number (to '' — the column is NOT
-- NULL), sellers.pan_number, organizations.pan, and any
-- seller_documents.document_number that is a raw Aadhaar number (the same rule
-- as migration 035's trigger: any number on an `aadhaar` document, or an
-- Aadhaar-shaped number on any type but `cancelled_cheque`). The Aadhaar
-- numbers are cleared without needing any ciphertext, because there is none
-- and there must not be: commerce may not hold them at all.
--
-- KEPT: IFSC, holder name, bank name, account_number_last4, pan_masked, the
-- lookup hashes, and GSTIN (public, printed on invoices).
--
-- No column is dropped — only contents cleared — so a mistake is recoverable
-- from the backup in step 5.
--
-- ─── ORDER ──────────────────────────────────────────────────────────────
--   1. deploy the dual-write image (COMMERCE_KYC_PII_CUTOVER=dual) with 035;
--   2. run `piibackfill -set=kyc` to completion: every table completed_at set,
--      failed = 0;
--   3. deploy COMMERCE_KYC_PII_CUTOVER=ciphertext and stamp
--      pii_kyc_cutover_state.ciphertext_authoritative_since;
--   4. drain every old writer and stamp old_writers_drained_at;
--   5. take a backup;
--   6. run this file.
--
-- NOTE: `commerce-migrate -gated` applies gated files in string order and stops
-- at the first failure, so through the runner this applies only after 1000 and
-- 1001 have. To run the KYC cutover ahead of the address one, apply this file
-- by hand (psql -v ON_ERROR_STOP=1) and record its schema_migrations row.
--
-- ROLLBACK: none. Restore the step-5 backup.

DO $$
DECLARE
    st              RECORD;
    tracked         BIGINT;
    incomplete      BIGINT;
    failed_rows     BIGINT;
    unsealed_payout BIGINT;
    unsealed_seller BIGINT;
    unsealed_org    BIGINT;
    sealed_before   BIGINT;
    sealed_after    BIGINT;
    scrubbed_payout BIGINT;
    scrubbed_seller BIGINT;
    scrubbed_org    BIGINT;
    scrubbed_docs   BIGINT;
    residual        BIGINT;
BEGIN
    -- ── Precondition 1: the operator assertions ───────────────────────
    SELECT * INTO st FROM pii_kyc_cutover_state WHERE id;
    IF st IS NULL THEN
        RAISE EXCEPTION 'KYC scrub precondition failed: pii_kyc_cutover_state is missing (migration 035 has not run)';
    END IF;
    IF st.ciphertext_authoritative_since IS NULL THEN
        RAISE EXCEPTION USING
            MESSAGE = 'KYC scrub precondition failed: the ciphertext-authoritative image is not recorded as live',
            HINT    = 'Deploy COMMERCE_KYC_PII_CUTOVER=ciphertext everywhere, then: UPDATE pii_kyc_cutover_state '
                      'SET ciphertext_authoritative_since = NOW() WHERE id;';
    END IF;
    IF st.old_writers_drained_at IS NULL THEN
        RAISE EXCEPTION USING
            MESSAGE = 'KYC scrub precondition failed: old writers are not recorded as drained',
            DETAIL  = 'A dual-write pod still running would keep writing bank account numbers and PANs '
                      'in plaintext while this transaction clears them.',
            HINT    = 'Confirm every replica runs the ciphertext-authoritative image, then: '
                      'UPDATE pii_kyc_cutover_state SET old_writers_drained_at = NOW() WHERE id;';
    END IF;
    IF st.scrubbed_at IS NOT NULL THEN
        RAISE NOTICE 'KYC: plaintext was already scrubbed at %; re-running to catch stragglers', st.scrubbed_at;
    END IF;

    -- ── Precondition 2: the backfill finished, and nothing failed ─────
    SELECT count(*) INTO tracked FROM pii_kyc_backfill_progress;
    IF tracked = 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = 'KYC scrub precondition failed: the KYC backfill has never run',
            HINT    = 'Run piibackfill -set=kyc first.';
    END IF;

    SELECT count(*) INTO incomplete FROM pii_kyc_backfill_progress WHERE completed_at IS NULL;
    SELECT COALESCE(sum(failed), 0) INTO failed_rows FROM pii_kyc_backfill_progress;
    IF incomplete > 0 OR failed_rows > 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = format('KYC scrub precondition failed: backfill incomplete_tables=%s failed_rows=%s',
                             incomplete, failed_rows),
            DETAIL  = 'Every tracked table must have completed_at set and failed = 0.',
            HINT    = 'Re-run piibackfill -set=kyc. A failed row is one whose ciphertext could not be '
                      'written or decrypted back to its source; scrubbing it would destroy it.';
    END IF;

    -- ── Precondition 3: the data itself, checked directly ─────────────
    SELECT count(*) INTO unsealed_payout
      FROM seller_payout_accounts
     WHERE btrim(account_number) <> ''
       AND (account_number_enc IS NULL OR pii_key_version IS NULL OR pii_key_version <= 0);

    SELECT count(*) INTO unsealed_seller
      FROM sellers
     WHERE btrim(COALESCE(pan_number, '')) <> ''
       AND (pan_enc IS NULL OR pan_key_version IS NULL OR pan_key_version <= 0);

    SELECT count(*) INTO unsealed_org
      FROM organizations
     WHERE btrim(COALESCE(pan, '')) <> ''
       AND (pan_enc IS NULL OR pan_key_version IS NULL OR pan_key_version <= 0);

    IF unsealed_payout > 0 OR unsealed_seller > 0 OR unsealed_org > 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = format('KYC scrub precondition failed: unsealed_payout_accounts=%s unsealed_seller_pans=%s '
                             'unsealed_organization_pans=%s', unsealed_payout, unsealed_seller, unsealed_org),
            DETAIL  = 'These rows hold plaintext with no ciphertext, or none naming the key version needed to '
                      'open it. Clearing their plaintext destroys the value.',
            HINT    = 'Re-run piibackfill -set=kyc until every row is sealed, then re-run this scrub.';
    END IF;

    SELECT (SELECT count(*) FROM seller_payout_accounts WHERE account_number_enc IS NOT NULL)
         + (SELECT count(*) FROM sellers WHERE pan_enc IS NOT NULL)
         + (SELECT count(*) FROM organizations WHERE pan_enc IS NOT NULL)
      INTO sealed_before;

    -- ── The scrub ─────────────────────────────────────────────────────
    UPDATE seller_payout_accounts SET account_number = '' WHERE account_number <> '';
    GET DIAGNOSTICS scrubbed_payout = ROW_COUNT;

    UPDATE sellers SET pan_number = NULL WHERE pan_number IS NOT NULL;
    GET DIAGNOSTICS scrubbed_seller = ROW_COUNT;

    UPDATE organizations SET pan = NULL WHERE pan IS NOT NULL;
    GET DIAGNOSTICS scrubbed_org = ROW_COUNT;

    UPDATE seller_documents
       SET document_number = NULL
     WHERE btrim(COALESCE(document_number, '')) <> ''
       AND (document_type = 'aadhaar'
            OR (document_type <> 'cancelled_cheque' AND commerce_looks_like_aadhaar(document_number)));
    GET DIAGNOSTICS scrubbed_docs = ROW_COUNT;

    -- ── Verify, in the same transaction ───────────────────────────────
    SELECT count(*) INTO residual FROM (
        SELECT 1 FROM seller_payout_accounts WHERE account_number <> ''
        UNION ALL
        SELECT 1 FROM sellers WHERE pan_number IS NOT NULL
        UNION ALL
        SELECT 1 FROM organizations WHERE pan IS NOT NULL
        UNION ALL
        SELECT 1 FROM seller_documents
         WHERE btrim(COALESCE(document_number, '')) <> ''
           AND (document_type = 'aadhaar'
                OR (document_type <> 'cancelled_cheque' AND commerce_looks_like_aadhaar(document_number)))
    ) r;
    IF residual > 0 THEN
        RAISE EXCEPTION 'KYC scrub verification failed: % row(s) still hold KYC plaintext; rolling the whole scrub back',
                        residual;
    END IF;

    -- Clearing plaintext must never have cleared ciphertext (035's stale-ciphertext
    -- triggers exempt blanking writes; this proves it held).
    SELECT (SELECT count(*) FROM seller_payout_accounts WHERE account_number_enc IS NOT NULL)
         + (SELECT count(*) FROM sellers WHERE pan_enc IS NOT NULL)
         + (SELECT count(*) FROM organizations WHERE pan_enc IS NOT NULL)
      INTO sealed_after;
    IF sealed_after <> sealed_before THEN
        RAISE EXCEPTION 'KYC scrub verification failed: sealed rows went from % to %; rolling the whole scrub back',
                        sealed_before, sealed_after;
    END IF;

    UPDATE pii_kyc_cutover_state SET scrubbed_at = NOW(), updated_at = NOW() WHERE id;

    RAISE NOTICE 'KYC: plaintext scrubbed (payout_accounts=% seller_pans=% organization_pans=% aadhaar_numbers=%); '
                 'zero KYC plaintext remains', scrubbed_payout, scrubbed_seller, scrubbed_org, scrubbed_docs;
END$$;
