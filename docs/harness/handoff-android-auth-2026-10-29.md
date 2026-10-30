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
