# Module: reviewer-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /admin/escalations
GET /admin/stats
GET /assignments/next
GET /content/:contentId/feedback
GET /me
GET /me/stats
GET /queue
POST /admin/escalations/:id/decision
POST /assignments/:id/decision
POST /assignments/:id/heartbeat
POST /internal/enqueue
POST /online
POST /opt-in
POST /verify-kyc
GROUP /v1/reviewer
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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

CREATE TABLE IF NOT EXISTS reviewer.review_queue (
    content_id      UUID PRIMARY KEY,
    creator_id      UUID NOT NULL,
    content_type    TEXT NOT NULL DEFAULT '',
    languages       TEXT[] NOT NULL DEFAULT '{}',
    content_seconds INT NOT NULL DEFAULT 0,
    claimed         BOOLEAN NOT NULL DEFAULT FALSE,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

CREATE TABLE IF NOT EXISTS reviewer.content_review_outcome (
    content_id          UUID PRIMARY KEY,
    test_audience_size  INT,
    engagement_pctile   NUMERIC(5,4),
    safety_confirmed    BOOLEAN,
    finalized_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS reviewer.reviewer_ledger (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id   UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id UUID REFERENCES reviewer.review_assignments(id),
    entry_type    TEXT NOT NULL,        -- base | bonus | penalty | clawback
    amount_minor  BIGINT NOT NULL,      -- paise; signed
    period        DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

CREATE TABLE IF NOT EXISTS reviewer.reviewer_flags (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id   UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id UUID REFERENCES reviewer.review_assignments(id),
    flag_type     TEXT NOT NULL,   -- audit_mismatch | shadow_mismatch | anomaly_rubberstamp | anomaly_approval_rate | ring_suspect
    severity      INT NOT NULL DEFAULT 1,
    details       TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reviewer.reviewer_flags (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id   UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id UUID REFERENCES reviewer.review_assignments(id),
    flag_type     TEXT NOT NULL,
    severity      INT NOT NULL DEFAULT 1,
    details       TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reviewer.escalations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_id        UUID NOT NULL,
    creator_id        UUID NOT NULL,
    reviewer_id       UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id     UUID REFERENCES reviewer.review_assignments(id),
    reviewer_comments TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'open',
    admin_decision    TEXT,
    admin_notes       TEXT,
    resolved_by       UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at       TIMESTAMPTZ,
    CONSTRAINT escalations_status_chk CHECK (status IN ('open','resolved')),
    CONSTRAINT escalations_decision_chk CHECK (admin_decision IS NULL OR admin_decision IN ('reject','request_edits','approve'))
);

```

## API types (request/response Go structs with JSON tags)
```go
type optInRequest struct {
	Languages []string `json:"languages"`
	Region    string   `json:"region"`
}

type onlineRequest struct {
	Online bool `json:"online"`
}

type heartbeatRequest struct {
	Seconds int `json:"seconds"`
}

type decideRequest struct {
	Decision string `json:"decision" binding:"required"` // approve | escalate
	Comments string `json:"comments"`                    // required when escalating
}

type resolveEscalationRequest struct {
	Decision string `json:"decision" binding:"required"` // reject | request_edits | approve
	Notes    string `json:"notes"`
}

type enqueueRequest struct {
	ContentID      string   `json:"content_id" binding:"required"`
	CreatorID      string   `json:"creator_id" binding:"required"`
	ContentType    string   `json:"content_type"`
	Languages      []string `json:"languages"`
	ContentSeconds int      `json:"content_seconds"`
	SpamScore      float64  `json:"spam_score"`
}
```
