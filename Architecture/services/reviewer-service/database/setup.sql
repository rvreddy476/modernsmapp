-- Database setup for Architecture/services/reviewer-service
-- Human review layer for PostTube (long-form) + Flicks/Reels (short-form).

CREATE SCHEMA IF NOT EXISTS reviewer;

-- A user who has opted in to review work.
CREATE TABLE IF NOT EXISTS reviewer.reviewers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL UNIQUE,
    status             TEXT NOT NULL DEFAULT 'probation',  -- probation | active | suspended
    tier               TEXT NOT NULL DEFAULT 'probation',  -- probation | trusted | senior
    languages          TEXT[] NOT NULL DEFAULT '{}',       -- ISO codes, used for routing
    region             TEXT NOT NULL DEFAULT '',
    reviewer_accuracy  NUMERIC(5,4) NOT NULL DEFAULT 0.5000, -- EWMA of correctness, 0..1
    max_concurrent     SMALLINT NOT NULL DEFAULT 1,
    kyc_verified       BOOLEAN NOT NULL DEFAULT FALSE,
    is_online          BOOLEAN NOT NULL DEFAULT FALSE,
    device_hash        TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT reviewers_status_chk CHECK (status IN ('probation','active','suspended')),
    CONSTRAINT reviewers_tier_chk   CHECK (tier IN ('probation','trusted','senior'))
);

-- Content awaiting human review (sourced from post-service flagged/ambiguous content).
CREATE TABLE IF NOT EXISTS reviewer.review_queue (
    content_id      UUID PRIMARY KEY,
    creator_id      UUID NOT NULL,
    content_type    TEXT NOT NULL DEFAULT '',
    languages       TEXT[] NOT NULL DEFAULT '{}',
    content_seconds INT NOT NULL DEFAULT 0,
    claimed         BOOLEAN NOT NULL DEFAULT FALSE,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_review_queue_unclaimed
    ON reviewer.review_queue (enqueued_at) WHERE claimed = FALSE;

-- One row per (content, reviewer) attempt. Only ONE active row per content.
CREATE TABLE IF NOT EXISTS reviewer.review_assignments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_id      UUID NOT NULL,
    creator_id      UUID NOT NULL,
    reviewer_id     UUID NOT NULL REFERENCES reviewer.reviewers(id),
    kind            TEXT NOT NULL DEFAULT 'primary',   -- primary | audit | shadow
    status          TEXT NOT NULL DEFAULT 'assigned',  -- assigned | in_progress | completed | expired | reassigned
    decision        TEXT,                              -- approve | reject | flag_unsafe
    decision_reason TEXT,
    content_seconds INT NOT NULL DEFAULT 0,
    watched_seconds INT NOT NULL DEFAULT 0,            -- heartbeat-verified, capped
    base_paid       BOOLEAN NOT NULL DEFAULT FALSE,
    graded          BOOLEAN NOT NULL DEFAULT FALSE,    -- Phase 2 backtest done
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    decided_at      TIMESTAMPTZ,
    CONSTRAINT assignments_status_chk CHECK (status IN ('assigned','in_progress','completed','expired','reassigned')),
    -- Reviewer decisions: a reviewer either APPROVEs (publish) or ESCALATEs to a
    -- super-admin with comments. (reject/flag_unsafe kept for backward compat.)
    CONSTRAINT assignments_decision_chk CHECK (decision IS NULL OR decision IN ('approve','escalate','reject','flag_unsafe'))
);

-- Enforce a single active PRIMARY reviewer per content. Scoped to kind='primary'
-- so Phase 3 audit/shadow second-reviews can run concurrently on the same content.
CREATE UNIQUE INDEX IF NOT EXISTS one_active_review
    ON reviewer.review_assignments (content_id)
    WHERE status IN ('assigned','in_progress') AND kind = 'primary';

CREATE INDEX IF NOT EXISTS idx_assignments_reviewer
    ON reviewer.review_assignments (reviewer_id, status);
CREATE INDEX IF NOT EXISTS idx_assignments_expiry
    ON reviewer.review_assignments (expires_at) WHERE status IN ('assigned','in_progress');

-- Engagement-based ground truth, filled after test-audience exposure (Phase 2+).
CREATE TABLE IF NOT EXISTS reviewer.content_review_outcome (
    content_id          UUID PRIMARY KEY,
    test_audience_size  INT,
    engagement_pctile   NUMERIC(5,4),
    safety_confirmed    BOOLEAN,
    finalized_at        TIMESTAMPTZ
);

-- Append-only accrual of base pay (paise). Phase 2 settles these into the
-- monetization ledger via the payout path; Phase 1 only accrues + credits.
CREATE TABLE IF NOT EXISTS reviewer.reviewer_ledger (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id   UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id UUID REFERENCES reviewer.review_assignments(id),
    entry_type    TEXT NOT NULL,        -- base | bonus | penalty | clawback
    amount_minor  BIGINT NOT NULL,      -- paise; signed
    period        DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_reviewer_ledger_reviewer
    ON reviewer.reviewer_ledger (reviewer_id, period DESC);

-- Escalations: when a reviewer can't approve, they escalate to a super-admin
-- with comments. The super-admin decides reject | request_edits | approve.
CREATE TABLE IF NOT EXISTS reviewer.escalations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_id        UUID NOT NULL,
    creator_id        UUID NOT NULL,
    reviewer_id       UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id     UUID REFERENCES reviewer.review_assignments(id),
    reviewer_comments TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'open',  -- open | resolved
    admin_decision    TEXT,                          -- reject | request_edits | approve
    admin_notes       TEXT,                          -- what to remove / why
    resolved_by       UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at       TIMESTAMPTZ,
    CONSTRAINT escalations_status_chk CHECK (status IN ('open','resolved')),
    CONSTRAINT escalations_decision_chk CHECK (admin_decision IS NULL OR admin_decision IN ('reject','request_edits','approve'))
);
CREATE INDEX IF NOT EXISTS idx_escalations_open
    ON reviewer.escalations (created_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_escalations_creator
    ON reviewer.escalations (creator_id, created_at DESC);

-- Phase 3 integrity: every audit/shadow mismatch, behavioural anomaly, and
-- ring-detection hit is recorded here. Drives clawbacks + auto-suspension.
CREATE TABLE IF NOT EXISTS reviewer.reviewer_flags (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id   UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id UUID REFERENCES reviewer.review_assignments(id),
    flag_type     TEXT NOT NULL,   -- audit_mismatch | shadow_mismatch | anomaly_rubberstamp | anomaly_approval_rate | ring_suspect
    severity      INT NOT NULL DEFAULT 1,
    details       TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_reviewer_flags_reviewer
    ON reviewer.reviewer_flags (reviewer_id, created_at DESC);
-- Idempotency guard: at most one flag of a type per assignment (anomaly/audit re-runs).
CREATE UNIQUE INDEX IF NOT EXISTS uniq_flag_assignment_type
    ON reviewer.reviewer_flags (assignment_id, flag_type)
    WHERE assignment_id IS NOT NULL;
