-- 007_grievances.sql: IT Rules 2021 grievance redressal mechanism.
-- Any user may lodge a grievance; the grievance officer acknowledges it
-- and resolves it. due_at is the resolution deadline (created + 15 days)
-- the SLA is measured against.
CREATE TABLE IF NOT EXISTS trust.grievances (
    id                UUID PRIMARY KEY,
    complainant_id    UUID NOT NULL,
    subject           TEXT NOT NULL CHECK (subject IN
                          ('content_complaint', 'privacy', 'account',
                           'intellectual_property', 'other')),
    about_entity_type TEXT,
    about_entity_id   UUID,
    description       TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'open' CHECK (status IN
                          ('open', 'acknowledged', 'resolved', 'rejected')),
    assigned_to       UUID,
    resolution_notes  TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acknowledged_at   TIMESTAMPTZ,
    resolved_at       TIMESTAMPTZ,
    due_at            TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_grievances_complainant
    ON trust.grievances (complainant_id, created_at DESC);

-- Partial index for the officer's open-queue, ordered by SLA deadline.
CREATE INDEX IF NOT EXISTS idx_grievances_open_queue
    ON trust.grievances (status, due_at)
    WHERE status IN ('open', 'acknowledged');
