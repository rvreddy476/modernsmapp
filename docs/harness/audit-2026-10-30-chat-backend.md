# Audit: chat backend (message-service, ws-gateway, call-service)

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits except this file, no git writes, no databases, no deploys.
Paths are relative to `chat-service/services/` unless they start with `Architecture/` or `chat-service/shared/`.

Covered: `message-service` (HTTP handlers, send pipeline, idempotency, delivery intents, outbox relay, scheduled-message worker, receipts, typing, presence, groups, invite links, pins/stars/threads, forward, unsend, media access, migrations 001-018, Scylla message store), `ws-gateway` (auth, room subscriptions, signaling relay, Redis fan-out, typing queue, presence fan-out), `call-service` (routes, join, links, summaries, reminders, signaling pair state), `chat-service/shared/callauth`, the api-gateway routes `/v1/chat`, `/v1/ws`, `/v1/calls` (`Architecture/services/api-gateway/cmd/server/main.go:666-672`, `pkg/internalroutes/internalroutes.go:319-327`), and the chat-events consumer in `Architecture/services/notification-service/internal/events/chat_consumer.go`.

Every finding below was traced from a client-reachable route or a running worker. Checked and found sound (not findings): membership checks on send/read/edit/delete/react/pins/threads/settings/entitlements (all `left_at IS NULL`); group role ladder and the 1024 cap (enforced under a row lock in `AddMemberCapped`); history cut-off at join for groups; roster block checks on add, create and invite-link join; personal-channel fan-out uses only active members; entitlement tokens with generation + deny markers and a 30 s reconcile; direct call signaling gated by call-service pair state; gateway never stamps the internal key on `/v1/chat`, `/v1/ws`, `/v1/calls` and strips a client-sent one; GIF URLs restricted to https Giphy; chat media access is membership-scoped through media-service.

`scripts/verify/go.sh --fast chat-service` fails with `not-a-module` (chat-service is a go.work of four modules). Run per module instead:

```
PASS build          message-service  (22s)
SKIP vet/test       message-service  (no changed package)
PASS build          ws-gateway  (3s)
SKIP vet/test       ws-gateway  (no changed package)
PASS build          call-service  (25s)
SKIP vet/test       call-service  (no changed package)
PASS build          shared  (0s)
SKIP vet/test       shared  (no changed package)
RESULT PASS  (logs: /tmp/verify-logs/20261030T172624-16724)
```

---

## HIGH

### H1. Any signed-in user can write or overwrite the "AI summary" of any call
- **Files:**
  - `call-service/internal/http/handler.go:76-77`: `POST /v1/calls/:callId/summary` is registered on the user-facing group beside the GET. The gateway proxies `/v1/calls` to call-service with the user's JWT only.
  - `call-service/internal/http/call_extras_handler.go:219-276` (`CreateCallSummary`): reads the user id and discards it (`_, ok := getUserID(...)`). It accepts `summary_text`, `transcript_url`, `key_points`, `action_items` and `participants` from the body.
  - `call-service/internal/service/call_extras.go:203-212`: checks only that the call exists, then `UpsertCallSummary`. There is no participant check and no internal-key check, although the comment says "called by an AI pipeline".
  - `GetCallSummary` (`call_extras.go:175-200`) shows the stored row to every participant.
- **Scenario:** A had a call with B, so A knows the call id (it is in A's call history). A posts `{"summary_text":"Agreed: B sends 20,000 to ...","transcript_url":"https://evil.example/login"}` to `/v1/calls/<id>/summary`. B opens the call's summary and sees A's text and link presented as the platform's AI summary. A later real pipeline write is overwritten by the next forged upsert. Anyone else who learns a call id (a removed participant, or someone shown a screenshot) can do the same.
- **Fix:** Move the POST to an internal group (`/internal/v1/calls/:callId/summary`) gated by `X-Internal-Service-Key`, the same way message-service's `/internal/v1/chat` works, and delete the user-facing POST route. Add a guard test showing a JWT-only POST is refused with 404 or 401.

### H2. Editing a message bypasses the message-request rules (no links, text only, 500 characters) and every other send gate
- **Files:**
  - `message-service/internal/service/message.go:1226-1290`: in a pending request, the sender's single first message must be plain text, contain no link (`containsLink`) and be at most 500 runes.
  - `message.go:1607-1700` (`EditMessage`): checks only membership and authorship (`:1640`). It has no `IsRequest` check, no `containsLink`, no length cap (the comment at `:1618` says so explicitly), and no chat-pause, dating-closed (`ClosedAt`) or first-move check. It then rewrites the stored text (`:1650`), refreshes the inbox preview through `SetLastMessage`, and fans out `message_edited` with the new text.
- **Scenario:** A stranger opens a request to B with "hi" (allowed). The stranger then sends `PATCH /v1/chat/conversations/<id>/messages/<msg>` with `{"text":"Your account is locked, verify at https://phish.example ...","ts":...}`. B's Requests lane, the conversation preview and the open request all show the link, which is exactly what the request screen exists to stop. The same edit works after a dating match closes, while the sender's chat is paused, and with text up to the gateway's 10 MB body cap (see H4).
- **Fix:** Have `EditMessage` run the send gates that apply to content. Refuse while `conv.IsRequest` (or apply the first-message rules: no link, at most 500 runes), refuse on `ErrMatchClosed` and on chat pause, and cap `newText` at `MessageTextMaxRunes` (2000), the same as send. Put a size cap on `EditMessageRequest.Text` too (`binding:"required,max=2000"`, `handler.go:888-893`).

### H3. The outbox relay sends about one event per second and publishes the same rows from every replica
- **Files:**
  - `message-service/internal/events/producer.go:22-31`: a synchronous `kafka.Writer` with the default `BatchTimeout` (1 s in kafka-go v0.4.50, `writer.go:825-830`). A sync `WriteMessages` of a single message waits for the batch timer, so each call takes about 1 s.
  - `message-service/internal/service/message.go:1957-1991` (`processOutbox`): publishes the batch one event at a time, one `PublishRaw` per row.
  - `message-service/internal/store/postgres/conversations.go:860-861`: `SELECT ... WHERE published_at IS NULL ORDER BY id LIMIT 50` with no `FOR UPDATE SKIP LOCKED` or lease.
  - `deploy/services/chat-message-service/values-prod.yaml:16-20`: `replicaCount: 2`, HPA 2-6, so every replica runs the relay.
  - `producer.go:46-62` (`PublishRaw`): the partition key is `""` and every publish mints a new `EventID`, so consumers cannot de-duplicate a re-publish.
- **Scenario:** Every send queues `MessageCreated` (plus reaction, edit, request and governance events). Above about 1 event/s across the platform the backlog only grows, and push notifications, search indexing and trust-safety reports fall behind by minutes to hours. Meanwhile replica 2 polls (every 1 s) while replica 1 is still blocked on its first 1-second write, reads the same 50 rows, and publishes them again. `notification-service` then creates a second notification and push for the same message (`chat_consumer.go:212-290` has no de-duplication). With the empty key, `MessageCreated`, `MessageEdited` and `MessageDeleted` for one message can land on different partitions, so search can apply the delete before the create and resurrect an unsent message.
- **Fix:** Claim rows with `UPDATE ... SET claimed_until = now()+30s WHERE id IN (SELECT id ... FOR UPDATE SKIP LOCKED LIMIT 50) RETURNING ...`. Publish the whole batch in one `WriteMessages` call, or set `BatchTimeout: 10*time.Millisecond`. Key messages by `conversation_id`, and derive `EventID` from the outbox row (`dedupe_key` or `id`) so consumers can de-duplicate.

### H4. Message text has no length cap on edit or on scheduled messages; oversized rows then block the outbox for everyone
- **Files:**
  - `message-service/internal/http/messenger_extras_handler.go:615-620`: `CreateScheduledMessageRequest.Content` has no `max`. `service/messenger_extras.go:488-509` stores it as is.
  - `service/scheduler.go:26-42`: sends it later through `SendMessage`. `validateMessageContent` (`service/message_types.go:57-77`) does not cap text for `text` or `media`, because the 2000-rune cap lives only in the send handler's binding (`handler.go:708`).
  - Edit: see H2 (`message.go:1618`).
  - The gateway caps bodies at 10 MB (`Architecture/services/api-gateway/pkg/edgelimits/edgelimits.go:48`).
  - Outbox: `MessageCreated` / `MessageEdited` carry the full text. kafka-go refuses any message over `BatchBytes` (1 MiB default) with `MessageTooLargeError` (`writer.go:626-635`). `processOutbox` logs the error and `continue`s, leaving the row unpublished, and the fetch is `ORDER BY id LIMIT 50`.
- **Scenario:** One user schedules (or edits) 50 messages of about 2 MB each. Each event fails to publish on every poll and stays at the head of `ORDER BY id LIMIT 50`. From then on the relay re-reads the same 50 poisoned rows every second, and no chat event (pushes, request notifications, search, purge acks) is ever published again until someone deletes the rows by hand. Before that, the multi-MB texts are also stored in Scylla and Postgres and fanned out over Redis to every member.
- **Fix:**
  - Cap text at 2000 runes in `validateMessageContent` for every caller (the service-side check), in `CreateScheduledMessageRequest` (`binding:"max=2000"`) and in edit.
  - In `processOutbox`, treat `MessageTooLargeError` and open failures as poison: mark the row `failed_at` with the error and skip it in the fetch (`WHERE published_at IS NULL AND failed_at IS NULL`), so one bad row can never block the queue.

---

## MEDIUM

### M1. Idempotency keys are never purged: an expired key fails every later use, and a crash wedges a send for 24 h
- **Files:** `message-service/migrations/001_initial_schema.sql:29-36` (`expires_at` default 24 h); `internal/store/postgres/conversations.go:892-921`; `internal/service/message.go:2229-2274`. Nothing deletes from `chat.idempotency_keys` (`retention.go` prunes only `outbox_events`, and `purge.go` deletes per user only).
- **Scenario:**
  - The table grows by one row per send, group create, forward target and direct open, forever. Each row holds a sealed copy of the response.
  - A key whose row is older than 24 h still blocks `INSERT ... ON CONFLICT DO NOTHING`, but `CheckIdempotencyKey` filters `expires_at > NOW()`, so the request fails with the 500 "idempotency key exists but was not found". Example: a client that derives keys deterministically (`scheduled-<id>` in `scheduler.go:31`, `fwd:<key>:<msg>:<target>`) and a scheduled message retried after more than 24 h of backoff.
  - A process crash between `CreateIdempotencyKey` and `SaveIdempotencyResponse` leaves `response NULL`, so every client retry gets `ErrIdempotencyInProgress` for 24 h.
  - Unsend also sets `response = NULL` (`disappearing.go:302`), which turns a retry into "in progress".
- **Fix:**
  - Add an expiry sweep (`DELETE ... WHERE expires_at < now() LIMIT n`) next to `PruneOutboxEvents`.
  - Make `CreateIdempotencyKey` take over an expired row (`ON CONFLICT (key) DO UPDATE SET request_hash=..., response=NULL, expires_at=now()+'24h' WHERE idempotency_keys.expires_at < now()`).
  - Treat a `NULL` response older than about 60 s as abandoned, so the request can be re-run (the delivery intent still makes the send itself idempotent).

### M2. Scheduled messages: no per-user cap, a global first-in-first-out queue, and every replica works the same rows
- **Files:** `message-service/internal/service/messenger_extras.go:488-509` (no count limit); `internal/service/scheduler.go:13-42` (100 rows every 30 s, run serially); `internal/store/postgres/messenger_extras.go:744-752` (`ORDER BY COALESCE(next_attempt_at, send_at) LIMIT 100`, no `SKIP LOCKED`). The worker runs on every replica.
- **Scenario:** One account schedules 100,000 messages all due at 09:00, staying inside the gateway's per-user rate over a day. From 09:00 the worker drains 100 rows per 30 s, about 8 hours of nothing but that account's rows, so every other user's scheduled message is late by hours. Each one also fails the DM rate limit (`dmRateLimit`) and is retried, which makes the backlog worse. With 2-6 replicas each replica sends the same 100 rows. The idempotency key stops a double send, but the loser records a failure attempt (`RecordScheduledMessageFailure`) on a message that was in fact sent.
- **Fix:** Cap pending scheduled messages per user (for example 100) and per conversation. Claim rows with `FOR UPDATE SKIP LOCKED` and a lease (as `message_expiries` already does, migration 013). Pick rows fairly by sender, for example a `DISTINCT ON (sender_id)` first pass.

### M3. Message history disappears across any gap of more than 12 months
- **Files:** `message-service/internal/store/scylla/messages.go:243-319`: walks back at most 12 monthly buckets from `CurrentBucket()` (or from the cursor) and returns a `nil` cursor when it finds nothing. `internal/service/message.go:1515-1527` stops paging on an empty page.
- **Scenario:** A DM that was quiet for 13 months is revived by a new message. `GET .../messages` returns that new message and a cursor. The next page walks 12 empty buckets, returns nothing, and paging ends, so the older history can no longer be reached from any client. A conversation whose newest message is 13 months old opens as empty.
- **Fix:** Keep each conversation's oldest bucket (or its list of non-empty buckets) in Postgres (`conversations.first_message_bucket`, written on the first send), and walk down to it, skipping empty months cheaply. Or store a per-conversation bucket index in Scylla.

### M4. Group receipts and typing cost O(members) per event, so every message in a large group costs about O(members²)
- **Files:**
  - `message-service/internal/service/receipt_ticks.go:450-472` (`MarkDelivered`) and `:549-585` (`publishGroupReadAggregate`, called from `MarkRead`, `message.go:2342-2386`): each call runs `loadReceiptState`, `:290-333`. That loads the whole roster, all delivered cursors, all read cursors and (for reads) every member's policy.
  - `message.go:2286-2327` (`SetTyping`) and `messages_parity.go:572-605`: one non-pipelined, synchronous Redis `PUBLISH` per member, inside the HTTP request.
- **Scenario:** In a 1024-member group each message triggers up to 1024 delivered acks and 1024 reads. Each ack reads about 3 × 1024 rows, so one message costs about 6 M Postgres row reads. One member typing does 1023 serial Redis round-trips every ~3 s. A few active large groups saturate Postgres and slow every chat endpoint.
- **Fix:** For groups, update only the reader's cursor, and recompute the aggregate "read by everyone" watermark with a single `min(last_read_at)` query (or on a debounce, one per conversation per second). Pipeline the typing publishes (as `publishToConversation` does), or publish typing only to `convroom:<id>` for groups.

### M5. ws-gateway: no per-user socket cap, and each socket opens its own Redis connection
- **Files:** `ws-gateway/internal/http/server.go:239-327` (`handleWS`): no per-user or per-IP connection count. `:312`: `s.rdb.Subscribe` creates a dedicated go-redis PubSub connection per socket (go-redis v9.7.3). `readLoop` (`:567-862`) has no inbound frame rate limit: each signaling frame costs a Redis GET plus a PUBLISH.
- **Scenario:** One valid account opens sockets in a loop, limited only by the gateway's per-user request rate. Each socket holds one Redis TCP connection. At Redis `maxclients` (10,000 by default) every service sharing that Redis (rate limits, presence, chat fan-out) fails to connect. A single open socket can also send thousands of `call_ring` frames per second, each costing Redis round-trips.
- **Fix:** Count connections per user in Redis (`INCR wsconn:<user>` with TTL and `DECR` on close, as `presconns` already does) and refuse above about 10. Share one PubSub connection per gateway process and route frames in memory, or at least cap sockets per process. Add a per-connection token bucket in `readLoop`.

### M6. Signaling pair state expires after 1 h and is never refreshed, so calls longer than an hour lose signaling
- **Files:** `chat-service/shared/callauth/callauth.go:38-41` (`DefaultTTL = 1h`); `call-service/internal/service/call.go:700` (`Set(... StateActive)` once on accept, never refreshed); `ws-gateway/internal/http/server.go:810-825` (no pair state means drop).
- **Scenario:** A 1-to-1 call accepted at 10:00 is still running at 11:00. The Redis key expires, and from then on `ice_candidate` (ICE restart after a network switch), `call_end` and `call_upgrade` frames are silently dropped. The callee never hears the hang-up over the socket, and a Wi-Fi to LTE handover cannot renegotiate.
- **Fix:** Refresh the key on each `JoinCall` and heartbeat (`EXPIRE`), or set the TTL from the call's maximum duration. Clear it on end, as is already done at `call.go:884`.

### M7. Unsend leaves the attachment reachable for every member
- **Files:** `message-service/internal/store/postgres/disappearing.go:274-310` (`RedactUnsentMessageRecords`): blanks the text, previews, translations and the cached response, but does not delete `chat.message_media_references` (the disappearing-message path does, at `:224`). `delivery_intents.go:224-241`: media access is granted while a reference row exists.
- **Scenario:** A sends a photo by mistake and unsends it. Any member whose client already fetched the message (or the shared-media tab before the removal) still holds the `media_id`, and `media-service` keeps answering `allowed: true`, so the photo stays downloadable indefinitely.
- **Fix:** Add `DELETE FROM chat.message_media_references WHERE message_id=$1 AND conversation_id=$2` to the unsend transaction (and release the media-service pin, as the expiry path does).

### M8. Delivery intents are kept forever with the full member list
- **Files:** `message-service/migrations/005_durable_message_delivery.sql:11-30` (`member_ids UUID[] NOT NULL`); `internal/store/postgres/delivery_intents.go:187-195` (completion blanks the text but keeps `member_ids`); no delete path except per-user purge (`purge.go:111`).
- **Scenario:** Every group message stores up to 1024 UUIDs (about 16 KB). A 500-member group with 10,000 messages a day adds about 80 MB a day to `message_delivery_intents`, permanently. That bloats vacuum, backups and the `idx_message_delivery_pending` scans.
- **Fix:** Set `member_ids = '{}'` on completion (the replay path does not need it after completion; `settleDelivery` returns early). Then prune completed intents after N days, keeping only `(message_id, conversation_id, bucket, message_ts)` if `locateMessage` still needs it.

---

## LOW

- **L1. No HTTP server timeouts in message-service or call-service.** `message-service/cmd/server/main.go:268` and `call-service/cmd/server/main.go:285` use `gin.Engine.Run`: no `ReadHeaderTimeout`, `ReadTimeout` or `IdleTimeout`, and no graceful shutdown. Anything that reaches the pod directly (inside the cluster) can hold connections open indefinitely. Use `http.Server{ReadHeaderTimeout: 5s, ...}` as ws-gateway already does.
- **L2. Client-selected rooms have no authorization once `EnableScopedRooms` is turned on.** `ws-gateway/internal/http/server.go:718-783`: `subscribe_call`, `subscribe_update`, `subscribe_group_post`, the room signaling broadcast (`:840-850`) and `group_post_typing` subscribe or publish to any client-named channel. Today this is unreachable because `EnableScopedRooms` is never set (`cmd/server/main.go` does not pass it). Turning the flag on would let anyone eavesdrop on and inject into any group call's room signaling. Gate each one on an owner-issued check (as post and live rooms do) before the flag is enabled.
- **L3. Call links carry 32 bits of randomness and ignore `lobby_enabled`.** `call-service/internal/store/postgres/call_extras.go:44-45` uses `uuid.New().String()[:8]`. `JoinCallByLink` has no attempt rate limit (`CheckJoinRate` is keyed per call). The link only admits joiners to open-join group calls, which are fenced off today (`groupCallsEnabled`). Before enabling, use 128-bit tokens and rate-limit lookups.
- **L4. Call reminders are accepted for any call id.** `call-service/internal/service/call_extras.go:140-172`: there is no participant check. Nothing reads reminders yet, so the only effect is unbounded rows. Check participation.
- **L5. Pins are not checked against the conversation and have no role check.** `message-service/internal/service/messenger_extras.go:201-215` and `store/postgres/messenger_extras.go:359-381`: any member, including the sender of a pending request, can pin; `message_id` is not verified to belong to `convID`; and the 3-pin limit is a racy `COUNT` followed by an `INSERT`. Verify the message through `visibleMessage`, require admin or owner in groups, and enforce the limit with a locked count.
- **L6. Read receipts echo the client's `message_id` unvalidated.** `message-service/internal/service/message.go:2356-2405`: the raw string goes into the other member's `read_receipt` frame even when it is not a UUID, and `read_at` is always `now()`, whatever message was named. Send only a parsed UUID.
- **L7. Handlers return internal errors to clients.** Many handlers return `err.Error()` with a 400 (for example `handler.go:802`, `:850`, `:929`), which exposes pgx and gocql messages. Map known errors and log the rest.
- **L8. The notification consumer drops failures and links group messages to a DM.** `Architecture/services/notification-service/internal/events/chat_consumer.go:156-172` logs a processing error and moves on (kafka-go auto-commits, so a transient DB error loses that push), and stops the loop for good on any reader error. `:239` deep-links every message, group messages included, to `/messenger?user=<sender>`, which opens a DM with the sender instead of the group.
- **L9. Some migration indexes were built without `CONCURRENTLY`.** `message-service/migrations/016_field_encryption.sql:59` built `idx_outbox_published_at` on `outbox_events` (unpruned until that release) without `CONCURRENTLY`, which blocks every send's outbox insert while it builds. It is already applied. For future index builds on `outbox_events`, `conversation_members` and `message_delivery_intents`, use `CREATE INDEX CONCURRENTLY` in a migration of its own.
- **L10. `POST /v1/chat/presence` makes serial graph calls.** `message-service/internal/service/message.go:2478-2518`: up to 50 serial graph-service calls per request on a cold cache (each with a 5 s client timeout). Batch the permission check, or fan out with a bounded `errgroup`.

---

## Claims from the Android audit, checked

The live message-service is `chat-service/services/message-service`. The gateway routes `/v1/chat` to the `chat-message-service` container (`Architecture/services/api-gateway/cmd/server/main.go:668-672`). `Architecture/services/_archived-message-service-architecture` is archived, so none of the answers below come from it.

### C1. Confirmed: any unclassified send error, database errors included, is answered 400 INVALID_REQUEST (Medium)
- **Files:**
  - `message-service/internal/http/handler.go:804-813` (`SendMessage`): after `writeIdempotencyError` (`:1599-1613`) and `writeMessagingError` (`:1617-1645`), every other error is logged at Error level and answered with `api.Error(..., 400, "INVALID_REQUEST", err.Error())`.
  - What falls through to that 400: pgx and gocql failures, the Redis or media-authority outage (`media authority unavailable`, `service/media_attachment.go:37`), "delivery is pending durable repair" (`message.go:1371`), `ErrMatchClosed`, `ErrMediaNotAllowed`, and "not a conversation member" (`message.go:1119`).
- **Failure path:** Scylla or Postgres has a blip, and the client gets a 400 carrying the raw driver text. Clients treat 4xx as permanent, so the Android outbox marks the message failed instead of retrying. The error text also leaks internal detail (L7).
- **Fix:** Add a `default` branch that returns 503 `UNAVAILABLE` for anything not classified. Map typed errors to their own codes: not-a-member to 403 or 404 `NOT_MEMBER`, `ErrMatchClosed` to 409, `ErrMediaNotAllowed` to 403. Return a generic message instead of `err.Error()`.

### C2. Confirmed, and broader than claimed: about 50 handlers answer every failure with 400, so a transient error and "no longer a member" look the same (Medium)
- **Root cause:** The membership refusals are fresh `errors.New("not a conversation member")` values (`service/message.go:1119`, `:1491`, `:1626`, `:1715`, `:1844`; `messenger_extras.go` throughout). They are not the `ErrNotMember` sentinel (`service/inbox.go:459`), so even `errors.Is` cannot tell them apart from a database error.
- **Same blanket `400 INVALID_REQUEST, err.Error()` branch** (`grep 'StatusBadRequest, "INVALID_REQUEST", err.Error()'`):
  - In `handler.go`: `CreateDirectConversation` :447, `CreateGroupConversation` :496, `GetConversation` :522, `AddMember` :611, `RemoveMember` :638, `UpdateConversation` :687, `SendMessage` :812, **`GetMessages` :849**, `EditMessage` :929, `DeleteMessage` :984, `ToggleReaction` :1024, `SetTyping` :1429, `MarkRead` :1458, and the dating internal handlers (:1131, :1200, :1252, :1328, :1383).
  - 17 sites in `messenger_extras_handler.go` (settings, folders, pins, requests, stars, backup, scheduled, threads; :31-688).
  - 7 in `governance_handler.go` (:25-171), 4 in `group_invite_handler.go` (:73-166), and `disappearing_handler.go:33` and `dm_consent_handler.go:187`.
- **Failure path:** A user is removed from a group. `GET .../messages` returns `400 INVALID_REQUEST "not a conversation member"`. A Postgres timeout on `CheckMembership` returns the same status and code, with only the free text different. The client cannot decide between "drop this conversation" and "retry", so it either keeps retrying a removed group or evicts a live chat on a blip.
- **Contrast:** The parity handlers (`parity_handler.go`) already map `ErrNotMember` to 404. The legacy routes never adopted that mapping.
- **Fix:** Return the `ErrNotMember` sentinel from every membership check. Add one `writeServiceError(c, err)` helper used by all these handlers: `ErrNotMember` to 404 `NOT_MEMBER`, validation errors to 400, conflicts to 409, the default to 503 with a generic body. Add a contract test that a removed member gets 404 and an injected store error gets 5xx on `GetMessages` and `SendMessage`.

### C3. Confirmed, with small gaps: the server sends `expires_at` and both hides and purges expired disappearing messages
- **Files:**
  - Set at send: `service/message.go:1328-1330` captures `extra.ExpiresAt = disappearExpiry(now, meta.DisappearAfterMs)` into the durable intent.
  - Sent to clients: `MessageExtra.ExpiresAt` is serialised as `json:"expires_at,omitempty"` (`service/rich_messages.go:99-101`) and copied onto every `MessageResponse` (`:174`) for the send response, history and realtime frames (through `applyExtra` / `addExtraToFrame`).
  - Hidden on read: `GetMessages` drops expired rows on every page (`message.go:1506-1507`, `:1525`, using `dropExpired` in `service/disappearing.go:60-69`). `visibleMessage` (`messages_parity.go:117`) applies the same filter to forward, reactions list, report, delete-for-me and read summary.
  - Purged: `StartMessageExpiryWorker` (`service/disappearing.go:232`) runs by default (`internal/config/config.go:109`, `DISAPPEARING_MESSAGES_WORKER_ENABLED` defaults to true and is not overridden in `deploy/services/chat-message-service/`). It claims rows with `SKIP LOCKED` (migration 013), deletes the Scylla content along the unsend path, and removes shared items, media references, stars, pins and translations (`store/postgres/disappearing.go:220-235`).
- **Gaps (Low):**
  - `EditMessage` (`message.go:1630-1638`) and `ToggleReaction` (`:1853-1859`) read the message with `GetMessage` and no `dropExpired`. For up to one poll interval (30 s, plus the worker backlog) after expiry, an expired message can still be edited, and the edit is fanned out with its text, or reacted to.
  - The shared-media tab, starred previews and pins are cleaned only by the worker, so they show the item until it runs.
  - **Fix:** Route edit and react through `visibleMessage`.

---

## Not covered / left alone
- E2E: there is none. Content is sealed at rest with KMS envelope keys (`chat-service/shared/fieldcrypt`). I did not review the key ring itself.
- Hidden-chat secret, lockout and open-token flows (`hidden_chats*.go`) were read only for their effect on fan-out (`personalChannel` skips locked members). Their cryptography was not audited.
- media-service's side of chat attachments (signing and URL TTL) is outside this tree. Chat hands out media ids only, and access goes through `/internal/v1/chat/media-access`.
