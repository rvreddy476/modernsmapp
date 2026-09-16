-- Commerce — migration 036: an append-only audit trail for admin actions
-- that had none.
--
-- Seller decisions already land in seller_onboarding_reviews and product
-- decisions in product_moderation_log. Three human admin actions wrote
-- nothing at all:
--
--   * COD remittance settle — marks cash the courier collected as paid out
--     to the seller. Money; the row that says "paid" had no author.
--   * seller KYC verify — flips sellers.verification_status, which gates
--     payout eligibility.
--   * banner create / update / delete — what every shopper's home screen
--     shows.
--
-- One table for all three, shaped after product_moderation_log (actor,
-- action, target, reason, time) plus before/after snapshots.
--
-- ─── WHAT MAY NOT BE IN HERE ────────────────────────────────────────────
--
-- KYC rows record the verdict (status before/after, adapter, per-field
-- valid/invalid/skipped). Never the PAN, GSTIN, bank account or UPI handle:
-- migration 035 moved those behind envelope encryption, and an audit table
-- is the last place a plaintext copy should reappear.
--
-- ─── APPEND-ONLY ────────────────────────────────────────────────────────
--
-- A trigger refuses UPDATE and DELETE. An audit row that can be edited by
-- the same database role that writes the action it describes is a note, not
-- an audit. The nil UUID is refused by CHECK so a missing actor can never be
-- written as a plausible-looking id.

CREATE TABLE IF NOT EXISTS commerce_admin_audit_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id   UUID NOT NULL
                        CHECK (actor_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    action          TEXT NOT NULL
                        CHECK (action IN ('cod_remittance_settle','seller_kyc_verify',
                                          'banner_create','banner_update','banner_delete')),
    target_type     TEXT NOT NULL
                        CHECK (target_type IN ('cod_remittance','seller','banner')),
    target_id       UUID NOT NULL,
    before_state    JSONB,
    after_state     JSONB,
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_commerce_admin_audit_target
    ON commerce_admin_audit_log (target_type, target_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_commerce_admin_audit_actor
    ON commerce_admin_audit_log (actor_user_id, created_at DESC);

CREATE OR REPLACE FUNCTION commerce_admin_audit_log_append_only()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'commerce_admin_audit_log is append-only (% refused)', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

DROP TRIGGER IF EXISTS trg_commerce_admin_audit_log_append_only ON commerce_admin_audit_log;
CREATE TRIGGER trg_commerce_admin_audit_log_append_only
    BEFORE UPDATE OR DELETE ON commerce_admin_audit_log
    FOR EACH ROW EXECUTE FUNCTION commerce_admin_audit_log_append_only();
