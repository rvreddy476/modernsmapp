# Module: feature-flag-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /flags/me
GET /:key/audit
GET /:key/results
POST /flags/conversions
GROUP /admin/flags
GROUP /v1
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS flags.flags (
    key TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    rollout_pct INT NOT NULL DEFAULT 0,
    target_user_ids TEXT[], -- UUIDs as strings
    payload JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS flags.flag_audit_log (
    id         BIGSERIAL PRIMARY KEY,
    flag_key   TEXT NOT NULL,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL CHECK (action IN ('created', 'updated', 'deleted')),
    old_value  JSONB,
    new_value  JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS flags.experiment_conversions (
    id          BIGSERIAL PRIMARY KEY,
    flag_key    TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    variant     TEXT NOT NULL CHECK (variant IN ('control', 'treatment')),
    event_type  TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```
