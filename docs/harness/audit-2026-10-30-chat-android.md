# Audit: Android chat / messaging

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits, no git writes, no Gradle builds, no device.
Android paths are relative to `mobile/android/`. Server paths are relative to the repo root.

Modules covered: `core/chat` (REST API, socket, session manager, Room store and outbox, hidden chats, chat lock), `feature/chat` (thread, inbox and home screens, navigation), `core/notifications` (push presenter), `core/network` (error mapping, retry, auth), `core/auth/SessionManager`, and `app` (`ChatForegroundPresence`, `MainActivity`, manifest, image loader). Server side: `chat-service/services/message-service`, `chat-service/services/ws-gateway`, the `/v1/chat` and `/v1/ws` gateway routes in `Architecture/services/api-gateway/cmd/server/main.go:666-672`, and `notification-service`.

Every Retrofit path in `core/chat` was matched against the gin routes in message-service, plus `GET /v1/graph/connections/:userId` in graph-service. None is retired or missing. The send, read, typing, delivered, reaction, delete, settings and requests bodies match the server's binding tags. The findings below were each traced end to end. Anything I could not confirm was dropped.

---

## HIGH

### H1. The chat socket can stay dead while the app is in the foreground, and foreground "dm" pushes are then dropped, so incoming messages are missed with no notification
- **Files:**
  - `core/auth/src/main/kotlin/com/us/android/core/auth/SessionManager.kt:61-62, 78-87, 90`: the access token is held in memory only. At cold start it is `null` while the state is already `Authenticated` (an optimistic state built from the stored refresh token). It is filled only when the first REST call gets a 401 and the authenticator refreshes it. Nothing refreshes it up front at startup.
  - `app/src/main/kotlin/com/us/android/chat/ChatForegroundPresence.kt:45-55, 80-93`: when the app comes to the foreground while signed in, `ChatSessionManager.start()` is called once. `PresenceSocketGate` acts on state changes only, so it never calls `start()` again while the app stays in the foreground.
  - `core/chat/src/main/kotlin/com/us/android/core/chat/data/ChatSocket.kt:274-279`: a null or blank token makes `connect` emit `Disconnected(permanent = true)` without making any network request. `:313-315`: a 401 or 403 on the upgrade request is also treated as permanent, even when it comes from a refresh that failed for a transient reason.
  - `core/chat/src/main/kotlin/com/us/android/core/chat/data/ChatSessionManager.kt:336-340`: on a permanent disconnect the loop returns and `endSession` releases the scope (`:122-130`). Nothing restarts it.
  - `core/notifications/src/main/kotlin/com/us/android/core/notifications/NotificationPresenter.kt:83` together with `DoorstepOfferAlertCoordinator.kt:29-30`: while the app is in the foreground, every `dm` and `message_request` push is dropped, on the assumption that "the session socket already delivered" the message. Chat pushes use type `dm` (`Architecture/services/notification-service/internal/service/notification.go:454`).
- **Scenario:** A user cold-starts the app and lands on the feed. `ProcessLifecycle ON_START` fires and the gate calls `start()`. `store.ensureReady()` is a local DB check, so `connect` runs before the feed's first request has completed its 401, refresh and retry round trip. The token is still null, so the socket ends permanently.
  - For the rest of that foreground session the user shows as offline, and inbox badges and Notes do not update live.
  - A friend's message arrives by FCM and is suppressed because the app is in the foreground. Nothing appears.
  - The socket comes back only when the user opens a chat screen (the ViewModels call `start()` defensively) or sends the app to the background and brings it back.
- **Fix:**
  - In `ChatSessionManager.runLoop`, when `currentAccessToken()` is null, `await` `TokenRefresher.refresh()` before `connect`. Treat a null token as a retryable disconnect, not a permanent one.
  - On a permanent rejection, do not return while the session is `Authenticated`. Suspend until the token changes, for example by observing the session state or a token flow, then reconnect.
  - Suppress a foreground `dm` push only when `ChatSessionManager.connection.value == Connected`.

### H2. Disappearing messages stay in plaintext on the device and reappear in the thread after they expire
- **Files:**
  - Server: `chat-service/services/message-service/internal/service/message.go:270`: `MessageResponse.ExpiresAt` (`expires_at`) is on every history page and every send response. `service/disappearing.go:15-27`: an expired message is removed only by the `message_deleted` frame with `reason: "expired"`.
  - `core/chat/src/main/kotlin/com/us/android/core/chat/data/ChatApi.kt:575-632` (`MessageDto`): there is no `expires_at`, so the client cannot expire anything itself.
  - `ChatSessionManager.kt:323` → `ChatStore.kt:751-753`: the cached row is deleted only when that frame arrives on the socket. The socket is closed whenever the app is in the background, by design (`ChatForegroundPresence.kt:28-31`), so almost every 24 h, 7 d or 90 d expiry happens while nothing is listening.
  - `ChatStore.kt:407-418` (`cacheMessages`): rows are inserted and never removed when the server stops returning them.
  - Resurfacing paths:
    - `feature/chat/src/main/kotlin/com/us/android/feature/chat/ui/ChatThreadViewModel.kt:1260-1264` (`mergeConfirmed`): after every send completes, it re-adds the 10 newest cached rows through `controller.onRealtimeMessage`, which prepends any id the thread does not already hold (`ThreadController.kt:383-393`).
    - `ChatThreadViewModel.kt:343-348`: the offline fallback renders the cached rows as they are.
- **Scenario:** A DM has a 24 h timer. Message X expires overnight with the phone locked, so the server deletes it, but `us.db` still holds X's text. The next day the user opens the thread: page 1 comes from the server without X. The user sends "hi". When the outbox row completes, `mergeConfirmed` reads the cached tail, finds X missing from the thread and prepends it, so X reappears as the newest bubble. Opening the thread offline also shows X. Its text stays on disk until sign-out.
- **Fix:**
  - Add `expires_at` to `MessageDto`, `Message` and `ChatMessageEntity`.
  - Filter expired rows in `recentMessages` (`... AND (expiresAt IS NULL OR expiresAt > :now)`), and purge them from Room on `syncInbox` and on thread open.
  - On `refresh()`, delete cached rows that fall inside page 1's time window but were not returned by the server.

---

## MEDIUM

### M1. A transient server error, or a resend that overlaps the first attempt, parks a message as failed when it should be retried, and the user may discard a message that was actually delivered
- **Files:**
  - Server: `chat-service/services/message-service/internal/http/handler.go:804-812`: in `SendMessage`, any error that is not an idempotency or messaging error is answered `400 INVALID_REQUEST`. That includes DB errors from `CheckMembership` and `GetConversationMeta` (`service/message.go:1114-1119, 1138-1142`), failures of the idempotency store, and "not a conversation member".
  - Client: `core/network/src/main/kotlin/com/us/android/core/network/ErrorMapper.kt:49` maps code `INVALID_REQUEST` to `AppError.InvalidRequest`. `ChatStore.kt:1304-1323` (`isRetryableSend`) treats that as permanent, and `ChatStore.kt:1006-1012` marks the row failed after a single attempt.
  - `handler.go:1607-1608`: `409 IDEMPOTENCY_IN_PROGRESS` is answered while the first request with the same key is still running (`service/message.go:2250-2255`). The client maps it to `AppError.Unknown(statusCode = 409)` (`ErrorMapper.kt:86`), which `isRetryableSend` also treats as permanent.
- **Scenario:**
  - Postgres hiccups for one request. The message shows "Not sent" with Retry and Discard, even though the outbox was built to retry five times by itself.
  - On a poor connection, a send hits the 30 s read timeout (`NetworkModule.kt:112`, then `RetryLater`). The socket reconnects, and `drainNow()` (`ChatSessionManager.kt:291`) sends the same key again while the server is still writing the first request. The server answers 409 IN_PROGRESS and the row is parked as failed.
  - The first request then completes. The recipient has the message, but the sender sees "Not sent". If they tap Discard, they believe it never went.
- **Fix:**
  - Server: return `500 INTERNAL_ERROR` by default and `403` for a non-member.
  - Client: treat `409` with code `IDEMPOTENCY_IN_PROGRESS` as `RetryLater`. A replay with the same key returns the stored response.

### M2. Every reconnect replaces the open thread with page 1, and the socket reconnects at least every 15 minutes
- **Files:**
  - `core/chat/src/main/kotlin/com/us/android/core/chat/data/ThreadController.kt:147-155`: `refresh()` replaces `messages` with page 1 and resets `nextCursor`.
  - `ChatThreadViewModel.kt:1149-1160`: `refresh()` runs on every `Connected` event.
  - `chat-service/services/ws-gateway/internal/http/server.go:213-229, 261-263`: the gateway closes the socket 3 s before the JWT expires. `identity-platform/services/auth-service/internal/config/config.go:197` sets `ACCESS_TOKEN_TTL` to 15 minutes by default.
  - `ChatSessionManager.kt:280`: `attempt` resets to 0 on every `Connected`.
- **Scenario:**
  - A user scrolls back three pages, or uses "jump to quoted message", which loads up to `JUMP_PAGES = 5` pages. The JWT expiry or a background and foreground round trip makes the socket reconnect. The list collapses to the newest 30 messages and the scroll position is lost. This repeats every 15 minutes.
  - Inside the 3 s skew window, the client reconnects with the old token, which is still valid. The gateway upgrades, sees the token as "already expired" (`server.go:216-219`) and closes. The client counts that as a success and comes back at the minimum backoff.
  - Each such cycle repeats the full reconciliation: `syncInbox` (up to 6 requests), one entitlement POST per open room, and a thread refresh.
- **Fix:**
  - On reconnect, fetch page 1 and merge it by id into the existing list. Keep older pages and the existing cursor.
  - Before `connect`, refresh the token when `tokenStore.accessTokenExpiresAtMillis` is less than about 30 s away.
  - Reset `attempt` only after the connection has stayed up for a few seconds.

### M3. A slow screen can stall the session socket, which then silently drops frames, including messages and call signaling
- **Files:**
  - `ChatThreadViewModel.kt:1146-1160`: the collector waits on an HTTP `controller.refresh()` inside `collect`. With `CALL_TIMEOUT_SECONDS = 45` that can take up to 45 s.
  - `ChatSessionManager.kt:82`: `MutableSharedFlow(extraBufferCapacity = 64)` uses the default `SUSPEND` policy, so `_events.emit` (`:334`) suspends the whole `runLoop`.
  - `ChatSocket.kt:273, 295`: `callbackFlow` uses its default 64-slot buffer, and `onMessage` uses `trySend`, which fails silently when the buffer is full.
- **Scenario:** A thread is open in a busy group on a weak network. The socket reconnects and the thread's refresh hangs on the network. Typing, presence and receipt frames fill both 64-slot buffers. From then on, `trySend` drops frames:
  - message frames, which never reach `applyRealtimeMessage`, so no unread state and no delivered tick;
  - `message_deleted` frames;
  - `call_*` frames that `CallSessionManager.kt:143` waits for on the same flow.
- **Fix:**
  - In the ViewModel, run the reconnect refresh in its own job (`launch`, cancelling the previous one) instead of inside `collect`.
  - In `ChatSocket`, use `buffer(Channel.UNLIMITED)` or `trySendBlocking`, or at least never drop message, call or delete frames.
  - Optionally, conflate typing and presence frames.

### M4. Opening a thread offline shows its messages in reverse order
- **Files:**
  - `core/database/src/main/kotlin/com/us/android/core/database/ChatEntities.kt:161-162`: the cached rows are read `ORDER BY createdAt DESC`, newest first.
  - `ChatThreadViewModel.kt:347`: `cached.forEach { controller.onRealtimeMessage(it) }`. Each call prepends (`ThreadController.kt:389`), so the oldest row ends up at index 0, which is the bottom of the `reverseLayout` list next to the composer.
  - `mergeConfirmed` (`:1261`) uses `asReversed()` and gets this right. The offline fallback does not.
- **Scenario:** A user opens any chat in airplane mode. The first refresh fails and the cached tail renders upside down: the oldest message sits beside the composer and the newest is at the top.
- **Fix:** `cached.asReversed().forEach { ... }`.

### M5. The inbox and requests lists decode JSON for every row on the main thread each time a conversation row changes
- **Files:**
  - `ChatStore.kt:272-279`: the `rows.map { it.toDomainConversation() }` runs in the collector's context. `toDomainConversation` (`:1146-1170`) decodes `membersJson` and `extrasJson` for every row.
  - `feature/chat/src/main/kotlin/com/us/android/feature/chat/ui/home/ChatHomeViewModel.kt:567-573`: both `conversationsFlow()` and `requestsFlow()` are collected in `viewModelScope`, on Main.
  - The inbox can hold up to 5 × 100 rows (`ChatRepository.kt:344`).
- **Scenario:** Every realtime message, read receipt or unread change writes the conversation table (`applyRealtimeMessage` → `recordLastMessage`). That invalidates both Room flows, so each event costs two full decodes of up to 500 rows on the UI thread. In an active group this causes jank on the chat home screen.
- **Fix:** Add `.flowOn(Dispatchers.Default)` after the `combine` in `ChatStore`, plus `distinctUntilChanged()`.

---

## LOW

### L1. The cached Requests list is truncated at 20 and never pruned
- **Files:** `ChatApi.kt:116-117` (`requests()` sends no limit and no cursor; the server default is 20) and `ChatStore.kt:293, 333-339` (rows are only upserted).
- **Scenario:** If a request is declined on the web, withdrawn, or blocked from another device, it stays in the Android Requests list and its count until sign-out. Requests beyond the first 20 are never cached.
- **Fix:** Page with `MessageConsentApi.requestsPage` (it has a cursor), and prune the request rows the way `pruneInbox` prunes the inbox.

### L2. Chat content stays on disk unencrypted, and chat photos survive sign-out
- **Files:**
  - `core/database/.../di/DatabaseModule.kt:26`: the Room database has no `openHelperFactory`, so message text is plaintext in `us.db`. Only the outbox of hidden chats is sealed.
  - `app/src/main/kotlin/com/us/android/UsImageLoader.kt:42-48`: the Coil disk cache (`cacheDir/images`, 256 MB) keeps photos from chats that are not hidden. `ChatTeardown.kt` wipes Room but not this cache.
  - `allowBackup="false"` mitigates the backup route.
- **Scenario:** On a rooted device, or by forensic extraction, the chat history and photos can be read even with the chat PIN lock on. After account A signs out and B signs in on the same device, A's chat photos are still on disk.
- **Fix:** Clear Coil's disk and memory caches in `ChatTeardown` (or turn off the disk cache for chat `/v1/media/*/serve` requests). Consider SQLCipher for the chat tables.

### L3. Chat notifications already posted are not cleared on sign-out
- **Files:** `core/notifications/.../data/PushTeardown.kt` only unregisters the push token. `NotificationPresenter.tapIntent` carries the conversation id.
- **Scenario:** Account B signs in and taps A's leftover "New Message" notification. It opens A's conversation id and lands on a 403 or error screen.
- **Fix:** Call `NotificationManagerCompat.from(context).cancelAll()` in a teardown task.

### L4. `GetMessages` answers every failure with 400 `INVALID_REQUEST`
- **Files:** `chat-service/.../internal/http/handler.go:846-850`.
- **Scenario:** A Scylla timeout and "removed from the group" look identical to the client: both are a non-retryable `InvalidRequest`, so the thread cannot tell "try again" from "you are no longer a member". The same 400 pattern appears at `handler.go:447, 496, 522, 611, 638, 687, 929, 984`.
- **Fix:** Return 403 for a non-member and 5xx for everything unclassified. This is the same server change as M1.

---

## Checked and fine
- **Socket auth and reconnect:** the token is sent in the Authorization header, never in the query. The socket shares the authenticated OkHttp client, so a 401 on the upgrade is refreshed by the single-flight `TokenAuthenticator`. Reconnect backoff is full jitter from 1 s to 30 s.
- **Sends:** each outbox row carries one idempotency key, replayed on every retry. Rows are drained in order per conversation, and a retryable failure blocks later rows in that conversation. Echo de-duplication works (`heldEchoes`, `adoptAddress`).
- **Sign-out:** `stopAndJoin` runs first, then a gated wipe, then the disk scrub, with a write-ahead marker that records a scrub still owed.
- **Pushes:** chat pushes are generic by construction on the server ("New Message", no preview). Hidden chats get blanked pushes. The chat channel's lockscreen visibility is `PRIVATE`.
- **Links and navigation:** the only App Link filter is `/chat/join/`, and the join screen previews first and joins only on a tap. Every chat route except lock settings sits behind `ChatLockGate`, and turning the lock off needs the PIN. The FileProvider is not exported.
- **Attachments:** GIF URLs are checked against Giphy hosts on the server (`service/gifs.go:20-53`). Photos load through `/v1/media/{id}/serve`, never from a URL the sender supplied. Location links are URI-encoded.
- **Logging:** HTTP logging is `BASIC` in debug only, `NONE` in release, with `chat_open` redacted. No message content is logged.
