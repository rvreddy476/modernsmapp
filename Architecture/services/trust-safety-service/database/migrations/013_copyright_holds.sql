-- 013_copyright_holds.sql: the copyright case shell and the restriction
-- command queue (Copyright Match plan, sections 6.4 "restriction commands",
-- 9.2 and 9.5).
--
-- A hold that post-service enforces needs an owner on this side: a case
-- that says what the hold's state ought to be, a revision that only ever
-- rises, and one command row per transition whose decision_id is minted
-- ONCE and persisted BEFORE anything is sent, so a crash or a retry
-- re-sends the same id (re-signed) and post-service replays it.
--
-- trust.copyright_cases is deliberately a SHELL: only what a hold needs
-- now. The removal-request schema in plan section 6.4 (claimant, regime,
-- evidence snapshot, tiers, outcome codes, retention) waits on counsel
-- items L-1..L-14; those columns are added by a later migration, and the
-- interim state names here (hold_active / hold_released) are mapped onto
-- section 7's vocabulary then.
--
-- trust.restriction_commands is its own queue, NOT a kind on
-- trust.enforcement_outbox (011). The outbox is a Kafka relay: its payload
-- is the exact message value, it is published in id order and its only
-- outcomes are published / not yet. A restriction command is an HTTP
-- compare-and-set with four terminal outcomes (acked, superseded, parked,
-- or still pending), is signed at send time with a fresh 15-minute
-- capability, must be ordered per case and must never hold Kafka events
-- behind a parked HTTP call (nor the reverse). Sharing the table would
-- have meant a kind switch in both the store and the dispatcher and one
-- of the two orderings giving way.
--
-- Re-runnable: the integration helpers apply every migration file on each
-- open, so every statement is IF NOT EXISTS or DO-guarded.

CREATE TABLE IF NOT EXISTS trust.copyright_cases (
    id                 UUID PRIMARY KEY,
    subject_post_id    UUID NOT NULL,
    subject_author_id  UUID NOT NULL,
    source             TEXT NOT NULL DEFAULT 'copyright' CHECK (source IN ('copyright')),
    -- The hold state the case EXPECTS post-service to hold. Whether
    -- post-service has acknowledged it is on the command row.
    state              TEXT NOT NULL CHECK (state IN ('hold_active', 'hold_released')),
    case_revision      BIGINT NOT NULL DEFAULT 1 CHECK (case_revision > 0),
    policy_version     TEXT NOT NULL CHECK (btrim(policy_version) <> ''),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT copyright_cases_ids_not_nil CHECK (
        subject_post_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND subject_author_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

-- The reconciliation sweep reads active cases and recently released ones.
CREATE INDEX IF NOT EXISTS idx_copyright_cases_reconcile
    ON trust.copyright_cases (state, updated_at);
CREATE INDEX IF NOT EXISTS idx_copyright_cases_subject
    ON trust.copyright_cases (subject_post_id, created_at DESC);

CREATE TABLE IF NOT EXISTS trust.restriction_commands (
    decision_id        UUID PRIMARY KEY,
    case_id            UUID NOT NULL REFERENCES trust.copyright_cases(id),
    case_revision      BIGINT NOT NULL CHECK (case_revision > 0),
    action             TEXT NOT NULL CHECK (action IN ('place_hold', 'release_hold')),
    source             TEXT NOT NULL CHECK (source IN ('copyright')),
    subject_post_id    UUID NOT NULL,
    subject_author_id  UUID NOT NULL,
    expected_state     TEXT NOT NULL CHECK (expected_state IN ('absent', 'active', 'released')),
    reason_code        TEXT NOT NULL CHECK (btrim(reason_code) <> ''),
    policy_version     TEXT NOT NULL CHECK (btrim(policy_version) <> ''),
    actor_id           UUID NOT NULL,
    -- moderationcap.RestrictionClaims.Digest(): the claims with the time
    -- fields zeroed. It is what post-service compares a replay against and
    -- what a retry must reproduce byte for byte.
    claims_digest      BYTEA NOT NULL CHECK (octet_length(claims_digest) = 32),
    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'acked', 'superseded', 'parked')),
    attempts           INT NOT NULL DEFAULT 0,
    requeues           INT NOT NULL DEFAULT 0,
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    claimed_at         TIMESTAMPTZ,
    last_status_code   INT,
    last_error_code    TEXT,
    last_error         TEXT,
    acked_at           TIMESTAMPTZ,
    replayed           BOOLEAN,
    result             JSONB,
    parked_at          TIMESTAMPTZ,
    park_reason        TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    -- One command per case transition.
    CONSTRAINT restriction_commands_one_per_revision UNIQUE (case_id, case_revision),
    CONSTRAINT restriction_commands_terminal_shape CHECK (
        (status = 'pending' AND acked_at IS NULL AND parked_at IS NULL)
        OR (status = 'acked' AND acked_at IS NOT NULL AND parked_at IS NULL)
        OR (status = 'superseded' AND acked_at IS NULL)
        OR (status = 'parked' AND parked_at IS NOT NULL AND park_reason IS NOT NULL AND btrim(park_reason) <> ''))
);

CREATE INDEX IF NOT EXISTS idx_restriction_commands_pending
    ON trust.restriction_commands (next_attempt_at, created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_restriction_commands_case
    ON trust.restriction_commands (case_id, case_revision DESC);

-- The audit log may now name a copyright case (copyright_case.hold_placed /
-- copyright_case.hold_released). Guarded like 011's: replaced only when the
-- constraint is missing or does not yet admit 'copyright_case', so a later
-- widening survives a re-run of this file.
DO $$
DECLARE def TEXT;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conname = 'admin_audit_target_type_check' AND conrelid = 'trust.admin_audit'::regclass;
    IF def IS NULL OR def NOT LIKE '%copyright_case%' THEN
        ALTER TABLE trust.admin_audit DROP CONSTRAINT IF EXISTS admin_audit_target_type_check;
        ALTER TABLE trust.admin_audit ADD CONSTRAINT admin_audit_target_type_check
            CHECK (target_type IN ('report', 'appeal', 'grievance', 'strike', 'copyright_case'));
    END IF;
END $$;
