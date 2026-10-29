# Audit: gateway token verification + Android auth clients

Branch `codex/module-01-02-launch-safety` (checked out as `...-9erjug`, clean tree). Read-only audit; no edits, no git writes, no servers, no Gradle.
Unit tests run: `go test ./...` in `Architecture/services/api-gateway`, all packages pass (they do not cover the defects below).

Severity scale: critical / high / medium / low. Every finding below was traced end to end in code; anything I could not confirm was dropped.

---

## HIGH

### H1. Android users are signed out every time an access token expires in a live process (stale `access_token` cookie makes `/v1/auth/refresh` fail at the gateway)

Files:
- `mobile/android/core/network/src/main/kotlin/com/us/android/core/network/cookie/CsrfCookieStore.kt:28-40` stores every cookie the server sets (not just `csrf_token`) and `loadForRequest` returns `cookies.values.filter { it.matches(url) }`. OkHttp's `Cookie.matches()` checks domain, path and `secure`, not expiry (its docs tell callers to check expiry themselves), so expired cookies keep going out.
- `identity-platform/services/auth-service/internal/http/handler.go:1361-1372` (`setAuthCookies`) sets `access_token` (Path=/, Expires = token expiry) on login (`:908`), refresh (`:936`), OTP verify (`:599`) and partner verify (`partner_phone.go:75`).
- `mobile/android/core/network/src/main/kotlin/com/us/android/core/network/interceptor/Interceptors.kt:72-77,98-104` sends no `Authorization` on `/v1/auth/refresh`.
- `Architecture/services/api-gateway/cmd/server/main.go:914-947`: with no Bearer header, the gateway falls back to the `access_token` cookie, and any token that fails verification gets **401** before routing, on every path including `/v1/auth/refresh`.
- `mobile/android/core/auth/src/main/kotlin/com/us/android/core/auth/SessionManager.kt:143-155,163-164`: a 401 on refresh counts as a definitive rejection, so `clearSession()` runs.

How it fails: the app is open, or backgrounded but still alive, past the access TTL (15 min in prod, `config.go:197`). The next call carries the expired Bearer, the gateway answers 401, and `TokenAuthenticator` calls refresh. The refresh request carries the jar's `access_token` cookie, which holds the same expired token because each refresh overwrote it. The gateway verifies the cookie, rejects it, and answers 401. SessionManager wipes the session and the user lands on Login. It only works after a cold start, because the jar is in memory and starts empty.

The `HeaderContractTest` "session cookies ... are ignored" test only asserts the Authorization header. It never looks at the `Cookie` header that is actually sent.

Fix (both sides):
- **Android:** make the jar keep only `csrf_token` and drop expired cookies on load.
- **Server:** send the refresh token in the JSON body for logout too (see L8), since server-side logout currently works only because the jar carries `refresh_token`.
- **Gateway, defence in depth:** on the public `/v1/auth/{login,register,refresh,logout,...}` routes, treat a credential that fails verification as anonymous instead of 401. The same 401 also blocks web refresh and login whenever a browser holds a not-yet-expired cookie the gateway cannot verify, for example after an RS256 key change (M2).

### H2. Networking can deadlock for good on cold start: the refresh call queues behind the 401'd calls that are waiting on it (OkHttp per-host limit)

Files:
- `mobile/android/core/network/src/main/kotlin/com/us/android/core/network/TokenAuthenticator.kt:42`: `runBlocking { tokenRefresher.refresh() }` on the OkHttp dispatcher thread.
- `SessionManager.kt:94-105,119`: refresh is `authApi.get().refresh(...)`, a Retrofit `suspend` call, which goes through `Call.enqueue` on the **same** `@AuthenticatedClient` (`NetworkModule.kt:118-157`, no custom `Dispatcher`, so `maxRequestsPerHost = 5`).
- `SessionManager.kt:174` `shouldProactivelyRefresh` has no production caller (grep), so after process death every authenticated request goes out with no token and gets 401 (`Interceptors.kt:75-76`). For example, feed-service `GetHomeFeed` and `GetReelFeed` return 401 with no `X-User-Id`.

How it fails: on cold start, five or more concurrent async calls to the API host (feed, badges, module prefs, push registration and so on) all 401. Each one sits in `authenticate()`, holding its per-host slot while it blocks on the single in-flight refresh. The refresh's own `enqueue` lands in `readyAsyncCalls` behind them and can never be promoted, because no slot for that host ever frees. A queued call has not entered its timeout yet, so `callTimeout` does not fire for the refresh. Cancelling the blocked calls (callTimeout, or ViewModel cancellation) closes their exchange but does not wake `runBlocking`. The API host is stuck until the process dies. `SessionManagerTest` "N concurrent refreshes collapse" calls `sm.refresh()` directly and never goes through the real client and Authenticator, so it cannot see this.

Not executed (no Gradle per instructions). This rests on OkHttp's documented Dispatcher and Authenticator behaviour and Retrofit's `enqueue`-based suspend adapter.

Fix:
- Run refresh on a separate client: `client.newBuilder().dispatcher(Dispatcher()).authenticator(Authenticator.NONE)`, or make the refresh a synchronous `execute()`, which the per-host async limit does not apply to.
- Mint the access token before the first request on cold start, i.e. wire `shouldProactivelyRefresh`.
- Add a test that sends 6 or more concurrent 401s through the real client.

---

## MEDIUM

### M1. The gateway's per-IP rate limit keys on client-supplied `X-Real-IP` / leftmost `X-Forwarded-For`

Files: `Architecture/services/api-gateway/cmd/server/main.go:1070-1084` (in-memory) and `cmd/server/ratelimit_redis.go:100-116` (`clientIPForRateLimit`, Redis).

How it fails: the AWS ALB appends the peer address to XFF and does not set `X-Real-IP`. A client that sends `X-Real-IP: <random>` per request, or a forged first XFF hop, gets a fresh bucket every time. That bypasses the 100 rps per-IP limit, which is the only edge throttle on `/v1/auth/*` (login, OTP, 2FA, reset). Each forged key also adds an entry to the in-memory `rateLimiterStore` (pruned only after 10 minutes idle), so a distributed sender can grow gateway memory without bound. The gateway also forwards the forged XFF upstream unchanged.

Mitigation present: the prod WAF rate rule (1000 per 5 min per real IP, `infra/terraform/envs/prod/main.tf:205`). Staging has no WAF annotation (`deploy/services/api-gateway/values-staging.yaml`).

Fix: derive the client IP from `RemoteAddr` plus a configured trusted-proxy count (the rightmost untrusted XFF hop), ignore `X-Real-IP` unless a trusted proxy sets it, and overwrite or strip the inbound XFF before proxying.

### M2. Rotating the RS256 key logs everyone out: the gateway accepts exactly one RSA key

Files: `cmd/server/main.go:85-93` (`rsaKeys = {JWT_RS256_KID: pub}`), `pkg/tokenpolicy/verify.go:63-77`. auth-service also signs with one key (`identity-platform/.../config/config.go:226-227`). There is no previous-RSA-key setting and no JWKS fetch. HS256 has an active/previous pair, but HS256 is refused in production.

How it fails: during a key change, whichever side deploys first breaks the other. Every outstanding access token (15-minute window), or every newly minted one, returns 401 `unknown kid`. Combined with H1, web clients holding the old cookie cannot even reach `/v1/auth/refresh`. The "key-rotation path" the C7 comments describe exists only for HS256.

Fix: accept a second public key (`JWT_PUBLIC_KEY_PEM_PREVIOUS` + `JWT_RS256_KID_PREVIOUS`, or a static JWKS document listing both kids) and document the order: publish the new public key, then switch signing, then drop the old key after one TTL.

---

## LOW

### L1. `/metrics` is served to the internet
`cmd/server/main.go:334-337` serves Prometheus metrics in `newCoreHandler` before any route or auth check. The prod ingress routes `/` to the gateway (`values-prod.yaml:175-176`) and the WAF has no path rule. Anyone can read runtime and revocation counters (`gateway_session_revocation_check_failures_total` and others), which helps an attacker time a Redis outage, because consumer revocation fails open. Fix: serve metrics on a separate internal port, or refuse `/metrics` unless the request comes from the cluster network.

### L2. CORS responses do not send `Vary: Origin`
`cmd/server/main.go:1142-1157` reflects the request `Origin` with `Allow-Credentials: true` and never sets `Vary: Origin`. A shared cache or CDN in front of the API can then serve one origin's ACAO (or none) to another origin, so legitimate cross-origin calls break. Fix: always add `Vary: Origin`. (The origin allowlist itself is explicit in prod and staging values, which is sound. `*` only produces a warning; consider refusing it in production.)

### L3. The gateway forwards the `refresh_token` cookie to every upstream
`newRoute` / `httputil.ReverseProxy` passes the whole `Cookie` header through. Browsers, and Android because of H1, send `refresh_token` (Path=/) on every API call. So post-, media-, feed- and all other services receive a 30-day credential they never need, and any header logging or upstream compromise leaks it. Fix: strip `refresh_token` (and `access_token` once it has been consumed) from `Cookie` for every prefix except `/v1/auth`.

### L4. A late 401 runs a second refresh even when a fresh token already exists
`TokenAuthenticator.kt:38-47` calls `refresh()` before comparing the failed token with `currentAccessToken()`. Any 401 that arrives after the first refresh has finished (and `inFlightRefresh` has been reset, `SessionManager.kt:101-105`) rotates the refresh token again. This is the common case on cold start, where the request carried no token at all. The cost is extra auth-service writes and latency. There is no correctness loss, because rotation keeps the sid. Fix: if `currentAccessToken()` is non-null and different from the failed token, retry with it without refreshing.

### L5. A refresh still in flight can sign the user back in after logout, or overwrite a newer login
`SessionManager.adoptTokens` (`:227-241`) writes tokens unconditionally. If a refresh was sent before `logout()` and its 200 arrives after `clearSession()`, the logged-out user is Authenticated again with a fresh refresh token stored. The reverse also happens: an old refresh's 401 arriving after a new login wipes the new session. Fix: keep a session generation counter that `clearSession` and `applyAuthResponse` bump, and discard refresh results from an older generation.

### L6. A malformed OAuth redirect crashes the app; process death mid-OAuth always fails
`core/auth/.../oauth/GoogleAuthorizationFlow.kt:195-201`: `parseQuery` calls `URLDecoder.decode`, which throws `IllegalArgumentException` on a bad escape such as `%zz` or a trailing `%`. `complete()` does not catch it, and `LoginViewModel.finishGoogleSignIn` (`feature/auth/.../login/LoginViewModel.kt:165-176`) has only `try/finally` inside `viewModelScope.launch`, so the exception reaches the uncaught handler and the app crashes. `MainActivity` (exported, custom scheme `${GOOGLE_OAUTH_REDIRECT_SCHEME}`) offers any matching URI (`MainActivity.kt:128,197`), so another app or a tapped link can crash the app while Login is showing.

Separately, `attempt` (verifier and state) lives only in memory, so if the browser hop kills the process the redirect always ends in `OAUTH_STATE_MISMATCH`.

Fix: catch decoding errors in `parseQuery` and return `CODE_STATE_MISMATCH`. Persist the attempt (state + verifier) in `SavedStateHandle` or encrypted prefs with a short TTL.

### L7. A wrong partner-phone OTP says "That email or password isn't right"
`feature/auth/.../partner/PartnerPhoneRoute.kt:103` maps verify failures through `AuthErrorText.forLogin`. The server answers a wrong or expired code with 401 `AUTH_FAILED` (`identity-platform/.../http/partner_phone.go:71`), and `forLogin` turns that into the email/password sentence on an OTP screen. Also, a `requires_2fa` or `requires_step_up` result leaves the global session in `PendingTwoFactor`/`PendingStepUp` with no route to finish (`:105`). Fix: use an OTP-specific text, and route the pending states the way `LoginViewModel.applySession` does.

### L8. Server-side logout depends on the cookie jar by accident
`AuthApi.logout()` (`AuthApi.kt:86-87`) sends no body. auth-service `Logout` reads the refresh token **only** from the cookie (`handler.go:939-941`) and returns 200 no-op when it is empty (`service/auth.go:1000-1003`). Logout revokes the session today only because the jar (H1) carries `refresh_token`. Fixing H1 on the client alone would silently turn every Android logout into a no-op, leaving the refresh token valid server-side. Fix: accept `{refresh_token}` in the logout body (mirroring `/refresh`) and have Android send it before `clearSession()`.

### L9. The verify-email resend cooldown restarts and stacks on recomposition
`feature/auth/.../verify/VerifyEmailScreen.kt:37-38` calls `viewModel.start()` from `LaunchedEffect(verificationToken)`. Each new composition (rotation, return to the screen) launches another `startResendCooldown()` loop (`VerifyEmailViewModel.kt:130-139`). The timer resets to 60 and then counts down 2x or 3x faster. The server's 429 still protects the endpoint. Fix: make `start` idempotent, or cancel the previous cooldown `Job`.

### L10. Observation: the prod WAF per-IP budget is very small for CGNAT mobile traffic
`infra/terraform/envs/prod/main.tf:205` allows `rate_limit_per_5min = 1000` per source IP, about 3.3 rps averaged. Indian mobile carriers put many subscribers behind one CGNAT address, so a busy shared IP can be blocked for everyone behind it. This is config, not code; worth load-testing before launch.

---

## Checked and sound

Gateway (`Architecture/services/api-gateway`):
- **Algorithm:** a whitelist of HS256/RS256 only, so `none` and alg confusion are refused. The header alg is re-checked by policy, and HS256 is refused when `Production` (`verify.go:135-156`, `policy.go:199-208`). The prod values set `ENV=prod` and `APP_ENV=production`, and `LoadFromEnv` refuses to boot without issuer, audience and public key.
- **Claims:** `exp` is mandatory; `nbf` is honoured; 60s skew on both; `iss` allowlist; `aud` string or array; `typ` must be an access token (and is required in prod); `sub` must be a strict UUID; `sid` is required in prod. HMAC compare is constant time. RS256 uses PKCS1v15/SHA-256.
- **kid lookup:** unknown kid is rejected. An RS256 token with an empty kid resolves only when exactly one key exists.
- **Identity headers:** all trusted headers (X-User-Id, X-Verified-User-Id, X-Scopes, X-Admin-Role, X-Device-Id, X-Internal-Service-Key, X-Admin-Id, X-Internal-Key, X-Auth-Time, X-Admin-MFA, X-Step-Up-At, X-Graph-Write-Source) are stripped case-insensitively on every request before verification, then re-stamped only from a verified token. The internal key is removed and re-applied per route. I inventoried the X-* headers the backend reads; none trusted by a service is missing from the strip list. `X-Service-Authorization` is a signed, audience-scoped token, and `X-Caller-Service` is an audit label only.
- **Internal and admin paths:** `/internal` segments are refused under every decoding and cleaning variant. `/v1/admin` takes the token only from the admin cookie and requires `sk=admin` plus a constant-time double-submit CSRF check. A consumer token is refused on admin paths and an admin token everywhere else. `/v1/payments` and `/v1/auth/internal` are blocked at boot and per request.
- **Query tokens:** an exact or segment-boundary allowlist (`/v1/ws` only in prod).
- **Unauthenticated routes:** probes, `/v1/features` and anonymous pass-through are by design. Upstreams enforce auth on X-User-Id plus the internal key. `/metrics` is the exception (L1).
- **Revocation:** fails closed for privileged requests and open for consumers, as documented. Redis is required in prod.
- **Limits:** body cap, ReadHeaderTimeout, IdleTimeout and header cap.
- **Outages:** there is no remote JWKS, so a JWKS outage cannot affect verification. If auth-service is down, the proxy answers 502, and Android treats that as transient and does not log the user out.
- **Shared middleware:** `RequireInternalKey` refuses an empty secret and compares in constant time.

Android:
- **Token storage:** the refresh token is AES-256-GCM under an Android Keystore key, and the IV is random per write. The access token is memory-only. Prefs are `MODE_PRIVATE`, `allowBackup=false` and `fullBackupContent=false`, and an undecryptable token forces a clean re-login.
- **Logging:** HttpLoggingInterceptor runs at BASIC in debug and NONE in release, and redacts `chat_open`. I found no `Log.*` or `println` in the auth, network or app code paths. Tracing records `url.path` only.
- **Bearer scope:** the token is attached only when host and port match the API origin. Presigned uploads use the bare client.
- **Cleartext:** denied in the `main` network security config of all five apps. It is allowed only in `dev` flavour overrides.
- **OAuth:** authorization code + PKCE (S256) with a 128-bit `state`; single-use attempt; no client secret.
- **Refresh:** concurrent refresh calls collapse; no refresh-of-refresh loop; MAX_ATTEMPTS=2. 401 and 403 are told apart from 429, 5xx and IO.
- **Logout clean-up:** logout and deactivate run teardown tasks (push, chat, analytics, dating, offline feed) while the session is still valid, then clear tokens and cookies. Module prefs are cleared on Unauthenticated.
- **Deep links:** App Links (autoVerify) carry content paths only. No auth state travels in a deep link. The verification, pending and 2FA tokens travel as in-app nav args, which survive process death.
- **Registration and OTP:** register codes and statuses (`handler.go:683-806`) map to the right fields. EMAIL_NOT_VERIFIED is recovered with its token. The 2FA, step-up and reset texts match the server's codes. Register uses an Idempotency-Key. `apiCall` turns an empty or null envelope into `Malformed` rather than crashing.
