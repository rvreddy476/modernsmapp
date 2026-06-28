# Module: search-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /history
DELETE /saved/:id
GET /autocomplete
GET /events
GET /hashtags
GET /history
GET /messages
GET /posts
GET /products
GET /saved
GET /suggested
GET /trending
GET /users
POST /click
POST /internal/reindex/users
POST /saved
POST /users/bulk-sync
GROUP /v1/discover
GROUP /v1/search
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS search.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS search.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS saved_searches (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    query       TEXT NOT NULL,
    search_type TEXT NOT NULL DEFAULT 'universal',
    saved_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS search_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    query       TEXT NOT NULL,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS search_queries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    viewer_id     UUID,
    query         TEXT NOT NULL,
    types         TEXT[] NOT NULL,
    result_counts JSONB NOT NULL,   -- {"posts": 23, "users": 4, ...}
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS search_clicks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query_id    UUID NOT NULL,
    viewer_id   UUID,
    entity_type TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    position    INT  NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```
