# Module: suggestion-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /batch
GET /hubs
GET /interstitial
GET /people
POST /action
POST /impression
GROUP /v1/suggestions
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS suggestion_candidates (
    viewer_id        UUID NOT NULL,
    candidate_id     UUID NOT NULL,
    suggestion_type  VARCHAR(10) NOT NULL DEFAULT 'friend',
    base_score       REAL NOT NULL DEFAULT 0,
    reason_codes     TEXT[] NOT NULL DEFAULT '{}',
    explain_text     VARCHAR(200) NOT NULL DEFAULT '',
    source_bucket    VARCHAR(20) NOT NULL DEFAULT 'fof',
    mutual_friend_count SMALLINT DEFAULT 0,
    impression_count SMALLINT DEFAULT 0,
    generated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ,
    PRIMARY KEY (viewer_id, candidate_id, suggestion_type)
);

CREATE TABLE IF NOT EXISTS suggestion_impressions (
    id               UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    viewer_id        UUID NOT NULL,
    candidate_id     UUID NOT NULL,
    surface          VARCHAR(20) NOT NULL DEFAULT 'mycircle',
    suggestion_type  VARCHAR(10) NOT NULL DEFAULT 'friend',
    rank_position    SMALLINT,
    score            REAL,
    experiment_id    VARCHAR(50),
    variant_id       VARCHAR(20),
    shown_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    session_id       UUID
);

CREATE TABLE IF NOT EXISTS suggestion_actions (
    id               UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    viewer_id        UUID NOT NULL,
    candidate_id     UUID NOT NULL,
    action           VARCHAR(20) NOT NULL,
    surface          VARCHAR(20) NOT NULL DEFAULT 'mycircle',
    suggestion_type  VARCHAR(10) NOT NULL DEFAULT 'friend',
    experiment_id    VARCHAR(50),
    variant_id       VARCHAR(20),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS suggestion_cooldowns (
    viewer_id        UUID NOT NULL,
    candidate_id     UUID NOT NULL,
    cooldown_type    VARCHAR(20) NOT NULL,
    cooldown_until   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (viewer_id, candidate_id)
);

CREATE TABLE IF NOT EXISTS suggestion_dismiss_patterns (
    viewer_id       UUID NOT NULL,
    signal_type     VARCHAR(50) NOT NULL,
    dismiss_count   SMALLINT DEFAULT 1,
    penalty_weight  REAL DEFAULT 0.8,
    last_dismissed  TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (viewer_id, signal_type)
);

```
