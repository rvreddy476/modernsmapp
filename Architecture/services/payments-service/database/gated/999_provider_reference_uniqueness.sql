-- 999 — one provider reference belongs to exactly one intent.
--
-- R4-LB-2 / A2. The application already refuses a reference that is ALREADY
-- ambiguous when it looks: ApplyWebhookAtomically counts matches before it
-- locks, and the refund resolver insists on exactly one target. What it cannot
-- do is stop a duplicate appearing WHILE it looks.
--
-- Under READ COMMITTED, the capture path's count and its subsequent
-- `SELECT ... FOR UPDATE` are two statements against two snapshots, and
-- SetProviderOrder has no cross-intent serialisation at all. A concurrent
-- attach of the same reference to a second intent can slip between them, and
-- the locking lookup then selects an arbitrary row. The consequence is a
-- genuine, signature-verified capture settling the wrong local order.
--
-- No amount of application care closes a phantom under READ COMMITTED. The
-- database has to hold the invariant, so this installs it.
--
-- ─── WHY THIS IS GATED, NOT A BOOT MIGRATION ────────────────────────────
--
-- It is a CONTRACT operation twice over:
--
--   * it can REFUSE existing data (a duplicate already in the table);
--   * `CREATE UNIQUE INDEX` takes a SHARE lock on payment_intents, which
--     blocks INSERT/UPDATE/DELETE for its duration.
--
-- Either would break a live mixed-version fleet mid-rollout, which is exactly
-- what the boot set is forbidden from doing.
--
-- LOCK BEHAVIOUR: SHARE on payments.payment_intents for the whole DO block.
-- Concurrent reads continue; every write blocks. On a small intents table this
-- is sub-second, but it is a write outage and must be scheduled.
--
-- CONCURRENTLY is deliberately NOT used: it cannot run inside a transaction
-- block, so the precondition, the backfill and the index could not commit or
-- roll back together — and a failed CONCURRENTLY build leaves an INVALID index
-- silently enforcing nothing.
--
-- ROLLOUT ORDER:
--   1. drain writers (scale payments-service to zero, or stop the reconciler
--      and put the webhook ingress into 503);
--   2. run this file; if it RAISEs, nothing is installed — hand the report to
--      finance/operations and stop;
--   3. operations resolves every duplicate using PROVIDER evidence, deciding
--      which intent genuinely owns each reference;
--   4. re-run; on success the index exists;
--   5. restore writers.
--
-- ROLLBACK: `DROP INDEX payments.uq_payment_intents_provider_reference;`
-- The backfill in step 2 below is NOT rolled back by that, and does not need
-- to be: it only copies a value that was already the effective reference into
-- the column that should have held it.
--
-- This file NEVER picks a winner among duplicates. Choosing one would silently
-- decide which customer's payment is attributed where, and that is a finance
-- decision made against the provider's records, not a decision a migration
-- may make.

DO $$
DECLARE
    blank_provider   BIGINT;
    disagreeing      BIGINT;
    dup_groups       BIGINT;
    dup_rows         BIGINT;
    sample           TEXT;
BEGIN
    -- ── Precondition 1: a reference with no provider ──────────────────
    --
    -- The identity is (provider, effective reference). A row carrying a
    -- reference but no provider has no identity, so uniqueness cannot be
    -- expressed over it and the index would silently not cover it.
    SELECT count(*) INTO blank_provider
      FROM payments.payment_intents
     WHERE COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) IS NOT NULL
       AND (provider IS NULL OR provider = '');

    -- ── Precondition 2: the two columns disagree ──────────────────────
    --
    -- provider_ref is the legacy column; provider_order_id superseded it and
    -- SetProviderOrder writes both. A row where both are nonblank and DIFFER
    -- is a row whose reference we cannot name, so the backfill below must not
    -- guess which one is real.
    SELECT count(*) INTO disagreeing
      FROM payments.payment_intents
     WHERE NULLIF(provider_order_id,'') IS NOT NULL
       AND NULLIF(provider_ref,'')      IS NOT NULL
       AND provider_order_id <> provider_ref;

    -- ── Precondition 3: duplicates ────────────────────────────────────
    SELECT count(*), COALESCE(sum(n), 0) INTO dup_groups, dup_rows
      FROM (
        SELECT count(*) AS n
          FROM payments.payment_intents
         WHERE COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) IS NOT NULL
         GROUP BY provider, COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,''))
        HAVING count(*) > 1
      ) d;

    IF blank_provider > 0 OR disagreeing > 0 OR dup_groups > 0 THEN
        -- The report names the rows, so operations can resolve them against
        -- the provider's own records. Capped so a large break does not
        -- produce an unreadable error.
        SELECT string_agg(line, E'\n') INTO sample FROM (
            SELECT format('  provider=%s ref=%s intents=[%s]',
                          COALESCE(NULLIF(provider,''),'<blank>'),
                          COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')),
                          string_agg(id::text, ', ' ORDER BY created_at)) AS line
              FROM payments.payment_intents
             WHERE COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) IS NOT NULL
             GROUP BY provider, COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,''))
            HAVING count(*) > 1
                OR bool_or(provider IS NULL OR provider = '')
             ORDER BY 1
             LIMIT 50
        ) s;

        RAISE EXCEPTION USING
            MESSAGE = format(
                'A2 precondition failed: provider references are not unique. '
                'blank_provider=%s disagreeing_columns=%s duplicate_groups=%s duplicate_rows=%s',
                blank_provider, disagreeing, dup_groups, dup_rows),
            DETAIL  = COALESCE(sample, '(no duplicate groups; see the disagreeing-column count)'),
            HINT    = 'This migration never picks a winner. Each duplicate must be resolved by '
                      'finance/operations against the PROVIDER''s records, deciding which intent '
                      'genuinely owns the reference. Re-run after every group is resolved.';
    END IF;

    -- ── Backfill, only where it is unambiguous ────────────────────────
    --
    -- A legacy row may carry its reference only in provider_ref. Copy it into
    -- provider_order_id so the authoritative column holds it. Precondition 2
    -- has already proved the two never disagree, so this cannot overwrite a
    -- different value, and the WHERE clause touches only blank targets.
    UPDATE payments.payment_intents
       SET provider_order_id = provider_ref,
           updated_at        = NOW()
     WHERE NULLIF(provider_ref,'')      IS NOT NULL
       AND NULLIF(provider_order_id,'') IS NULL;

    -- ── The invariant ─────────────────────────────────────────────────
    --
    -- Expression index over the EFFECTIVE reference, so a future writer that
    -- populates only the legacy column is still covered. Partial, so the many
    -- intents that have not yet opened a provider order (a legitimate,
    -- non-unique state) are excluded.
    CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_intents_provider_reference
        ON payments.payment_intents (
            provider,
            (COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')))
        )
        WHERE COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) IS NOT NULL;

    -- ── Assert it exists ──────────────────────────────────────────────
    --
    -- IF NOT EXISTS above is silent when an index of that NAME already exists,
    -- even a differently-defined one. Assert the invariant is actually present
    -- rather than trusting the command not to have been a no-op.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = 'payments'
           AND tablename  = 'payment_intents'
           AND indexname  = 'uq_payment_intents_provider_reference'
    ) THEN
        RAISE EXCEPTION 'A2: the provider-reference unique index was not installed';
    END IF;

    RAISE NOTICE 'A2: provider-reference uniqueness installed (blank_provider=0 disagreeing=0 duplicates=0)';
END$$;

COMMENT ON INDEX payments.uq_payment_intents_provider_reference IS
    'R4-LB-2: one provider reference belongs to exactly one intent. The application '
    'refuses references that are already ambiguous, but under READ COMMITTED it cannot '
    'stop a duplicate appearing between its count and its locking lookup — so a genuine '
    'capture could settle an arbitrary order. Keyed on (provider, effective reference), '
    'where the effective reference prefers provider_order_id and falls back to the legacy '
    'provider_ref. Partial: an intent with no provider order yet is legitimately not unique.';
