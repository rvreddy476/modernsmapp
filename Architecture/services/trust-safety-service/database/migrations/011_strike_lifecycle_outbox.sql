-- 011_strike_lifecycle_outbox.sql: strikes get a lifecycle and trust-safety
-- gets a transactional outbox (Copyright Match plan, section 4 P-6 / P-7,
-- section 6.4).
--
-- Before this file a strike had no case link, no idempotency key, no policy
-- version, usually no expiry (so it was permanent) and no way to be reversed
-- except DELETE. A retried issue minted a second strike. Now:
--
--   * idempotency_key is UNIQUE where set: the same key is the same strike.
--   * expires_at is always set. Legacy rows with none are given
--     created_at + 90 days (founder default F-15) and marked
--     policy_version = 'legacy-f15-90d' so they stay identifiable; the
--     count is raised as a NOTICE here and logged by the service at boot.
--   * A strike is voided (voided_at / void_reason / voided_by), never
--     deleted. "Active" means voided_at IS NULL AND expires_at > now().
--   * trust.admin_audit may name a strike.
--   * trust.enforcement_outbox holds the events written in the SAME
--     transaction as the strike issue/void and the purge erase; the
--     dispatcher (internal/outbox) relays them to Kafka.
--
-- Re-runnable: the integration helpers apply every migration file on each
-- open, so every statement is IF NOT EXISTS or DO-guarded.

ALTER TABLE trust.user_strikes
    ADD COLUMN IF NOT EXISTS case_id         UUID,
    ADD COLUMN IF NOT EXISTS strike_group    TEXT,
    ADD COLUMN IF NOT EXISTS policy_version  TEXT NOT NULL DEFAULT 'strike-v1',
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS voided_at       TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS void_reason     TEXT,
    ADD COLUMN IF NOT EXISTS voided_by       UUID;

-- F-15: a legacy strike with no expiry lasts 90 days from issue.
DO $$
DECLARE n BIGINT;
BEGIN
    UPDATE trust.user_strikes
       SET expires_at = created_at + interval '90 days',
           policy_version = 'legacy-f15-90d'
     WHERE expires_at IS NULL;
    GET DIAGNOSTICS n = ROW_COUNT;
    IF n > 0 THEN
        RAISE NOTICE 'trust.user_strikes: % legacy strike(s) given expires_at = created_at + 90 days (F-15)', n;
    END IF;
END $$;

ALTER TABLE trust.user_strikes ALTER COLUMN expires_at SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_strikes_expiry_after_issue') THEN
        ALTER TABLE trust.user_strikes ADD CONSTRAINT user_strikes_expiry_after_issue
            CHECK (expires_at > created_at);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_strikes_void_shape') THEN
        ALTER TABLE trust.user_strikes ADD CONSTRAINT user_strikes_void_shape CHECK (
            (voided_at IS NULL AND void_reason IS NULL AND voided_by IS NULL)
            OR (voided_at IS NOT NULL AND void_reason IS NOT NULL AND btrim(void_reason) <> ''
                AND voided_by IS NOT NULL));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_strikes_idempotency_key_shape') THEN
        ALTER TABLE trust.user_strikes ADD CONSTRAINT user_strikes_idempotency_key_shape
            CHECK (idempotency_key IS NULL OR btrim(idempotency_key) <> '');
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_strikes_idem
    ON trust.user_strikes (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_user_strikes_active
    ON trust.user_strikes (user_id, expires_at) WHERE voided_at IS NULL;

-- The audit log may now name a strike (strike.issued / strike.voided).
-- Guarded: a later migration widens this CHECK again (013 adds
-- copyright_case), and re-running an unguarded DROP + ADD here after rows
-- of the wider type exist would fail. The constraint is replaced only when
-- it is missing or does not yet admit 'strike'.
DO $$
DECLARE def TEXT;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conname = 'admin_audit_target_type_check' AND conrelid = 'trust.admin_audit'::regclass;
    IF def IS NULL OR def NOT LIKE '%strike%' THEN
        ALTER TABLE trust.admin_audit DROP CONSTRAINT IF EXISTS admin_audit_target_type_check;
        ALTER TABLE trust.admin_audit ADD CONSTRAINT admin_audit_target_type_check
            CHECK (target_type IN ('report', 'appeal', 'grievance', 'strike'));
    END IF;
END $$;

-- Transactional outbox. payload is the exact Kafka message value (an
-- EventEnvelope for the trust topic, a bare ack for the purge-acks topic);
-- the dispatcher never rebuilds it. event_id is minted once, so a row
-- published twice (crash between publish and mark) is a duplicate, never a
-- new event.
CREATE TABLE IF NOT EXISTS trust.enforcement_outbox (
    id              BIGSERIAL PRIMARY KEY,
    event_id        UUID NOT NULL UNIQUE,
    event_type      TEXT NOT NULL CHECK (event_type <> ''),
    topic           TEXT NOT NULL CHECK (topic <> ''),
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at    TIMESTAMPTZ,
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_error      TEXT
);
CREATE INDEX IF NOT EXISTS idx_enforcement_outbox_pending
    ON trust.enforcement_outbox (id) WHERE published_at IS NULL;
