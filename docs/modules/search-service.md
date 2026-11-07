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

## Event consumers (hand-written; source: `cmd/server/main.go`, `internal/events`)

One dispatcher (`internal/events/consumer.go`) behind six Kafka consumers. Each
has its own group, so a stalled write on one topic never holds up another.

| Topic (env, default) | Group | Start offset | Off switch | Handles |
|---|---|---|---|---|
| `KAFKA_TOPIC`, `social.events.v1` | `search-service-group` | committed / first | none | posts, users, follows, hashtags, products, chat DLQ letters |
| `MEDIA_KAFKA_TOPIC`, `media.events` | `search-service-media-group` | committed / first | none | `MediaSubtitlesChanged` only |
| `CHANNEL_KAFKA_TOPIC`, `channel-events` | `search-service-channel-group` | **last** (new group) | `CHANNEL_KAFKA_TOPIC=-` | `channel.created/updated/deleted/subscribed/unsubscribed` |
| `COMMUNITY_KAFKA_TOPIC`, `community-events` | `search-service-community-group` | **last** (new group) | `COMMUNITY_KAFKA_TOPIC=-` | `community.created/updated/deleted/member_*` |
| `CHAT_KAFKA_TOPIC`, `chat.events.v1` | `search-service-chat-group` | committed / first | `CHAT_KAFKA_TOPIC=-` | chat messages into `chat_messages_v1` |
| `IDENTITY_KAFKA_TOPIC`, `identity.events.v1` | `search-service-identity-group` | committed / first | none | profile, privacy, account lifecycle (hide / purge) |

- Start offset applies only to a group with no committed offset. The channel
  and community groups start at the end of the topic on purpose: their history
  is old id-only / name-and-type-only events that would replay over the
  backfill's documents. The backfill (below) covers everything before the
  first deploy.
- Failures retry, then go to the shared DLQ `SEARCH_DLQ_TOPIC`
  (`search.events.v1.dlq`; `-` disables). The DLQ replayer re-applies dead
  letters through the social consumer's dispatcher, so a channel or community
  event can be applied minutes or hours late. That is one reason the snapshot
  order below exists.

## Snapshot order: communities_v1 and channels_v1 (2026-11-08)

community-service and channel-service publish every change as the whole row
(`CommunitySnapshot` / `ChannelSnapshot` on `*.created` / `*.updated`). They
key by entity id, but each publish runs in its own goroutine and dead letters
replay late, so snapshots arrive out of order. Applied blindly, a stale
"active" snapshot after a newer "suspended" one re-lists the entity.

Every write is one scripted `_update` (`internal/store/search/entity_state.go`)
that decides inside OpenSearch, under the document lock, whether it wins:

- **Version** = the snapshot's own `updated_at` (the row's, not the envelope's)
  in microseconds, stored as `source_updated_us` (long). Zero = unversioned
  (events from before 2026-11-08, legacy `*.created` without the row,
  `community.updated` without the row, documents written before this change).
- **Searchable** (status `active` and a public type): written when newer than
  the stored version, or equal over a live document (redelivery). Fields are
  merged into a live document (`engagement_score` is left to the member /
  subscriber events); a missing document or a tombstone becomes the whole
  document.
- **Not searchable** (suspended, archived, private or another non-public type):
  replaced by a **tombstone** `{<id>, removed: true, status: "removed",
  source_updated_us}` unless the stored document is newer. A removal wins ties.
  A removal with no stored document still writes the tombstone, so a later
  stale "active" finds it.
- **Deleted** (`*.deleted`, or a snapshot with status `deleted`): a terminal
  tombstone (`deleted: true`) that nothing writes over again. Neither service
  can restore a deleted row, and the delete events carry no row version.
- **Unversioned** writes keep the old behaviour among themselves, but never
  overwrite a versioned document. An unversioned removal still removes (fail
  closed) and keeps the stored version.
- `community.updated` without the row is a partial update that lands only on a
  live, unversioned document and never creates one.

Why a tombstone and not a delete: a deleted document has no version left to
compare, so a stale "active" after it would recreate the entity. Tombstones
hold no name, handle, description, owner or counts. Every query over these
indices filters on `status = active` and a public type (`buildFunctionScoreQuery`,
`autocompleteCommunities`), which a tombstone never has. They carry no
`owner_id`, so owner erasure (`DeleteOwnerEntityDocs`) leaves them and the
ordering survives it.

Engagement events (`AddToEngagementScore`, all four indices) update an existing
document only. They used to upsert a stub holding only `engagement_score` for
any entity without a document, private ones included. A tombstone keeps no score.

## Deploy-time backfills (`cmd/backfill`)

Run once after the deploy that ships the consumers, and again whenever the index
may have missed events (past retention, a consumer switched off):

```
go run ./cmd/backfill -entity communities,channels,engagement-stubs
```

- `communities`, `channels`: every row, deleted ones included, with its status
  and `updated_at`, through the same ordered write. Active public rows are
  written, others become tombstones, deleted rows terminal tombstones. An event
  newer than the read wins over it, so this is safe on a live index.
- `engagement-stubs`: `_delete_by_query` of documents without their id field
  (`post_id`, `user_id`, `community_id`, `channel_id`) in the four indices: the
  stubs left by the old engagement upsert. Idempotent. It reports and fails on
  version conflicts; re-run it.
- `-dry-run` reads the rows and writes nothing.
