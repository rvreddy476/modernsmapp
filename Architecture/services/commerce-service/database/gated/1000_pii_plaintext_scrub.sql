-- 1000 — clear the identifying address plaintext.
--
-- B5. This is the only destructive step in the PII cutover, and the only one
-- that cannot be undone: after it runs, the ciphertext is the sole copy of
-- every customer's and seller's name, phone, street and landmark. If any row's
-- ciphertext is absent, wrong, or unopenable, this destroys that address.
--
-- Everything below the RAISEs exists to make that impossible to do by
-- accident. The preconditions are not defensive politeness; each one names a
-- specific way the estate could be unsafe to scrub.
--
-- ─── WHAT IS AND IS NOT CLEARED ─────────────────────────────────────────
--
-- CLEARED (identifying): contact_name, phone, address_line_1, address_line_2,
-- landmark, and the identifying half of orders.delivery_address_snapshot.
--
-- KEPT IN PLAINTEXT (routing and tax): city, state, postal_code, country.
-- These are read directly in SQL by the courier serviceability check, the GST
-- place-of-supply determination and the shipping-quote binding. None of them
-- identifies a person: a postal code is a delivery region, not a customer.
-- Encrypting them would mean decrypting every row to answer "can we deliver
-- here", which is a query, not a lookup.
--
-- NOT TOUCHED: order_snapshot retention and key shredding. Those wait on a
-- legal retention decision, and this file deliberately has no opinion on them.
-- No column is dropped here either — only contents cleared — so a mistake is
-- recoverable from a backup rather than from a schema migration.
--
-- ─── LOCK BEHAVIOUR AND ROLLOUT ─────────────────────────────────────────
--
-- Row-level locks on every row it updates, inside one transaction. On a large
-- estate this is a long transaction; run it in a maintenance window with
-- writers drained, which the preconditions require anyway.
--
-- ORDER:
--   1. deploy the dual-write image (COMMERCE_PII_CUTOVER=dual);
--   2. run the backfill to completion; every table must report
--      completed_at set and failed = 0;
--   3. deploy the ciphertext-authoritative image
--      (COMMERCE_PII_CUTOVER=ciphertext) and stamp
--      pii_cutover_state.ciphertext_authoritative_since;
--   4. drain every old writer and stamp old_writers_drained_at;
--   5. take a backup;
--   6. run this file.
--
-- ROLLBACK: there is none. Restore from the backup taken at step 5. That is
-- why step 5 is in the list.

DO $$
DECLARE
    unsealed_customer BIGINT;
    unsealed_seller   BIGINT;
    no_version        BIGINT;
    incomplete        BIGINT;
    failed_rows       BIGINT;
    tracked           BIGINT;
    st                RECORD;
    scrubbed_cust     BIGINT;
    scrubbed_sell     BIGINT;
    scrubbed_orders   BIGINT;
    residual          BIGINT;
BEGIN
    -- ── Precondition 1: the operator assertions ───────────────────────
    SELECT * INTO st FROM pii_cutover_state WHERE id;
    IF st IS NULL THEN
        RAISE EXCEPTION 'B5 scrub precondition failed: pii_cutover_state is missing (migration 017 has not run)';
    END IF;
    IF st.ciphertext_authoritative_since IS NULL THEN
        RAISE EXCEPTION USING
            MESSAGE = 'B5 scrub precondition failed: the ciphertext-authoritative image is not recorded as live',
            HINT    = 'Deploy COMMERCE_PII_CUTOVER=ciphertext everywhere, then: UPDATE pii_cutover_state '
                      'SET ciphertext_authoritative_since = NOW() WHERE id;';
    END IF;
    IF st.old_writers_drained_at IS NULL THEN
        RAISE EXCEPTION USING
            MESSAGE = 'B5 scrub precondition failed: old writers are not recorded as drained',
            DETAIL  = 'A dual-write pod, worker or cron still running would keep producing identifying '
                      'plaintext while this transaction clears it.',
            HINT    = 'Confirm every replica runs the ciphertext-authoritative image, then: '
                      'UPDATE pii_cutover_state SET old_writers_drained_at = NOW() WHERE id;';
    END IF;
    IF st.scrubbed_at IS NOT NULL THEN
        RAISE NOTICE 'B5: plaintext was already scrubbed at %; re-running to catch stragglers', st.scrubbed_at;
    END IF;

    -- ── Precondition 2: the backfill finished, and nothing failed ─────
    SELECT count(*) INTO tracked FROM pii_backfill_progress;
    IF tracked = 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = 'B5 scrub precondition failed: the backfill has never run',
            HINT    = 'Run the piibackfill job first. Clearing plaintext that was never encrypted '
                      'destroys the address.';
    END IF;

    SELECT count(*) INTO incomplete FROM pii_backfill_progress WHERE completed_at IS NULL;
    SELECT COALESCE(sum(failed), 0) INTO failed_rows FROM pii_backfill_progress;
    IF incomplete > 0 OR failed_rows > 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = format('B5 scrub precondition failed: backfill incomplete_tables=%s failed_rows=%s',
                             incomplete, failed_rows),
            DETAIL  = 'Every tracked table must have completed_at set and failed = 0.',
            HINT    = 'Re-run the backfill. A failed row is one whose ciphertext could not be written '
                      'or could not be decrypted back to its source; scrubbing it would destroy it.';
    END IF;

    -- ── Precondition 3: the data itself, checked directly ─────────────
    --
    -- The progress table is a claim. This checks the estate, because a
    -- cursor that says "done" and a table that still holds unsealed rows
    -- disagree, and the table is what gets scrubbed.
    SELECT count(*) INTO unsealed_customer
      FROM customer_addresses
     WHERE contact_name_enc IS NULL OR address_line_1_enc IS NULL;

    SELECT count(*) INTO unsealed_seller
      FROM seller_addresses
     WHERE contact_name_enc IS NULL OR address_line_1_enc IS NULL;

    SELECT count(*) INTO no_version
      FROM (
        SELECT 1 FROM customer_addresses WHERE pii_key_version IS NULL OR pii_key_version <= 0
        UNION ALL
        SELECT 1 FROM seller_addresses   WHERE pii_key_version IS NULL OR pii_key_version <= 0
      ) v;

    IF unsealed_customer > 0 OR unsealed_seller > 0 OR no_version > 0 THEN
        RAISE EXCEPTION USING
            MESSAGE = format('B5 scrub precondition failed: unsealed_customer=%s unsealed_seller=%s '
                             'missing_key_version=%s', unsealed_customer, unsealed_seller, no_version),
            DETAIL  = 'These rows have no ciphertext, or none that names the key version needed to '
                      'open it. Clearing their plaintext destroys the address.',
            HINT    = 'Re-run the backfill until every row is sealed, then re-run this scrub.';
    END IF;

    -- ── The scrub ─────────────────────────────────────────────────────
    --
    -- Identifying columns only. NOT NULL columns become '', nullable ones
    -- become NULL, so "nonblank identifying plaintext" is the property the
    -- verification below can assert.
    UPDATE customer_addresses
       SET contact_name   = '',
           phone          = '',
           address_line_1 = '',
           address_line_2 = NULL,
           landmark       = NULL,
           updated_at     = NOW()
     WHERE contact_name <> '' OR phone <> '' OR address_line_1 <> ''
        OR address_line_2 IS NOT NULL OR landmark IS NOT NULL;
    GET DIAGNOSTICS scrubbed_cust = ROW_COUNT;

    UPDATE seller_addresses
       SET contact_name   = '',
           phone          = '',
           address_line_1 = '',
           address_line_2 = NULL
     WHERE contact_name <> '' OR phone <> '' OR address_line_1 <> ''
        OR address_line_2 IS NOT NULL;
    GET DIAGNOSTICS scrubbed_sell = ROW_COUNT;

    -- The order snapshot keeps only its routing fields. The identifying half
    -- lives in delivery_address_snapshot_enc, which migration 011 added and
    -- checkout has been writing since.
    --
    -- Only rows that HAVE the encrypted copy are reduced: an order whose
    -- snapshot was never sealed keeps its plaintext rather than losing the
    -- address it must be fulfilled and invoiced against.
    UPDATE orders
       SET delivery_address_snapshot = jsonb_build_object(
               'city',        delivery_address_snapshot->>'city',
               'state',       delivery_address_snapshot->>'state',
               'postal_code', delivery_address_snapshot->>'postal_code',
               'country',     delivery_address_snapshot->>'country'
           ),
           updated_at = NOW()
     WHERE delivery_address_snapshot_enc IS NOT NULL
       AND snapshot_key_version IS NOT NULL
       AND delivery_address_snapshot IS NOT NULL
       AND (delivery_address_snapshot ? 'contact_name'
         OR delivery_address_snapshot ? 'phone'
         OR delivery_address_snapshot ? 'address_line_1');
    GET DIAGNOSTICS scrubbed_orders = ROW_COUNT;

    -- ── Verify: zero nonblank identifying plaintext remains ───────────
    --
    -- Inside the same transaction, so a verification failure rolls the whole
    -- scrub back rather than leaving a half-cleared estate.
    SELECT count(*) INTO residual FROM (
        SELECT 1 FROM customer_addresses
         WHERE contact_name <> '' OR phone <> '' OR address_line_1 <> ''
            OR COALESCE(address_line_2,'') <> '' OR COALESCE(landmark,'') <> ''
        UNION ALL
        SELECT 1 FROM seller_addresses
         WHERE contact_name <> '' OR phone <> '' OR address_line_1 <> ''
            OR COALESCE(address_line_2,'') <> ''
        UNION ALL
        SELECT 1 FROM orders
         WHERE delivery_address_snapshot_enc IS NOT NULL
           AND (delivery_address_snapshot ? 'contact_name'
             OR delivery_address_snapshot ? 'phone'
             OR delivery_address_snapshot ? 'address_line_1')
    ) r;

    IF residual > 0 THEN
        RAISE EXCEPTION 'B5 scrub verification failed: % row(s) still hold identifying plaintext; '
                        'rolling the whole scrub back', residual;
    END IF;

    UPDATE pii_cutover_state SET scrubbed_at = NOW(), updated_at = NOW() WHERE id;

    RAISE NOTICE 'B5: plaintext scrubbed (customer_addresses=% seller_addresses=% orders=%); '
                 'zero nonblank identifying plaintext remains',
                 scrubbed_cust, scrubbed_sell, scrubbed_orders;
END$$;
