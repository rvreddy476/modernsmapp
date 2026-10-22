-- Field encryption at rest (founder, 22 Oct 2026). Additive and idempotent;
-- database/setup.sql carries the same statements (applied on every boot by
-- BootstrapSchema).
--
-- No end-to-end encryption for launch. Instead message text and every other
-- content-bearing column is sealed by message-service (AES-256-GCM, envelope
-- encryption, shared/fieldcrypt) before it is written, so a leaked database
-- or backup shows nothing readable. Sealed values are "cfe1:<base64url>" in
-- TEXT columns and the same string as a JSON string in JSONB columns, so no
-- column changes type except the one below. Rows written before this keep
-- reading (no prefix = legacy plaintext); cmd/fieldcrypt-backfill re-seals
-- them.

-- 1. A request preview of up to 500 characters is up to ~2.7 KB sealed.
--    VARCHAR(500) -> TEXT is binary-compatible (no rewrite); the guard keeps
--    the boot replay from taking the table lock every time.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'chat' AND table_name = 'message_requests'
          AND column_name = 'preview' AND data_type <> 'text'
    ) THEN
        ALTER TABLE chat.message_requests ALTER COLUMN preview TYPE TEXT;
    END IF;
END $$;

-- 2. KMS-wrapped master keys (production; APP_ENV=production refuses to boot
--    without them). One row per version; the plaintext key never touches the
--    database. encryption_context is passed back to KMS verbatim on unwrap.
--    Losing this table loses every value sealed under its keys: it is in the
--    Aurora backups with everything else, and the keys are useless without
--    the KMS key that wrapped them.
CREATE TABLE IF NOT EXISTS chat.field_key_ring (
    version            INT PRIMARY KEY CHECK (version > 0),
    wrapped_key        BYTEA NOT NULL,
    kms_key_id         TEXT NOT NULL,
    encryption_context JSONB NOT NULL,
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at         TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_field_key_ring_one_active
    ON chat.field_key_ring ((active)) WHERE active;

-- 3. Resume points for cmd/fieldcrypt-backfill (one row per table it walks).
--    Only keys and counts; never content.
CREATE TABLE IF NOT EXISTS chat.field_backfill_progress (
    target       TEXT PRIMARY KEY,
    resume_after TEXT NOT NULL DEFAULT '',
    done         BOOLEAN NOT NULL DEFAULT FALSE,
    scanned      BIGINT NOT NULL DEFAULT 0,
    sealed       BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 4. Retention: published outbox rows older than 7 days are deleted by the
--    outbox retention worker; this keeps that DELETE an index range scan.
CREATE INDEX IF NOT EXISTS idx_outbox_published_at
    ON chat.outbox_events(published_at) WHERE published_at IS NOT NULL;
