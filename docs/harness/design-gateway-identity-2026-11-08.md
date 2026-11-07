# Gateway identity: user traffic without the internal key

Design, 8 Nov 2026. Read-only inventory of `codex/module-01-02-launch-safety`
plus one small fix (dating-service, section 6). Status: **awaiting founder
decisions**.

## 1. Founder decisions (answer these first)

| # | Decision | Recommendation |
|---|---|---|
| F1 | How a service learns "which user" | **Option B**: the gateway signs a short-lived, per-service identity assertion with its own Ed25519 key. Services verify it with the public key. (Option A, forwarding the user's JWT, is compared in section 4.) |
| F2 | Who provisions the new key pair | One Ed25519 pair for the gateway, `kid` `gw-1`. Private key only in the gateway's secret (AWS Secrets Manager via External Secrets, same path as the existing `servicetoken` keys). Public key goes into every verifying service's config (not secret). A second pair for rotation later. No values in git. |
| F3 | Per-service service-token keys | Each service that calls another gets its own Ed25519 pair, the pattern commerce already uses with payments. First wave: notification-service and chat message-service (to dating), then the key-only callers in section 3. Same store as F2. |
| F4 | Consumer-session admin scopes | Agree that `admin` / `moderator` / `superadmin` in a consumer token admit nobody to an admin action anywhere (dating did this in audit H3). Admin actions go only through admin-service with an `act`-claim service token. Nine services still decide admin on `X-Scopes` or `X-Admin-Role` (section 3). |
| F5 | Order and pace | Shared library, then services in the risk order of section 5, one service per lane, each behind its own flag. The gateway stops stamping a prefix only after its upstream is in enforce mode. |
| F6 | Deadline for the internal key on user routes | Proposed: no stamped prefix at launch. If that cannot hold, list the prefixes that may ship stamped and accept the residual in writing. |

## 2. The problem in one paragraph

`internalroutes.StampPolicy` stamps `X-Internal-Service-Key` onto user traffic
for **53 of 69** route prefixes. The audit counted about 59, so the table has
shrunk since. The upstreams of those prefixes treat "has the key" as "came
through the gateway" and then believe `X-User-Id`, `X-Scopes` and
`X-Admin-Role` as sent. The gateway strips client copies, which closes the
edge. But the same key is in roughly 30 pods: every service, the dating
exporters and purgers, rider, food and others, and they use it for their own
outbound calls. Any one of those pods can be anyone to any service: set
`X-User-Id: <victim>` and `X-Scopes: admin`, add the key, and you are trusted.
Five unstamped services are worse: they have no gate at all and trust
`X-User-Id` from any network peer.

## 3. Inventory (from code)

Stamped prefixes. "Key unlocks" is where the gate is installed. "Identity
source" is what the service then believes.

| Gateway prefix(es) | Upstream | Key unlocks | Identity source |
|---|---|---|---|
| `/v1/profiles` | identity-profile | whole engine, `RequireInternalServiceKey` (identity-platform `profile-service/internal/http/handler.go:130`) | `X-User-Id` |
| `/v1/users/me/{settings,modules,region}` | identity-user | whole engine, `RequireInternalServiceKey` (`user-service/internal/http/handler.go:50`) | `X-User-Id` |
| `/v1/users/me/keyword-filters`, `/v1/reports`, `/v1/appeals`, `/v1/grievances`, `/v1/verification-requests` | trust-safety | whole engine (`cmd/server/main.go:307`) | `X-User-Id`. **Admin on `X-Scopes`**: `adminAllowed` legacy branch (`admin_token.go:265`), `grievance_handler.go:122`, `trust_extras_handler.go:77`, verification queue GET |
| `/v1/graph`, `/v1/permissions` | graph | whole engine (`handler.go:102`) | `X-User-Id`, plus the gateway-stamped write-source label (LB-3) |
| `/v1/channels`, `/uploads`, `/videos`, `/reels`, `/stories`, `/saved`, `/hashtags`, `/comments`, `/playlists`, `/video-series`, `/series`, `/creators`, `/feedback`, `/posts`, `/crossposts`, `/credits`, `/links` (17) | post | whole engine, `keyGate` (`handler.go:77`) | `X-User-Id`. **Moderator on `X-Scopes`**: `requireModerator` (`moderator_gate.go:25`). Admin family on an admin-service token |
| `/v1/feed` | feed | whole engine (`handler.go:79`) | `X-User-Id` |
| `/v1/notifications`, `/v1/unread`, `/v1/realtime` | notification | whole engine (`handler.go:45`). SSE also takes an HMAC topic token | `X-User-Id` |
| `/v1/discover`, `/v1/search` | search | whole engine (`handler.go:126`) | `X-User-Id`. The `bulk-sync` route is on the edge deny list |
| `/v1/groups` | group | whole engine | `X-User-Id` |
| `/v1/reviewer` | reviewer | whole engine (`main.go:137`) | `X-User-Id`. **Admin on `X-Scopes`**: `isAdmin` (`handler.go:67`) |
| `/v1/analytics` | analytics | whole engine | `X-User-Id`. **`X-Admin-Role`** read in `content_insights.go:81` |
| `/v1/ai` | ai | whole engine | `X-User-Id` |
| `/v1/admin`, `/v1/apps`, `/v1/oauth` | admin-service | whole engine (`main.go:177`) | Admin session at the gateway (`admin_access_token` cookie, MFA claims), then identity permissions through `Gate`. `requireAnyScope` on `X-Scopes` remains on the mini-app status route |
| `/v1/monetization` | monetization | `/v1` group (`handler.go:89`) | `X-User-Id`. **Admin on `X-Scopes`**: `hasAdminScope` (`fraud_handler.go:31`), beside the token family |
| `/v1/livestream` | live-v2 | `/v1` group (`handler.go:81`) | `X-User-Id` |
| `/v1/memories` | memories | whole engine | `X-User-Id` |
| `/v1/broadcast-channels` | channel | whole engine, plus its `/internal` group | `X-User-Id` |
| `/v1/communities` | community | whole engine | `X-User-Id` |
| `/v1/qa` | qa | `qa` group | `X-User-Id` |
| `/v1/dating` | dating | public group (`handler.go:74`). The internal family takes a service token, or the key for 4 ops (section 6) | `X-User-Id`. Admin only on an admin-service token (H3) |
| `/v1/food` | food | `/v1` group | `X-User-Id`. **Admin on `X-Scopes`**: `requireAdminScope` (`handler.go:2199`) |
| `/v1/rider`, `/v1/partner` | rider | rider and partner groups | `X-User-Id` and `X-Scopes` put into context. **`/v1/rider/admin` `AdminGuard` on `X-Admin-Role`**. `REALTIME_TOKEN_SECRET` falls back to the internal key (`main.go:233`) |
| `/v1/doorstep` | doorstep | `/v1/doorstep` group | `X-User-Id` and `X-Scopes` put into context. Admin family token-only |
| `/v1/commerce` | commerce | `RequireGatewayTrust` on `/v1/commerce`, except the admin-token family (`gatewaytrust.go:77`) | `X-User-Id` |

Unstamped (16): the gateway does not send the key.

| Prefix(es) | Upstream | Identity source | Note |
|---|---|---|---|
| `/v1/chat`, `/v1/ws`, `/v1/calls` | chat-service | **Verifies the RS256 JWT itself** with `chat-service/shared/accessauth` (`RSAKeysFromEnv`; production refuses HS256 secrets) | Already the target state for users |
| `/v1/media`, `/v1/audio`, `/v1/audio-library`, `/v1/subtitles` | media | **Verifies the JWT itself** (`media-service/internal/http/jwtrsa.go`, `AuthMiddlewareWithKeys`) | `JWT_SECRET` is still required at boot (`main.go:40`) |
| `/v1/auth` | identity-auth | It mints the tokens | The key guards only `/v1/auth/internal/*` |
| `/v1/admin/flags`, `/v1/flags` | feature-flag | `X-User-Id`, and **admin on `X-Scopes` with no gate at all** (`AdminAuthMiddleware`, `handler.go:68`) | Any pod can forge an admin without even holding the key |
| `/v1/users`, `/v1/onboarding`, `/v1/pages` | user-service | `X-User-Id`, ungated | Any pod can act as any user |
| `/v1/wallet`, `/v1/billpay` | wallet, bill-pay | `X-User-Id`, ungated (`handler.go:78` in each) | Any pod can act as any user, on money routes |
| `/v1/suggestions` | suggestion | `X-User-Id`, ungated | |

Shared building blocks that already exist:

- `Architecture/shared/servicetoken`: per-caller Ed25519 tokens. The algorithm
  is pinned, the audience is checked, and each caller has an allowlist of
  operations and reference types. Maximum TTL is 5 minutes. An optional signed
  `act` claim names the human user. It travels as
  `X-Service-Authorization: Bearer <token>`. Commerce signs with it to
  payments (`commerce-service/internal/payments/p0client.go:101`). admin-service
  signs to dating, commerce, post, trust-safety and payments with
  `act` = the admin. food, rider, doorstep, monetization, live-v2, reviewer
  and trust-safety sign too. Verifiers are built from env with
  `SERVICE_CALLERS=<name,...>` plus `SERVICE_CALLER_<NAME>_{KID,PUBKEY,OPS}`
  (dating `auth_config.go:86`, payments).
- `Architecture/shared/middleware.RequireInternalKey`: the shared-key gate this
  design retires from user traffic.
- `chat-service/shared/accessauth` and api-gateway `pkg/tokenpolicy` both
  verify the RS256 access token. The public key arrives as
  `JWT_PUBLIC_KEY_PEM` / `JWT_RS256_KID` plus a `_PREVIOUS` pair; there is no
  JWKS endpoint for access tokens. auth-service's `/.well-known/jwks.json`
  serves mini-app tokens only.
- `identity-platform/shared` holds no auth middleware. Its services import
  `identity-shared`, not `atpost/shared`, so they need their own copy of the
  verifier (section 5).

## 4. Options

**A. Forward the user's JWT; every service verifies it.** The gateway would
copy the token into `Authorization`, because cookie and subprotocol callers
arrive without one. Every service would load the RS256 public key through a
shared middleware.

- Good: identity is end to end and needs no new key. chat and media already
  work this way.
- Bad: the access token is a platform-wide bearer token that lives for
  minutes. Every service that receives it can replay it to every other
  service as that user, so a compromised pod still moves sideways, only with
  real tokens instead of invented ones.
- Bad: every gate the gateway applies would have to be rebuilt in about 25
  services: session revocation (`sessionrevocation`), admin-versus-consumer
  session kind, admin CSRF, the dormant-product and pilot allowlists, the
  edge rate limit by user, and the `admin_mfa` / `step_up_at` stamping.
  Rebuilt policy drifts.
- Bad: two token verifiers already exist (gateway and chat). This would mean
  25 more deployments of key configuration and issuer and audience policy.
  An HS256 development fallback left on in any one of them is a forgery hole.

**B. The gateway signs a short-lived identity assertion (recommended).** After
`jwtExtractMiddleware` verifies the token and all edge gates pass, the gateway
mints `X-Gateway-Identity`, an EdDSA JWS signed with a key only the gateway
holds:

`iss=api-gateway`, `aud=<upstream service>` (from the route table), `sub`
(user id, or empty for anonymous), `sid`, `scopes`, `kind`
(consumer or admin), `admin_mfa`, `auth_time`, `step_up_at`, `dev` (device
id), `rid` (X-Request-Id), `htm` and `htu` (the method and the path it was
minted for), `iat`, and `exp` = iat + 60 s.

- Good: asymmetric, so a compromised service or any other pod can verify but
  never mint. That is the property the internal key lacks. An HMAC assertion
  (B') would rebuild the shared-secret problem, because every verifier could
  mint.
- Good: the audience and the method plus path binding mean an assertion
  copied out of one service's logs cannot be replayed against another
  service, or against another route on the same service. The 60-second
  expiry bounds the rest.
- Good: the gateway stays the single policy point. Services check one
  signature and read claims. They never parse user JWTs.
- Good: anonymous requests get an assertion too, with an empty `sub`. So
  "relayed user request" (an assertion is present) and "service-to-service
  call" (`X-Service-Authorization`, no assertion) are always told apart by
  credential type, never by guessing from headers.
- Cost: one key pair to provision and rotate (F2), and the gateway must be
  protected as the identity root. It already is today: it holds the key and
  the JWT verification keys.

**Service-to-service in both options:** `servicetoken`, unchanged. A service
acting for a user passes the user in the signed `act` claim, never in a
forwardable header.

**Admin scopes in both options:** a service reads scopes only from the
verified assertion. It never reads `X-Scopes` or `X-Admin-Role` headers, and
the gateway stops sending them. Admin actions need an admin-service token with
the per-route permission and `act` (dating, commerce, trust-safety, post and
payments already work this way). The `scopes` claim may personalise the user
interface (food capabilities); it never authorises an admin write.

## 5. Plan for option B

Each phase ships on its own. Each service has a flag
`GATEWAY_IDENTITY_MODE=off|shadow|enforce`, default `off`.

1. **Library.** Add `Architecture/shared/gatewayidentity`, built from
   `servicetoken`'s Ed25519 code: `Signer` (gateway only), `Verifier`
   (audience, kid map, current and previous key), and gin middleware
   `RequireGatewayIdentity(v)`. The middleware verifies the assertion,
   checks that `htm`/`htu` match the request, and then **overwrites**
   `X-User-Id`, `X-Verified-User-Id` and `X-Scopes` from the claims. That way
   existing handlers keep working unchanged and a forged header never survives.
   In shadow mode it logs mismatches and admits. Add a byte-identical copy
   under `identity-platform/shared/gatewayidentity`, held to the original by
   shared golden test vectors. The gateway mints for every prefix, alongside
   the key, behind `GATEWAY_IDENTITY_SIGN=on`.
2. **Services in risk order.** Each service runs shadow for one day in dev,
   then enforce:
   1. the five ungated services: wallet, bill-pay, user-service, feature-flag
      (and drop its `X-Scopes` admin), suggestion;
   2. the nine admin-on-scopes services: trust-safety, post moderator gate,
      monetization, food, reviewer, rider `AdminGuard`, analytics, doorstep,
      admin-service `requireAnyScope`. Each removes its header admin branch
      and moves to admin-service tokens;
   3. commerce (replace `RequireGatewayTrust`), dating, identity-profile,
      identity-user, graph;
   4. the rest: feed, notification, search, group, ai, memories, channel,
      community, qa, live-v2.
   chat and media keep their JWT verification. They may add the assertion
   later.
3. **The gateway stops stamping.** For each prefix whose upstream is in
   enforce mode, flip `StampPolicy` to `false`. The existing boot guard keeps
   the table complete. At the end `StampPolicy` holds only `false` and can go,
   along with `ApplyKey`'s stamp branch.
4. **The internal key on `/internal` only.** Each service mounts
   `RequireInternalKey` on its `/internal` group alone. `RequireGatewayIdentity`
   guards everything else. Key-only callers move to `servicetoken`. Known
   callers today: notification → dating (preview, panic context),
   message-service → dating (first message, chat state), dating → media,
   dating → identity-profile, dating → trust-safety, rider → wallet,
   identity-roles clients, the data-exporter and data-purger jobs, and rider's
   realtime secret fallback.
5. **The key retired.** When no `/internal` route accepts it, remove
   `INTERNAL_SERVICE_KEY` from every deployment.

**Tests.** Library: wrong algorithm, `none`, an unknown kid, a wrong audience,
an expired token, a TTL longer than 60 s, an `htu` mismatch, and a forged
`X-User-Id` beside a valid assertion (it must be overwritten). Each with a
mutation check. Per service: one table test over its real router. No
assertion → 401. A key without an assertion → 401 in enforce mode. An
assertion for another audience → 401. Consumer `admin` scopes on an admin
route → 401 or 403. A service token with no assertion on a user route → 401.
Gateway: every proxied request carries an assertion whose `aud` matches the
route table, enforced at boot like `GuardStampPolicy`.
`integration-all.yml` gains a cross-service case: a pod holding the key but
not the gateway private key cannot act as a user.

**Rollout and rollback.** Dev first, then staging, then prod, one service at
a time. The order within each step is: gateway signing on, then service
shadow, then service enforce, then gateway stamping off for that prefix.
Rollback in reverse at any step, with flags only: set the service back to
`shadow` or `off`, then flip the prefix back to `true` in `StampPolicy` (a
gateway redeploy). Until phase 5 the key still exists, so rollback never
needs a key re-issue. Watch the 401 rate per upstream and the shadow-mismatch
logs before every enforce.

## 6. Fix shipped with this design: dating `authorizeServiceCaller`

Before: the bare internal key admitted any operation on every service-only
route. That included the moderation scan and the retired
`/v1/dating/profile/:userId/preview`, which sit on the public group the
gateway stamps with the key on anonymous requests too. Only the edge deny list
was in the way.

Now (`Architecture/services/dating-service/internal/http/auth.go`):

- The key is accepted only when the matched route pattern is under
  `/v1/dating/internal/` **and** the operation is in `legacyKeyOps`: profile
  preview, panic notify context, first message, chat state. Those are the four
  whose callers (notification-service, chat message-service) still send only
  the key.
- Risk read, the moderation scan and the legacy preview are token-only:
  `401 SERVICE_TOKEN_REQUIRED`. The answer is the same with the right key, a
  wrong key or no key. The two public-group routes still sit behind the
  engine-wide key gate, so a caller there needs the key **and** a token. No
  service calls them today.
- Next step, phase 4: give notification-service and message-service signing
  keys (F3), register them in dating's `SERVICE_CALLERS`, and empty
  `legacyKeyOps`.
