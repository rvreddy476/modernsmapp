# Comment counts and live threads — backend handoff, 26 September 2026

Follow-up to Codex's "Reels design and live comments" report. Scope: post-service and chat ws-gateway only. **Uncommitted** on `codex/module-01-02-launch-safety`; deployed and verified on dev (post-service `0f09d4dd1073`, chat-ws-gateway `d6c6d14ee1e0`).

## 1. The counter mismatch, and the one definition that replaces it

**What was wrong.** `comment_count` lived in three stores that disagreed:

| Store | Written by | Read by |
|---|---|---|
| Scylla `post_counters.comment_count` | nothing on any live route | **every** read (`GET /v1/posts/:id`, lists, batch) → always 0 |
| Redis `post:eng` hash | `CreateCommentPG` (`HIncrBy`) | nothing |
| PG `post_engagement_counts.comment_count` | request path **and** `PGCounterConsumer` (Kafka) → +2 per comment; replies never counted; hidden comments stayed counted | nothing |

So the dev reel `7b1d256c-…` showed a comment in the list while detail said zero.

**Definition now, and the only one:**

```
comment_count(post) = COUNT(*) FROM comments
                      WHERE post_id = post AND is_deleted = FALSE
                        AND moderation_status = 'visible'
```

Replies count (they render inline). Held / hidden / removed comments do not (the list endpoint does not show them). Stored only in `post_engagement_counts.comment_count`.

**One writer.** The request path moves it by ±1 exactly once per transition into or out of the visible set, via `pgStore.AdjustCommentCount`:

| Mutation | Delta | Notes |
|---|---|---|
| create (`POST /v1/posts/:id/comments`) | +1 | not on an idempotency replay |
| reply (`POST /v1/comments/:id/reply`) | +1 | post-owner-only, unchanged |
| delete (`DELETE /v1/comments/:id`) | −1 **only if the comment was visible** | store returns `counted`; a second delete is 404 and moves nothing |
| moderation `visible → hidden/removed` | −1 | `SetCommentModerationStatus` returns the previous status |
| moderation `hidden/removed → visible` | +1 | |
| auto-review flip on the 3rd report (`visible → review`) | −1 | `IncrementCommentFlaggedCount` returns `flipped` once |
| edit, like, dislike | 0 | announce only |

`PGCounterConsumer` no longer touches comment events (`case EventCommentCreated, EventCommentDeleted: return nil`). The sharded `post_comment_count` counter is no longer constructed, so its flush cannot overwrite the column. The two orphaned `counter:post_comment_count:*` keys on dev Redis were deleted.

**Reads.** `countsForPost` / `overlayCommentCounts` merge Scylla likes+shares with the PG comment count for detail, lists, batch hydration (what the feed uses), my-uploads, and the ws broadcaster. The old direct `scyllaStore.GetCounts` reads are gone (a test forbids them).

**Safety net.** `RecountCommentCounts` rewrites any stored count that disagrees with the definition (including back to zero) and returns rows corrected. It runs 20 s after boot and hourly. Dev boot: `corrected_rows: 0` after the earlier manual repair.

## 2. Title and author enrichment

Detail (`GET /v1/posts/:id`) now returns `author {id, display_name, username, avatar_media_id, avatar_url}` via the same identity-profile batch the comment list uses, so the reel page and the feed card show the same person. **Title is untouched**: a post has a `title` column only when one was set; nothing manufactures one from the description. The reel above answers `counts.comments = 2` and an `author` object.

## 3. Live thread delivery

### Routes added

- `GET /v1/internal/posts/:id/visibility?viewer_id=<uuid>` (post-service, `X-Internal-Service-Key`). Returns `{"visible": true|false}`; 400 on a malformed id, 503 when the decision cannot be made. Same rule as `GET /v1/posts/:id` for that viewer: review status, scheduled/hidden, visibility, private accounts, blocks.

### ws-gateway post rooms (`WS_POST_ROOMS_ENABLED=true`, `POST_SERVICE_URL`, `INTERNAL_SERVICE_KEY` in compose)

Client → gateway, after connecting to `/v1/ws/connect`:

```json
{"type":"subscribe_post","post_id":"<uuid>"}
{"type":"unsubscribe_post","post_id":"<uuid>"}
```

Before joining `post:<id>` the gateway asks the visibility route. Only a plain `{"visible":true}` admits; `false`, a non-200, a timeout or a malformed id refuse. **A refusal is silent on the socket** (logged server-side as `post room subscribe refused`); the socket stays open. The grant is cached **per connection** for 5 minutes, so the client's 30 s re-subscribe is free; there is no server-side room memory across reconnects — **after a reconnect the client must send `subscribe_post` again.**

Gateway → client while subscribed (both are relayed from Redis `post:<id>`):

```json
{"type":"comment_change","payload":{
  "event_id":"dc6be7cf-…",          // uuid, unique per change → dedupe key
  "version":1790418995849403,        // unix micros at publish → order/invalidate
  "post_id":"…","comment_id":"…",
  "parent_id":"…",                   // only on change = "replied"
  "change":"created|replied|edited|deleted|reaction|moderated",
  "actor_id":"…",
  "comments":1                       // authoritative count after the change
}}
```

```json
{"type":"post_update","payload":{"post_id":"…","update_type":"comment|like|…",
  "actor_id":"…","comment_id":"…","likes":0,"comments":1,"shares":0}}
```

`post_update` is the older Kafka-driven count frame (fires on create only, tens of ms later); `comments` in it is now read from PG so the two frames agree. **No frame ever carries a comment body or text** — subscribers re-read the thread through the authorized list endpoint. The gateway drops a frame whose `payload.actor_id` (or `author_id`) equals the receiving user, so a client never hears its own mutation twice; it must apply its own change locally.

`group_post_typing` relay now emits only `{"type":"group_post_typing","post_id":"…"}` — no user id, no name — so anonymous group posts cannot leak the author through typing. Group-post rooms themselves are still behind the beta gate and unchanged.

### Guarantees and limits (honest)

- **At-most-once.** Redis pub/sub: a subscriber that is disconnected at publish time never receives the frame. Reconnect = resubscribe + refetch the thread; keep the client's 15/30 s refresh.
- **Ordering** is publish order per post; use `version` to discard stale frames, `event_id` to dedupe.
- **Edited** frames say *that* a comment changed, not what it now says. Refetch it.
- **Moderated** frames fire only to viewers of the parent post; a held comment's body is never on the wire.
- **Likes and shares** still come from Scylla and were not part of this task; only the comment count is reconciled.
- The `EditComment` store update was never a realtime event before this change; it is one now only because the service publishes `comment_change/edited` after the update commits.

## 4. Proof

**Unit** (`go test ./...` both services, green): source guards that every mutation adjusts once and publishes, no shard constructed, no direct Scylla count reads, the consumer skips comment events, the frame shape (uuid `event_id`, no `body`, `parent_id` only on replies); ws-gateway: authority yes/no/error/disabled/malformed, grant cache/expiry/revoke, gate, HTTP authorizer, typing frame carries no identity.

**Integration** (`post_it_test`, `go test -tags integration ./internal/store/postgres/ -run CommentCount`): the stored count equals the definition after every transition in §1 including a replayed create, a delete of a held reply (no change), a retried delete (refused); the recount repairs a doubled row and zeroes a post whose comments are gone, and a second recount changes nothing. Mutation checks: delete-always-counted, flag-never-flips, recount-never-zeroes, recount-counts-hidden each fail the named test.

**Live, two independent viewers on dev** (`scratchpad/probe-comments-live.ts`, disposable post created and deleted by the probe): 21/21 —

| Step | Detail count = definition | Other viewer hears |
|---|---|---|
| B creates; retries same Idempotency-Key | 1 (same comment id, not 2) | A: `created`, `comments=1`; B hears nothing (self-echo) |
| A replies | 2 | B: `replied` with `parent_id` |
| B edits | 2 | A: `edited` |
| A likes B's comment | 2 | B: `reaction` |
| B disconnects, reconnects, resubscribes | — | — |
| A deletes reply | 1 | B (new socket): `deleted` |
| B deletes comment; retries | 0 (retry 404) | A: `deleted` |
| B subscribes to an unknown post | — | refused silently, socket open |
| all frames | — | 6 unique `event_id`s, numeric `version`, no `body`/`text` |

## 5. Files (uncommitted)

post-service: `internal/store/postgres/comment_counts.go` (new), `comments.go`, `internal/service/comment_counts.go` (new), `post.go`, `my_uploads.go`, `reports.go`, `internal/engagement/consumers/pg_counter.go`, `ws_broadcaster.go`, `internal/reconcile/counts.go`, `internal/http/handler.go`, `handler_post_visibility.go` (new), `cmd/server/main.go`, tests `internal/service/comment_counts_test.go`, `internal/store/postgres/comment_counts_integration_test.go`.
ws-gateway: `internal/http/postrooms.go` (new), `server.go`, `internal/config/config.go`, `cmd/server/main.go`, `internal/http/postrooms_test.go`.
`Architecture/docker/docker-compose.yml`: three env vars on chat-ws-gateway. Prod/staging values files still need `WS_POST_ROOMS_ENABLED`, `POST_SERVICE_URL`, `INTERNAL_SERVICE_KEY` on the gateway before this is live there.

## 6. For Codex (web)

- Reels/detail: read `counts.comments` and `author` from detail as-is; stop guessing from the first comment page.
- After `connect`: send `subscribe_post`; on every reconnect send it again; on `comment_change` dedupe by `event_id`, drop if `version` ≤ last seen for that post, set the count from `comments`, and refetch the thread (or the one comment) for `edited`/`created`/`replied`. Apply your own mutations locally; you will not be echoed.
- Anonymous group posts: `group_post_typing` no longer carries identity, so the typing indicator can be re-enabled there; live comment updates for group posts still wait on the group-post room gate.
