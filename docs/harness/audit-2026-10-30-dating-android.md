# Audit: Android dating (Pulse)

Branch `codex/module-01-02-launch-safety`. This was a read-only audit: I made no edits except this file, ran no git writes, no Gradle builds and used no device.
Android paths are relative to `mobile/android/`. Server paths are relative to the repo root.

## Scope

**Modules covered:**
- `feature/dating`, all of `src/main` (73 files): `DatingApi`, `DatingDtos`, `DatingRepository`, `DatingCall` error mapping, `DatingSession`, deck, picks, liked-you, matches, safety, privacy, premium, selfie, clips, location, navigation.
- What dating uses outside its module:
  - `core/common`'s `SecureWindow`.
  - `core/media`'s uploader.
  - `core/payments`.
  - `core/notifications` (`NotificationChannelSpec`, `NotificationPresenter`, `UsMessagingService`).
  - The app's Coil `UsImageLoader`.
  - The dating push routing in `app/.../push/PushDestinations.kt` and `app/.../navigation/UsNavHost.kt`.
- There is no separate dating chat screen. A Pulse match's chat is the ordinary `feature/chat` thread, opened through `onOpenChat`. `core/chat` keeps it out of the Messages inbox (`ChatStore.kt:296-316`). I read only its dating hooks: kindness and report-sender.

**Server side checked:**
- `Architecture/services/dating-service/internal/http/handler.go:62-338` (route registration), plus the handlers and stores behind each finding.
- The `/v1/dating` entry and pilot gate in `api-gateway/cmd/server/main.go:731-732, 1346, 1368`.
- `chat-service/services/message-service/internal/http/handler.go:137-153`. Every dating route there is internal (`/internal/v1/chat/conversations/dating-match*`, `/dating-match/state|turns`). The app calls none of them, and none is reachable through the gateway.
- notification-service:
  - `internal/events/dating_safety.go`, `dating_pulse.go`, `chat_consumer.go`.
  - `internal/service/safety_alert.go`, `push_data.go`, `notification.go`.
  - `internal/push/fcm.go`.

**Contract checks:**
- Every Retrofit path in `DatingApi` (about 95 routes) matches a gin route in `handler.go`. No client route is retired or missing:
  - `/premium/plans`, `/premium/checkout`, `/premium/cancel`, `/matches/:id/first-message` and `/risk/:userId` are retired or moved server-side, and the app calls none of them.
  - Server routes the app does not call: `/pulse/nebula`, `/pulse/boost`, `/tune`, `/profile/intent`, `/vouches*`, `/verification/aadhaar/*`, `/safety/meet*`, `DELETE /sparks/:id`.
- All 162 golden fixtures in `feature/dating/src/test/resources/contracts/` are byte-identical to dating-service's `internal/http/testdata/contracts/` (`cmp`, 0 diffs, none missing on either side).
- Every upper-case error code the client branches on exists server-side. `TOO_MANY_LANGUAGE` is built as `"TOO_MANY_"+code` in `service/profile_options.go:142`.
- I also field-checked the DTOs marked "no fixture" for safety (block, report, panic, trusted contacts, shares) against the Go JSON tags (`handler_safety.go`, `store/location_shares.go:33-43`, `store/trusted_contacts.go:27-32`). They match.
- Each finding below was traced end to end. Anything I could not confirm was dropped or moved to "Not verified".

---

## HIGH

### H1. A trusted contact's panic alert, and a "sharing their live location" alert, open nothing, and the panic location can never be seen in the app
- **Files:**
  - Server: when a member presses panic, `dating_safety.go:201-229` sends each trusted contact a `dating.safety.panic.trusted_contact` push: "*Name* triggered a safety alert / Check on them now. Tap to see where they were."
    - The location exists only in the deep link, `/dating/safety/alerts/<incident>?lat=..&lng=..` (`:213-217`). No member-facing route serves the incident: `handler.go` has only admin `/admin/safety/panic*`.
    - `dating_safety.go:296-317` sends `dating.safety.location_shared` with the deep link `/dating/safety/shared-locations/<id>`.
  - Android tap routing:
    - `PushDestinations.datingTargetOf` (`app/.../push/PushDestinations.kt:145-157`) knows only spark, match, message, first message, scam alert and date check-in.
    - `UsNavHost.openPushDestination` sends both safety types to `else -> Unit` (`UsNavHost.kt:731`; the KDoc at `:636` says "Unknown types route nowhere").
    - Nothing in `feature/dating` parses `/dating/safety/alerts/...`. There is no screen for an incoming panic alert.
  - Foreground: `CreateSafetyAlertNotification` (`notification-service/internal/service/safety_alert.go:84-93`) puts no `title`/`body` in `data`. `UsMessagingService.onMessageReceived` (`core/notifications/.../UsMessagingService.kt:82`) passes only `data` to `NotificationPresenter.present`, which returns at `val title = data[KEY_TITLE] ?: return` (`NotificationPresenter.kt:68`). A contact who has the app open gets no tray alert at all.
  - Delivery: these pushes are not in `highPriorityTypes` (`push/fcm.go:107-119`), so on a dozing phone FCM may hold them until a maintenance window.
- **Scenario:** Priya adds her sister as a trusted contact. `addTrustedContact` always sends `share_location_on_panic=true`; see M4. On a date, Priya presses panic.
  - The sister's phone is in her bag, dozing. The normal-priority push arrives late.
  - She taps it. Momentum opens on its home tab, not Priya's alert. The coordinates in the tap's deep link are dropped, and there is no screen to find them on later.
  - If the sister has the app open instead, nothing appears in the tray.
  - The share alert's tap is the same: she has to know to go to Dating, then Safety, then "shared with you".
- **Fix:**
  - Client: add `dating.safety.panic.trusted_contact` → a new `DatingPanicAlertRoute(incidentId, lat?, lng?)` that shows "*Name* needs help, last known here" with an "Open in maps" action. Add `dating.safety.location_shared` → `DatingSharedLocationRoute(shareId)`, which already exists. Parse ids as UUIDs and coordinates as bounded doubles.
  - Server: send `title`/`body` in `data` for safety alerts, as `push_data.go` does for every other type. Add both types to `highPriorityTypes`. Better still, serve a member-readable `GET /safety/alerts/:id` (recipient-scoped, time-limited) and drop the raw coordinates from the deep link. They currently sit in the inbox row and in the FCM payload.

---

## MEDIUM

### M1. "Live location" is one fix taken when the share starts, and the screen calls it live
- **Files:**
  - `safety/SafetyViewModels.kt:240-262`: `fetchAndShare()` takes one `getCurrentLocation`, sends one `POST /safety/share-location` and says "Sharing your location for $minutes minutes." Nothing ever sends a newer point.
  - The server has no update route, only create, list, get and stop (`handler.go:231-235`). `service/safety.go:333` describes it as "the sharer's exact point".
  - Recipient: `SafetyScreens.kt:254-266` titles the point "Live location shared with you". "Refresh" re-reads the same stored point. `SharedLocationDto.createdAt` (`DatingDtos.kt:1324`) is never shown, so the reader cannot tell the point is 90 minutes old.
  - The section header is "Share your live location" (`SafetyScreens.kt:143`).
- **Scenario:** Before a date, someone shares for 2 hours with a friend from the café. They then leave in the date's car. The friend checks at minute 60, taps Refresh, and sees the café pinned as "live". If something has gone wrong, the friend looks in the wrong place.
- **Fix:**
  - Short term: rename it everywhere to "Share where I am now". Show "Shared at 19:42 (58 min ago)" from `created_at` on the recipient screen. Drop "live" from the copy and from the push title (`dating_safety.go:297`).
  - Real fix: a foreground service of type `location` that, while a share is active, PUTs a point every 1-2 minutes to a new `PUT /safety/share-location/:id` (server-capped, stops on expiry or Stop). This needs a disclosure like the rider app's.

### M2. Dating pushes ignore the private "Dating" channel when the app is in the background, and the safety and check-in ones name people on the lock screen
- **Files:**
  - `NotificationChannelSpec.DATING` (`core/notifications/.../NotificationChannelSpec.kt:246-252`) is `privateOnLockScreen = true`. That applies only to notifications the app draws itself.
  - `push/fcm.go:126-189`: every non-call dating push carries an FCM `notification` block. The system renders it in the background on `android.notification.channel_id`, which is set only from `android_channel_id`.
  - No dating sender sets `android_channel_id`: `push_data.go` sets it only for `dm` and `message_request`, and `safety_alert.go` never does. The app manifest declares no `default_notification_channel_id`. So background dating pushes land on Firebase's fallback "Miscellaneous" channel, which has no lock-screen restriction.
  - Even in the foreground, `NotificationChannelSpec.forType` (`:585`) maps only four dating types to `DATING`. `dating_date_checkin`, `dating_scam_alert` and `dating.safety.*` fall to `SOCIAL`, which is not private.
  - Copy that names people:
    - "Tell us how your date with *Name* went" (`notification-service/internal/service/dating_pulse.go:71`).
    - "*Name*, who you matched with on Pulse, was removed for scam behaviour" (`:62`).
    - "*Name* triggered a safety alert" (`dating_safety.go:203`).
    - "*Name* is sharing their live location with you" (`:297`).
- **Scenario:** A user keeps their dating life private from family. Their phone is on the table, locked. "How did it go? Tell us how your date with Rahul went" appears in full on the lock screen. Separately, someone who silenced the "Dating" channel still gets every background dating push, because those posts are on "Miscellaneous".
- **Fix:**
  - Server: set `android_channel_id: "dating"` on every `dating.*` / `dating_*` push in `push_data.go` and `safety_alert.go`. For the named types, put the name only in the body and keep a neutral title ("Pulse"), so the channel's `VISIBILITY_PRIVATE` redaction ("Contents hidden") covers it.
  - Client: map every `dating.` / `dating_` prefix to `DATING` in `forType`. Set `setVisibility(VISIBILITY_PRIVATE)` plus a neutral `setPublicVersion` on the dating builder in `NotificationPresenter`, not just on the channel.

### M3. Screenshot protection is off in production by default, fails open, and never covers the match chat
- **Files:**
  - `safety/ScreenProtection.kt:62-70`: protection is on only when `GET /client-config` returns `screen_protection: true`. Any failure reads as off (`:68`).
  - Server: `DATING_SCREEN_PROTECTION_ENABLED` defaults to `payments.IsLocalEnv(ENV)` (`dating-service/internal/http/boot_config.go:467, 526`), so it is false in dev and prod unless someone sets it.
  - `ProtectThisScreen()` is placed only on the home tabs, match detail and person (`HomeScreens.kt:102, 330`, `PersonScreens.kt:198`). It is not on:
    - Safety: trusted contacts' and recipients' photos and names, and shares.
    - Past matches.
    - The report screen.
    - The shared-location screen.
  - The KDoc (`ScreenProtection.kt:35-36`) leaves the chat to the chat lock. An unlocked Pulse chat, which is where people send photos and numbers, is never protected.
- **Scenario:** A match screenshots or screen-records the conversation and the other person's full photos, then posts them. Nothing in the app prevents it, because the flag was never turned on. Even with the flag on, one flaky `client-config` read leaves the whole session unprotected (it is cached per visit, `:62-65`).
- **Fix:**
  - Decide the policy explicitly: the founder's call.
  - If protection is wanted: default the server flag to true, and make the client fail closed (protect while unknown, release only on an explicit `false`).
  - Add `ProtectThisScreen()` to Safety, Past matches and the report screen.
  - Have the chat thread hold `SecureWindow` for `isDatingChat` conversations regardless of chat lock. `ConversationKindness` already tells `feature/chat` which chats are Pulse.

### M4. Every trusted contact is silently opted in to receive the exact panic location, and the screen says only the safety team sees it
- **Files:**
  - `data/DatingRepository.kt:362-363` always sends `TrustedContactRequest(shareLocationOnPanic = true)`. No UI toggles it.
  - The panic card's copy (`SafetyScreens.kt:99`): "This alerts our safety team straight away. If you've allowed location, they'll see where you are."
  - Trusted contacts may be matches (`SafetyViewModels.kt:113-125` offers matches as candidates). The server pushes the point to each opted-in contact (`dating_safety.go:213-217`).
- **Scenario:** A user adds a recent match as a trusted contact for a first date. Weeks later, uneasy around that same person, they press panic. That match receives their exact coordinates, which the copy never said would happen.
- **Fix:** Add a per-contact "Send them my location if I press panic" switch, default off for a match and on for a connection, wired to the existing field. Change the panic copy to "...our safety team and your trusted contacts who can see your location".

---

## LOW

### L1. Sign-out leaves the last account's blocks, profile and consents in `DatingSession`, and deleting the profile leaves the device data that sign-out clears
`DatingTeardown.onSignOut` (`safety/DatingTeardown.kt:18-22`) never calls `DatingSession.clear()`. `clear()` (`DatingSession.kt:116-120`) also skips `_removed`. The process outlives sign-out, which runs through `SessionTeardownTask` and does not kill the process. If account B signs in on the same phone, every person account A blocked or reported stays filtered out of B's deck, sparks and matches until the process dies. `session.profile` and `myUserId` stay A's until the root gate reloads, and a push that opens straight into match detail can use A's id in `otherOf` (`HomeViewModels.kt:987-989`).

The reverse gap: `PrivacyViewModel.deleteProfile` (`privacy/PrivacyScreens.kt:185-190`) calls only `session.clear()`. It leaves `PrefsKindAnswerStore`'s on-disk answers, the conversation→match map and the screen-protection cache until sign-out.

**Fix:** have `onSignOut` call `session.clear()`, and have `clear()` also reset `_removed`, the filters and travel versions. Run the same teardown after a successful profile delete.

### L2. Onboarding asks for precise location for a point the server rounds to ~1 km
`OnboardingScreens.kt:305` launches `[ACCESS_COARSE_LOCATION, ACCESS_FINE_LOCATION]`, so Android offers "Precise" pre-selected. The fix is a `BALANCED_POWER_ACCURACY` one-shot (`DatingLocation.kt:132`), and the server snaps to 0.01° (`dating-service/internal/store/geo.go:199-214`). The precise grant is then kept for the whole app. **Fix:** request only `ACCESS_COARSE_LOCATION` here. Ask for fine location only inside Safety (`SafetyScreens.kt:237`), where panic and the share legitimately need it.

### L3. Premium: a 5xx after "Buy" drops the key, and a kept key is reused for a different pass
`PremiumViewModel.submit` (`premium/PremiumViewModel.kt:165-183`) resets `phase = FRESH` on any failure and keeps the key only for `DatingError.Network` (`:174`). Two problems follow:
- A gateway 502/504 after the server committed the purchase loses the key, and the next tap creates a second `pending` purchase.
- After a real network failure the key is kept, but the catalogue is live again. Picking a *different* pass sends the old key with a new product, which gets 409 (`ErrIdempotencyKeyReused`, `dating-service/internal/store/premium.go:27-29, 94`). That clears the key and shows an error, so the buyer has to tap twice.

No money moves twice: a sheet opens only after a purchase response, and status is server-polled. **Fix:** keep the key and stay in `SUBMITTING` on `Network`, on `Unexpected(status>=500 or null)` and on 409 key-reuse. Lock the other products until the resend settles. Clear the key only on a definite 4xx.

### L4. Matches are read as `status=all` under a 200-row cap that includes closed matches
`DatingRepository.matches()` (`:270`) calls `GET /matches` with no status. The server orders all of the caller's matches, closed included, and stops at `LIMIT 200` (`dating-service/internal/store/matches.go:279-288`). The client then drops `closed` rows (`HomeViewModels.kt:911`). Three places depend on this list:
- The Matches tab.
- The Safety screen's recipients (`SafetyViewModels.kt:113`).
- The kindness check that decides which chats are Pulse chats (`KindMessages.kt:154`).

A long-time user with many expired or closed matches stops seeing older active ones. **Fix:** add an "open" bucket server-side (`matched`, `conversing`, `quiet`) and ask for it. Or page the list.

### L5. "Report and block" from a Pulse chat returns to that open chat
`DatingReportRoute` pops one level on Done (`navigation/DatingNavigation.kt:209-211`, `safety/ReportPerson.kt:119`), back to the `feature/chat` thread. The thread has no dating-closed handling (no `isDatingChat` or closed check in `ChatThreadViewModel`). The person just blocked stays on screen with a live composer until the user backs out. **Fix:** on SENT, pop past the chat thread to Matches (`popUpTo<DatingRootRoute>`), or have the thread close itself when the match closes.

### L6. Smaller items
- **Unblock does not clear the session's hidden set.** `BlocksViewModel.unblock` (`PrivacyScreens.kt:262-277`) does not undo `DatingSession.removePerson`. That is consistent with "restores nothing", but someone unblocked can never reappear in the deck until the app restarts. State it in the confirm copy, or clear the entry.
- **A failed report from match detail loses the draft.** In match detail the report sheet closes before sending (`HomeScreens.kt:485-488`). A failure shows a snackbar, and the user's reason and details are gone. `ReportPersonViewModel` already keeps the draft on failure; reuse that pattern.
- **Raw coordinates in the panic deep link.** The panic deep link carries six-decimal coordinates (`dating_safety.go:216`). They are stored in the notification inbox row and sent through FCM. See the server fix in H1.

---

## Checked and fine
- **Error mapping:**
  - `DatingError.from` tells the gateway pilot's 404 (no `meta`) from dating-service's own 404 and maps it to `NotAvailable`.
  - 401 maps to `Unauthorized`, 422 `CONSENT_REQUIRED` gives the typed consent flow, and everything else is `Refused(code)`. Screens branch on `code`, never on the message.
  - Only `CancellationException` escapes `datingCall`.
- **Paging:** liked-you pages with `limit`/`offset`, and the server returns `total`. The deck refill follows the server's `remaining_today`, not a local count.
- **Age gate:** server-side from identity's verified DOB (`dating-service/internal/service/profile.go:15-18, 83-96`). The client's 18 is only the filter slider floor.
- **Selfie:**
  - A server challenge comes first.
  - The clip is recorded to `cacheDir/dating-selfie` and deleted after upload or failure (`SelfieVideoCamera.kt:163, 197`, `SelfieVideoUploader.kt:60`).
  - Verdicts come only from the server. `SELFIE_ALREADY_PASSED` and `MEDIA_NOT_READY` resubmit without spending an attempt.
- **Paid features are server-checked:**
  - Liked-you `unlocked` comes from the server, and a locked tile is a separate type with no name field.
  - `/sparks/incoming` returns `LockedIncomingSpark` rows when locked (`dating-service/internal/service/liked_you.go:122-135`), and the client drops them.
  - The locked photo route only ever redirects to the blurred variant.
  - Pass filters follow `pass_filters.active`.
  - Super Spark is charged in the spark transaction.
  - Purchase status is polled and never trusted from the sheet. Process death asks the server first.
- **Swipes and likes:**
  - One deck action at a time (`act`, `HomeViewModels.kt:554-571`), and Picks has the same guard.
  - A spark is idempotent server-side (UNIQUE plus `ON CONFLICT`, `dating-service/internal/store/sparks.go:155-176`), and a repeat Super Spark is not charged twice (`store/super_spark.go:33-47`). A double tap or a retry cannot duplicate a like or spend twice.
  - The opening answer uses a server-derived key per match and sender.
  - There is no offline queue: failures spring the card back and say so.
- **Block and report:**
  - On server confirmation the person is removed from every list through `DatingSession.removed`. Every list re-filters on each emission, so a stale reload cannot bring them back.
  - Match detail re-reads and goes to `Gone`.
  - The server closes the match and the conversation.
- **Location privacy:**
  - Each fix is a one-shot balanced `getCurrentLocation`. There is no background location, no `ACCESS_BACKGROUND_LOCATION` and no service.
  - The server snaps to about 1 km and rate-limits changes to 1 per 15 min and 10 per day.
  - Others see only distance buckets: `lt_5_km`, `km_5_10`, `km_10_25`, `gt_25_km`. Explain is limited to 60 a day. Travel takes a city name, not coordinates. Together this makes trilateration impractical from the client.
  - Panic sends a location only if permission was already granted.
- **Photos and media:**
  - `DatingPhoto` disables Coil's disk cache (`ui/DatingUi.kt:376-380`), and the shared OkHttp client has no HTTP cache, so dating photos never land on disk. The memory cache is keyed by the gateway path, never by the 120 s signed URL.
  - Unmatched viewers get the blurred variant. The check fails closed on any unknown `photo_state`.
  - Photos come through `PickVisualMedia`, so MediaProvider redacts GPS without `ACCESS_MEDIA_LOCATION`. media-service also re-encodes and strips EXIF and XMP (`media-service/internal/processing/strip_metadata.go`), and ffmpeg strips video metadata.
  - Voice clips go to `cacheDir` and are deleted.
  - The data export is streamed to a user-chosen `CreateDocument` target, with no app copy.
- **Backup and logging:** `allowBackup=false`, `fullBackupContent=false` and data-extraction rules are set in `app`. No `Log`, `Timber` or `println` call anywhere in `feature/dating`.
- **Spark, match and message pushes:** these use generic copy ("New Notification"; `notifTitleBody` has no dating case), so they name no one. Only the four types in M2 do.
- **Performance and memory:**
  - The deck composes only the top and next card (`PulseDeck.kt:387, 405`), so preloading is one card ahead.
  - Images are sized to layout.
  - Clip players are created on first Play and released when the card leaves, with every callback after release ignored.
  - Lists are lazy with keys. Allowances are refreshed on events, not polled.

## Not verified
- Whether `ChatStore.deleteConversation` also removes the cached *messages* of a Pulse chat. `cacheMessages` (`core/chat/.../ChatStore.kt:407-411`) writes thread history to Room for any opened conversation. If those messages survive an unmatch or block until sign-out, a closed match's chat history stays on the device.
- What message-service answers to a send in a dating conversation closed by block or unmatch, and whether the open thread learns of the close in real time (L5).
- Whether media-service keeps the uploaded original (with EXIF) after processing, or only the stripped variants.
- Whether a repeat spark to someone who already matched returns `matched=true` with the match id, so a retried spark after a lost response still shows the celebration.
- How each OEM shows the Firebase fallback channel on the lock screen by default (M2). I reasoned from platform defaults, not a device.
