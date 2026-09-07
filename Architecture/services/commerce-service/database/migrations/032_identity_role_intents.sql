-- 032 — the durable queue that tells identity somebody became a seller.
--
-- identity-auth-service owns ecosystem roles for the whole platform. commerce
-- must tell it when a `sellers` row appears or is terminally rejected. That is
-- an HTTP call to another service from inside an approval transaction, so it
-- cannot be made inline: an identity outage would either fail the approval or
-- silently lose the role. Instead the intent is written HERE, in commerce's
-- own database, in the same transaction as the seller row, and a worker
-- delivers it (shared/identityroles).
--
-- The DDL below is a copy of shared/identityroles.SchemaSQL(""). It is a copy
-- because a .sql file is the one thing Go cannot import, exactly as
-- auth-service's user_roles CHECK constraint is. TestMigrationMatchesSharedSchema
-- in internal/store/postgres asserts the two have not drifted.

CREATE TABLE IF NOT EXISTS identity_role_intents (
    id               BIGSERIAL PRIMARY KEY,
    op               TEXT        NOT NULL CHECK (op IN ('grant','revoke')),
    user_id          UUID        NOT NULL,
    role_name        TEXT        NOT NULL,
    service          TEXT        NOT NULL,
    reason           TEXT        NOT NULL DEFAULT '',
    attempts         INT         NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at     TIMESTAMPTZ,
    dead_lettered_at TIMESTAMPTZ
);

-- The worker's only query. Partial so the index stays the size of the backlog
-- rather than the size of history.
CREATE INDEX IF NOT EXISTS idx_identity_role_intents_pending
    ON identity_role_intents (next_attempt_at)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;

-- Operator query: "what is stuck and why".
CREATE INDEX IF NOT EXISTS idx_identity_role_intents_dead
    ON identity_role_intents (dead_lettered_at)
    WHERE dead_lettered_at IS NOT NULL;
