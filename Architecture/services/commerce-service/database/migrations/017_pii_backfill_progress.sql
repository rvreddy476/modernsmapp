-- 017 — make the PII backfill's progress table load-bearing, and record the
-- cutover's two operator assertions.
--
-- B5. Migration 011 created `pii_backfill_progress` and seeded a row per
-- table, but it only ever tracked a cursor and a count. The gated plaintext
-- scrub needs more than that, because it is the one irreversible step in the
-- cutover: after it runs, ciphertext is the sole copy of every customer's
-- name, phone and street, and a row whose ciphertext is absent or unopenable
-- has had its address destroyed.
--
-- So the scrub has to be able to REFUSE, and refusing needs facts 011 does not
-- record:
--
--	verified         a row is not done when it is encrypted; it is done when
--	                 the ciphertext has been decrypted back and compared to
--	                 the source. "Encrypted to nothing" is a real failure mode
--	                 and it is silent.
--	failed           any row that could not be sealed or verified. A nonzero
--	                 count blocks the scrub outright.
--	last_error_*     which row, and what kind of failure — never the plaintext,
--	                 so an operator can go and look without the log becoming
--	                 the leak.
--
-- The table is EXTENDED rather than replaced: 011's rows, cursor and
-- completion stamps stay exactly as they are, and an old image that knows
-- nothing about the new columns keeps working.
--
-- EXPAND-ONLY: added nullable columns and defaulted counters, one new table.
-- Nothing dropped, renamed or tightened; no constraint added to an existing
-- table.

ALTER TABLE pii_backfill_progress
    ADD COLUMN IF NOT EXISTS verified        BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed          BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS last_error_id   UUID,
    ADD COLUMN IF NOT EXISTS last_error_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error_kind TEXT;

COMMENT ON COLUMN pii_backfill_progress.verified IS
    'B5: rows whose ciphertext was decrypted back and compared to the source before the '
    'transaction committed. Encryption that silently produced garbage would otherwise be '
    'discovered only after the scrub removed the only other copy.';

COMMENT ON COLUMN pii_backfill_progress.failed IS
    'B5: rows that could not be sealed or whose verification did not match. A nonzero value '
    'blocks the gated plaintext scrub. The job never resets it — an operator has to look.';

COMMENT ON COLUMN pii_backfill_progress.last_id IS
    'B5: the highest primary key whose row is COMPLETE — sealed, verified and committed. It '
    'advances in the SAME transaction as the ciphertext it describes, so progress can never '
    'run ahead of the data. The scrub trusts this cursor to decide what is safe to clear.';

-- ─── Cutover state ───────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS pii_cutover_state (
    -- Singleton.
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),

    -- Set by the operator when the ciphertext-authoritative image is live in
    -- every replica. Until then a dual-write image may still be creating
    -- plaintext rows, and scrubbing would race it.
    ciphertext_authoritative_since TIMESTAMPTZ,

    -- Set by the operator when the previous image is fully drained: no pod,
    -- no worker, no cron still running code that writes identifying plaintext.
    --
    -- Deliberately a human assertion rather than something the database
    -- infers. Nothing in PostgreSQL can see a straggler pod in another
    -- namespace, and a scrub that guessed wrong would clear plaintext while a
    -- writer was still producing it.
    old_writers_drained_at TIMESTAMPTZ,

    -- Stamped by the gated scrub itself, so it is visible that it ran.
    scrubbed_at TIMESTAMPTZ,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO pii_cutover_state (id) VALUES (TRUE) ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE pii_cutover_state IS
    'B5: the two operator assertions the gated plaintext scrub requires — that the '
    'ciphertext-authoritative image is live, and that every old writer is drained. Both are '
    'human assertions on purpose: PostgreSQL cannot see a straggler pod, and a scrub that '
    'guessed wrong would clear plaintext while something was still writing it.';
