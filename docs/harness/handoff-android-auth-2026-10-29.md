# Handoff: Android login and registration fixes (29 Oct 2026)

The cloud session can't build Android (no Android SDK; the proxy blocks the
Java 25 toolchain download), so this lane runs on the local Windows tree.
Paste the prompt below into the lead local agent.

---

You are the lead agent for modernsmapp. Read `CLAUDE.md` and
`docs/harness/AGENT-RULES.md` and follow them. Branch:
`codex/module-01-02-launch-safety` (`git fetch origin && git pull` first).
Subagents make no git writes; you commit by path with the trailer
`Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. No devices or
emulators: the founder runs the device. Never weaken assertions, delete tests
or add skips.

Scope: `mobile/android/core/network`, `mobile/android/core/auth`,
`mobile/android/feature/auth` only. One Android lane. Mutation-check the
security guards (cookie filtering, the session epoch) with
`scripts/harness/mutate.sh`. Check with
`scripts/verify/android.sh --fast :core:network :core:auth :feature:auth`,
and `--full` once at the end.

The audit with every file and line is
`docs/harness/audit-2026-10-29-auth-edge-android.md` (Android section). Fix:

1. **H1, logged out every 15 minutes in-process.** `CsrfCookieStore` keeps
   every cookie, and OkHttp doesn't check expiry, so the expired
   `access_token` cookie rides along on `/v1/auth/refresh`, the gateway
   answers 401, and `SessionManager` treats it as final. Keep only the
   `csrf_token` cookie and drop expired cookies. Send the refresh token in the
   JSON body for refresh and logout: auth-service accepts it there (check the
   handler for the exact field name before you code it). The gateway now
   treats a bad credential on the public `/v1/auth/*` routes as anonymous.
2. **H2, networking can lock up on cold start.** `TokenAuthenticator` blocks
   on a refresh that queues on the same OkHttp dispatcher (5 per host). Run
   the refresh on its own client with timeouts. Wire in the proactive refresh
   before the first request after process death (`shouldProactivelyRefresh`
   exists but is never called). Add a MockWebServer test: 6+ concurrent 401s
   through the real client stack all recover with exactly one refresh.
3. **L4.** A 401 arriving after a refresh already finished retries with the
   new token instead of refreshing again.
4. **L5.** An in-flight refresh must not bring back a session after logout,
   or overwrite a newer login: use a session generation/epoch check.
5. **L6.** A malformed OAuth redirect (`%zz`) must not crash
   (`GoogleAuthorizationFlow.parseQuery`); show a failure instead. Persist the
   state and PKCE verifier safely so process death during the browser step
   doesn't always fail with a state mismatch.
6. **L7.** A wrong partner-phone OTP shows the OTP message, not
   "That email or password isn't right".
7. **L9.** The verify-email resend timer survives rotation correctly.
8. **Google sign-in must send an ID token (blocking; do this first).** The
   server closed an account-takeover hole: `POST /v1/auth/oauth/google/token`
   no longer accepts a Google *access* token. It now requires
   `{"id_token": "<Google ID token>"}`, verified against Google's keys and
   our client ids, and answers 400 `ID_TOKEN_REQUIRED` to the old body.
   `GoogleAuthorizationFlow.kt` must read `id_token` from Google's token
   response (the scope already includes `openid`) and send it; update
   `OAuthTokenRequestDto` and the Android tests to match. Until this ships,
   Google sign-in on Android fails. The deploy also needs
   `OAUTH_GOOGLE_ID_TOKEN_AUDIENCES` set to the Android OAuth client id
   (founder), or the route answers 503 `OAUTH_UNAVAILABLE`.

Report: files, the `android.sh --full` RESULT block, mutations killed/total,
and anything only the founder can confirm on a device.

---

## Second Android lane: home feed and reels (30 Oct 2026)

Run this as its own lane after the login lane above. Scope:
`mobile/android/feature/feed` (and the reels screen it shares), plus the core
paging/media modules they use. The audit with exact lines is
`docs/harness/audit-2026-10-30-feed-reads-android.md` (Android section). Fix:

1. **Deleting your just-published post doesn't remove it.** The post pinned
   at the top after publishing (`_head` in `FeedViewModel.kt` ~200-201,
   ~249-253, ~274-289, drawn by `FeedScreen.kt` ~248; `_live` in
   `ReelsViewModel.kt` ~352, ~385, ~464, ~516-523) ignores delete, hide and
   edit, so a deleted post keeps showing and playing at the top. Apply delete,
   hide and edit to these pinned items exactly as to paged items.
2. **Memory grows for the whole session.** `FeedPager.kt` ~21-27 sets no
   Paging `maxSize`, and the Home tabs never end. Set `maxSize` (and keep
   `enablePlaceholders` consistent with it) so old pages are dropped.
3. **Low:** `MediaDeliveryCache.kt` ~26-34 never evicts expired entries;
   follow status for every loaded author is re-requested on every page, and
   failures retry every time (`FeedViewModel.kt` ~493-498, ~591-593,
   `FollowGraph.kt` ~63-87); a failed pull-to-refresh with posts on screen
   shows nothing, so offline looks like up to date (`FeedScreen.kt` ~346-353,
   ~556-568).

Unit tests for each; report the `android.sh --full` RESULT block.

---

## Third Android lane: reels (30 Oct 2026)

Run after the feed lane. Scope: the Android reels modules (player, pager,
create/upload flow, studio preview, chat shared-reels viewer). The audit with
exact lines is `docs/harness/audit-2026-10-30-reels-create-android.md`
(Android section). Also check playback against the server change that
lengthens HLS segment signatures; until it ships, re-prepare on a 403. Fix:

1. **Reel upload stuck on "Preparing" after process death.** The Photo Picker
   grant is gone, the first URI lookup throws `SecurityException`
   (`MediaSourceResolver.kt` ~37-38, ~52-53; `ReelPublishStore.kt` ~318;
   `ReelPublishPipeline.kt` ~120-126), and the worker crashes on every launch,
   cancelling the publishes behind it. Catch lookup errors and end the step
   as unreadable with a message; copy the video (or keep a lasting permission)
   before queueing; wrap the worker run in a catch-all that records a failure.
2. **Chat shared-reels viewer** (`SharedReelsViewer.kt` ~163-167, ~177;
   `MediaModule.kt` ~124): the first reel never starts (play before the player
   exists, no tap-to-play), reels are always muted (volume 0 never raised),
   and playback doesn't pause in the background.
3. **Reel studio preview keeps playing with sound in the background**
   (`ReelPreviewPlayer.kt` ~32, `ReelStudioScreen.kt` ~165-167): pause on stop.
4. **Reels comes back frozen after backgrounding** (`ReelsScreen.kt`
   ~1834-1851, ~1135): resume on start, or show the play glyph so one tap plays.
5. **Small uploads (<100 MB) aren't resumed or retried**
   (`ReelMediaUploads.kt` ~97-126): retry transient network failures
   automatically, and don't re-upload the whole file when only confirm failed.

Unit tests for each; report the `android.sh --full` RESULT block.

---

## Fourth Android lane: long videos / Tube (30 Oct 2026)

Run after the reels lane. Scope: `mobile/android/feature/tube`, plus the
`core/media` and `core/feed` pieces it uses. The audit with exact lines is
`docs/harness/audit-2026-10-30-tube-android.md`. Fix:

1. **H1, any playback error leaves the player dead, including expired
   segment URLs (403).** Server segment URLs now live 2x the duration +
   15 min (max 2 h), so a 2.5 h video or a long pause hits 403.
   `WatchViewModel.kt` ~339-366 has no `onPlayerError`, and `togglePlay`
   (~754-762) never calls `prepare()`. Add `onPlayerError` that rebuilds the
   media source (fetching the playlists again), prepares, seeks back and caps
   attempts; make Play call `prepare()` when idle with an error; show an
   error state with Retry (`WatchControls.kt` ~217, `VideoLoading.kt` ~297-302).
2. **H2, privacy: account A's History / Watch later / Liked shows in B's
   Up next after sign-out and sign-in.** `TubeQueue.kt` ~21-31 is a singleton
   never cleared. Add a sign-out teardown task (`AuthRepository.kt` ~309) that
   clears it, or key it by viewer id. Mutation-check this guard.
3. **M1** audio focus and becoming-noisy handling (`MediaModule.kt`
   ~113-125, `WatchViewModel.kt` ~296-299). **M2** stacked Watch screens each
   keep a prepared player (`TubeNavigation.kt` ~263, `WatchViewModel.kt`
   ~831-839): `stop()` on stop and re-prepare on start, or single-top.
   **M3** analytics view not reopened after backgrounding (`WatchViewModel.kt`
   ~831-839, `WatchScreen.kt` ~669-679), so creators lose credited views.
   **M4** a long upload stops for good after ~31 s offline
   (`ResumableUploader.kt` ~241, ~295-323, `ReelPublishWorker.kt` ~115-117):
   return `Result.retry()` while a resumable session exists. **M5** page
   Collections / Watch later / Liked (`VideoLibraryRepository.kt` ~136-145)
   and bound `HashtagPostHydrator.kt` ~75-78 concurrency; the server now caps
   and paginates playlist items.
4. **Lows L1-L7** as listed in the audit (old video playing behind
   "Loading", membership-reload race, sheets drawn into PiP, Clear all undone
   by Load more, age-restriction message, per-account history pause, a longer
   buffer profile for long video).

Unit tests for each; report the `android.sh --full` RESULT block.

## Fifth Android lane: chat (30 Oct 2026)

Audit: [audit-2026-10-30-chat-android.md](audit-2026-10-30-chat-android.md)
(server side: [audit-2026-10-30-chat-backend.md](audit-2026-10-30-chat-backend.md),
fixed on this branch). Work in `mobile/android` only; one item at a time,
unit test each, `scripts/verify/android.sh --fast :module` while working.

1. **H1 socket dies in the foreground** (`ChatSocket.connect`,
   `ChatSessionManager.runLoop`, `ChatForegroundPresence`,
   `suppressForegroundNotification`): refresh the token before the first
   connect, treat a null token / 401 / 403 as "refresh and retry with
   backoff" while signed in (stop only on sign-out), and suppress a
   foreground `dm` push only while the socket is actually connected.
2. **H2 disappearing messages stay on the device**: add `expires_at` to
   `MessageDto` and the cache entity (Room migration), filter expired rows
   in every cache query and in `mergeConfirmed`, and purge expired rows on
   open and on a timer. The server already sends `expires_at` and hides
   expired messages on every read.
3. **M1 send classification**: retry `409 IDEMPOTENCY_IN_PROGRESS` with the
   same key; treat `503 UNAVAILABLE` as retryable. The server no longer
   answers infrastructure failures with 400 (see the table below).
4. **M2** keep the loaded pages and scroll position across a reconnect
   (fetch only what is newer than the newest message held). **M3** do not
   suspend inside the socket event `collect` (launch the refresh), and do
   not drop frames silently (`trySend` failure → resync). **M4** add the
   missing `.asReversed()` in the offline fallback. **M5** `flowOn(Dispatchers.Default)`
   for the inbox/requests JSON decode.
5. **Lows**: L1 page the cached Requests list and prune it; L2 encrypt the
   chat database (SQLCipher or EncryptedFile key) and clear the image cache
   on sign-out; L3 cancel posted chat notifications on sign-out.

**Server contract changes on this branch (message-service, call-service,
ws-gateway); the client must handle them:**

| Case | Before | Now |
|---|---|---|
| Not a member (history, send, edit, delete, react, typing, read, settings, pins, requests, scheduled, star, group, invite) | 400 `INVALID_REQUEST` + raw error | 404 `NOT_FOUND` |
| Infrastructure failure | 400 `INVALID_REQUEST` | 503 `UNAVAILABLE` (retry) |
| Closed dating match | 400 | 409 `MATCH_CLOSED` |
| Media not allowed | 400 | 422 `MEDIA_NOT_ALLOWED` |
| Role / not your message / not allowed | 400 | 403 `FORBIDDEN` |
| Already a member, last admin, already resolved, owner must transfer | 400 | 409 `CONFLICT` |
| Message or folder not found | 400 | 404 `NOT_FOUND` |
| Edit over 2000 chars | — | 400 `BAD_REQUEST` |
| Edit in a pending request with a link or over 500 chars | — | 422 `INVALID_REQUEST_MESSAGE` |
| Edit while chat paused | — | 403 `MESSAGING_NOT_ALLOWED` |
| Edit or react on an expired message | — | 404 |
| Body over 64 KiB (send, edit, schedule) | — | 413 `PAYLOAD_TOO_LARGE` |
| More than 100 pending scheduled messages | — | 400 |
| Pin/unpin by a plain group member | allowed | 403 `FORBIDDEN` (hide pin in the long-press menu unless owner/admin; `ChatPins.kt` says "any member may pin") |
| WebSocket connect over 10 sockets per user | — | 429 before upgrade (back off; don't hot-loop) |
| WebSocket frames over 20/s (burst 60) | — | dropped |
| Read-receipt frames | any id | only real message ids of that conversation |

Codes the app already parses are unchanged (`INVITE_NOT_FOUND`,
`INVITE_NOT_LIVE`, `JOIN_NOT_ALLOWED`, `GROUP_FULL`, `MESSAGING_NOT_ALLOWED`,
`CHAT_LOCKED`, `INVALID_REQUEST_MESSAGE`, `RATE_LIMITED`, `IDEMPOTENCY_*`).

**Watch out:** `ChatParity.isMissingRoute()` reads any 404 as "route not
shipped yet". It is used in `ChatSettingsScreen.kt` (~320, ~333, ~851),
`InboxLists.kt` ~109, `ChatThreadViewModel.kt` ~1038 and
`RequestsFolder.kt` ~125. A not-a-member 404 now reaches these: tell the two
apart by the error code (a missing route has no `NOT_FOUND` envelope from
message-service) so a removed member is not told "isn't available yet",
and `oldPath()` at ~851 is not tried for a membership refusal.

Unit tests for each; report the `android.sh --full` RESULT block.

## Sixth Android lane: commerce (30 Oct 2026)

Audit: [audit-2026-10-30-commerce-android.md](audit-2026-10-30-commerce-android.md)
(server side: [audit-2026-10-30-commerce-backend.md](audit-2026-10-30-commerce-backend.md),
fixed on this branch). Work in `mobile/android` only; one item at a time,
unit test each, `scripts/verify/android.sh --fast :module` while working.

1. **H1 dead "Try again" after a failed checkout** (`CheckoutViewModel.kt`
   ~406-436, `CheckoutScreen.kt` ~224-229): on an ambiguous failure
   (timeout, 5xx, network) keep the phase `Submitting` and resubmit with the
   SAME idempotency key so the server returns the order it already made; on
   a quote failure re-quote. Never show "Your bag is empty" while an order
   from this attempt may exist.
2. **M1** disable `MethodRow` while Place order is in flight, and never drop
   a payment-sheet result (`CheckoutScreen.kt` ~456-469,
   `CheckoutViewModel.kt` ~204-215, 377-403). **M2** show the real reason
   payment couldn't open (sold out etc.). **M3** send an `Idempotency-Key`
   (one per user edit, reused on retry) on `PATCH seller/variants/{id}/stock`
   (`StockViewModel.kt` ~124-153, `CommerceApi.kt` ~1173); the server now
   honours it (409 `IDEMPOTENCY_CONFLICT` on key reuse with a different
   body, 400 `INVALID_IDEMPOTENCY_KEY` over 128 chars). **M4** seller
   documents: until media-service gets a private `document` kind, accept
   images only (drop `application/pdf` from the picker and ask for a photo).
   **M5** show the upload rate limit (429) as "too many uploads, try again
   in an hour", not "could not start". **M6** cancel the in-flight search
   when the query changes (`StoreBrowseViewModel.kt` ~102-147).
3. **Lows**: L1 bag-row update rollback scoped to its own row; L2 don't
   derive the breakdown on the client; L3 map `ADDRESS_NOT_FOUND` to an
   address message; L4 `FLAG_SECURE` on the payout screen; L5 prefill only
   an empty name field; L6 bound price input length; L7 skip the 180 s
   "Confirming" when no sheet opened; L8 no raw Razorpay errors in logcat,
   file lookups off the main thread, currency symbol from the currency
   field, fix the golden fixture's trailing newline.

**Server contract changes on this branch:**

| Case | Now |
|---|---|
| Payment retry when the order's coupon is used up | 422 `COUPON_USED_UP` (order stays `payment_failed`) |
| `commerce.order.refunded` | only when fully refunded; payload adds `amount_minor`, `refunded_total_minor`, `order_total_minor`, `refund_id`, `fully_refunded` |
| Partial refund | new event `commerce.order.partially_refunded`; order may stay `refund_pending` |
| Review response by a user with no seller account | 403 `NO_SELLER` |
| Delete an address used by an order | 204; the address disappears from the book |
| Stock adjust replay | same response + header `Idempotent-Replayed: true` |

Unit tests for each; report the `android.sh --full` RESULT block.

## Seventh Android lane: food — Feast, kitchen and rider apps (30 Oct 2026)

Audit: [audit-2026-10-30-food-android.md](audit-2026-10-30-food-android.md)
(server side: [audit-2026-10-30-food-backend.md](audit-2026-10-30-food-backend.md),
fixed on this branch). Modules: `feature/feast`, `feature/kitchen`,
`feature/rider`, `app-kitchen`, `app-rider`. One item at a time, unit test
each, `scripts/verify/android.sh --fast :module` while working.

1. **H1 riders miss offers in the background**: `app-rider`'s manifest
   removes the push service and nothing posts to `rider_job_offer`. Restore
   FCM for the rider app (founder: `GOOGLE_SERVICES_JSON`), post a
   high-priority full-screen-intent notification on a job offer, and keep
   the Home list as the in-app view.
2. **H2 kitchen order detail has no items**: call the existing
   `GET /v1/food/partner/orders/{orderId}` from `KitchenApi` (golden-pinned
   on the server) instead of searching the order list; drop the stale
   "route missing" comment.
3. **M1 Feast checkout timeout**: on a timeout / 5xx keep the idempotency
   key and resubmit the same request (the server returns the committed
   order); never reset to an empty cart while an order from this attempt may
   exist; add a "pay for pending order" path. **M2** disable "Go offline"
   while holding a job (the server now keeps an on-job rider ACTIVE and lets
   them verify). **M3** the server now answers a repeated accept by the same
   rider with 200 — treat it as success. **M4** kitchen new-order alarm: a
   foreground service (or FCM) so Android doesn't freeze it; **M5** build
   the Preparing / Awaiting pickup boards from status-filtered queries, not
   the latest 100.
4. **Lows**: L1 offer countdown from the server's `expires_at` minus a
   server-time offset; L2 re-read the cart after an address change and
   compare the bill with the amount charged; L3 cancel the kitchen alarm
   after a restart; L4 `FLAG_SECURE` on bank, PAN/GSTIN, documents and selfie
   screens and mask the re-entered account number; L5 stop polling in the
   background; L6 treat a lost delivery-code response by re-reading the job;
   L7 https-only rider app links with real hosts; L8 the smaller items in
   the audit.

**Server contract changes on this branch (food-service, admin-service):**

| Case | Now |
|---|---|
| Menu item in a category of another restaurant | 404 `FOOD_MENU_CATEGORY_NOT_FOUND` |
| Menu `image_url` | must be platform media (media id or `/v1/media/<id>/serve…`), else 422 `FOOD_IMAGE_URL_INVALID`; an unchanged legacy URL may be resent. The kitchen editor types a URL by hand — upload the photo through media-service instead |
| Order chat by a non-party (incl. a rider before accepting / after delivery) | 403 `FOOD_ORDER_CHAT_FORBIDDEN`; mark-read of another order's message 404 `FOOD_ORDER_MESSAGE_NOT_FOUND` |
| Chat / ticket message blank or over 2000 chars | 400 `FOOD_MESSAGE_BODY_INVALID` |
| Approved rider changes name, phone or vehicle | 409 `FOOD_DELIVERY_PROFILE_LOCKED` (`details.fields`) |
| Reused `Idempotency-Key` with a different request | 409 `FOOD_IDEMPOTENCY_PAYLOAD_MISMATCH`; keyed body over 1 MB 413 |
| Substitution that raises the price | 422 `FOOD_SUBSTITUTION_PRICE_INCREASE`; free text needs `price_diff_paise` ≤ 0; cheaper ones refund the difference |
| Admin cancels a paid order | order goes to `REFUND_PENDING` |
| Rider location ping from a non-active rider / implausible jump | 403 `FOOD_DELIVERY_PARTNER_NOT_ACTIVE` / 422 `FOOD_DELIVERY_LOCATION_IMPLAUSIBLE` |
| Rider goes offline while on a job | stays ACTIVE (online off) and can finish the job |
| Repeated offer accept by the same rider | 200 |
| COD switch after an online attempt | 409 `FOOD_COD_NOT_ALLOWED_FROM_STATE` |
| Invoice | optional `adjustments`, `net_payable_paise` |

Unit tests for each; report the `android.sh --full` RESULT block.

## Eighth Android lane: dating (30 Oct 2026)

Audit: [audit-2026-10-30-dating-android.md](audit-2026-10-30-dating-android.md)
(server side: [audit-2026-10-30-dating-backend.md](audit-2026-10-30-dating-backend.md),
fixed on this branch). Work in `feature/dating` and the notification /
nav code it uses. One item at a time, unit test each,
`scripts/verify/android.sh --fast :module` while working.

1. **H1 safety pushes go nowhere**: route the trusted-contact panic alert
   and the live-location-share push to a screen that shows the incident /
   shared point (`UsNavHost.kt` ~731). The server now sends `title`/`body`
   in `data`, high priority, channel `dating`, and lock-screen copy that
   names no one; show the details only after unlock.
2. **M1** live location: refresh the shared point while sharing and show
   "shared at". **M2** map every dating type (`dating.*`, `dating_*`) to the
   DATING channel in `NotificationChannelSpec.forType`. **M3** screenshot
   protection: treat a failed `/client-config` read as ON
   (`ScreenProtection.kt` ~62-70; the server default is now ON) and add it
   to Safety, past matches, report and every match chat. **M4** trusted
   contacts: no silent `share_location_on_panic=true`
   (`DatingRepository.kt` ~363) — wait for the founder's decision; until
   then send false and fix the copy that says only the safety team sees it.
3. **Lows**: L1 clear `DatingSession` on sign-out and on profile deletion;
   L2 ask for coarse location; L3 keep the premium idempotency key across a
   5xx and mint a new one per pass; L4 read open matches with paging, not
   `status=all` under the 200 cap; L5 after "Report and block" leave the
   chat; L6 the smaller items.

**Server contract changes on this branch:**

| Case | Now |
|---|---|
| Pass / stash a candidate you were never served | 404 `CANDIDATE_UNAVAILABLE` |
| `GET /vouches/for/:userId` for someone you can't see | 404 `CANDIDATE_UNAVAILABLE`; viewers see relationship + note, never `voucher_id` / `community_id` |
| Hidden spark note | `note` is omitted in incoming sparks and liked-you; fetch it with `GET /v1/dating/sparks/:id/note` on tap. **Sync the golden** `src/test/resources/contracts/sparks_incoming_get_200_note_hidden.json` from `Architecture/services/dating-service/internal/http/testdata/contracts/` together with this change |
| Saving a profile with no identity birth date | 403 `IDENTITY_DOB_REQUIRED` (client birth dates are no longer trusted) |
| Aadhaar already linked to another account | 409 `AADHAAR_ALREADY_LINKED` |
| Primary photo changed after a selfie pass | badge drops to phone until a moderator re-checks |
| Send / edit in a dating chat that is closed (block, unmatch, suspension, deletion, restriction, risk hold) | 409 `MATCH_CLOSED` within 15 s |
| Send / edit while either member is under review | 409 `MATCH_ON_HOLD` — show "chat paused", keep the draft; it reopens when the review clears |
| Live location share after unmatch / block | 404 |
| `screen_protection` in client config | true by default |

Unit tests for each; report the `android.sh --full` RESULT block.

### Food lane addendum (31 Oct 2026): customer cancel only before cooking

Founder decision (food audit M5, option a). Customer cancel works only from
PLACED or CONFIRMED; from PREPARING on (including delivered) the server
answers **409 `FOOD_CANCEL_AFTER_PREPARING`** and nothing changes. Customer
order responses (place, list, detail, cancel, payment confirm) carry
`can_cancel`: hide the cancel button when it is false and offer "Request a
refund" instead (`POST /v1/food/support/tickets` with `order_id`, then
`POST /v1/food/refunds` with `order_id`, `amount`, `reason`, optional
`ticket_id`). Re-sync the Feast golden fixtures from
`Architecture/services/food-service/internal/http/testdata/contracts/`
(five gained `can_cancel`; new `order_cancel_post_409_after_preparing.json`).
