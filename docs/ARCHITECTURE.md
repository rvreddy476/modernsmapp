# VChat / atPost — Architecture, Security & Resilience

> Companion to `PLATFORM_SPEC.md` (the module catalog) and `modules/` (per-module DDL +
> routes). This doc explains **how the pieces interact**, the **security model**, and how
> **Chat and Video are designed to stay up**.
>
> **Honesty rule (important):** this describes the *architecture and the patterns in the
> code*. The dev `docker-compose` runs **one replica of each service on a single node** — that
> is **not** highly available by itself. Where something is "designed for HA but needs a
> production deployment to actually achieve it," it is called out explicitly. We do **not**
> claim literal zero downtime; we describe the mechanisms and their failure/degradation modes.

---

## 1. How the system interacts (request & event flow)

### 1.1 The two planes
- **Synchronous plane (request/response):** client → edge → **api-gateway** → service → datastore.
  Used for reads and user actions that need an immediate answer.
- **Asynchronous plane (events):** a service commits to its DB, emits an event (Kafka /
  outbox), and **consumers** react later. Used for fan-out, counters, analytics, search
  indexing, notifications, and cross-service side-effects — so the request path stays fast and
  services stay **loosely coupled**.

```
                 ┌──────────── clients ────────────┐
                 │  Flutter app        Next.js web  │
                 └─────────────┬───────────┬────────┘
        REST /v1/*  (mobile)   │           │  /v1/* → /api/proxy (web rewrite)
                               ▼           ▼
                        ┌─────────── api-gateway (:8080) ───────────┐
                        │ JWT verify · CORS · rate-limit ·          │
                        │ inject X-User-Id / X-Scopes / X-Internal  │
                        │ prefix-route /v1/<x> → service            │
                        └───────┬───────────────┬──────────────┬────┘
            ┌───────────────────┘               │              └───────────────┐
            ▼                                    ▼                              ▼
   post / feed / graph / user / ...      chat-message-service          media-service
       │        │        │                  │   │   │                   │      │
   Postgres   Redis   Scylla            Scylla Redis ws-gateway       MinIO  media-worker
       │                                   │                                    │
       └──────────► Kafka (social.events.v1) ◄── consumers: analytics, feed, notifications,
                    monetization.events, chat.events.v1, identity.events.v1   trust-safety…
```

### 1.2 Worked example — a user posts a short video
1. Web/mobile uploads the file: `POST /v1/media/init` → **PUT directly to MinIO** (presigned) →
   `POST /v1/media/confirm`. (Browser never streams through our services — offloaded to object
   storage.)
2. `POST /v1/posts` (post-service) creates the row; `video_metadata` links the media asset; a
   `transcoding_job` is queued.
3. **media-worker** transcodes asynchronously → renditions/HLS → `playback_url`. The request
   that created the post **did not wait** for transcode.
4. post-service emits engagement/post events → **Kafka** → consumers update **feed**
   candidates, **analytics** rollups (CQS), **search** index, **notifications**.
5. Reads: `/v1/feed` ranks candidates; playback is served via `/v1/media/:id/serve` (307 →
   MinIO/CDN). Hot counters (likes/views) are **sharded Redis counters**, reconciled to
   Postgres by a background job — so a viral post never hot-rows a single DB row.

### 1.3 Service-to-service calls
Direct HTTP between services (e.g., reviewer-service → graph-service for the collusion check,
→ wallet-service for KYC, → post-service to publish) carry **`X-Internal-Service-Key`**.
Cross-cutting effects prefer **events over direct calls** to avoid tight coupling and
cascading failure.

---

## 2. Security model (defense in depth)

| Layer | Control |
|---|---|
| **Edge / transport** | TLS terminated at Caddy/Cloudflare in prod (`https://…`); cleartext is dev-only. |
| **AuthN** | identity-auth issues JWTs (access + refresh) with **`kid` rotation**. Signature is **HS256 by default, RS256 when a private key is configured** (verifiers hold only the public key → can't mint). Verifiers accept **both** and pin `alg∈{HS256,RS256}` (no `none`/alg-confusion), enforce `exp`. Token from `Authorization: Bearer`, `access_token` cookie, or `?token=` (legacy WS). |
| **Identity propagation** | The gateway **strips any inbound copy** of `X-User-Id` / `X-Verified-User-Id` / `X-Scopes` / `X-Device-Id` / `X-Internal-Service-Key`, then sets them **only after verifying the token**. Downstream services trust these headers precisely because the edge guarantees they can't be client-supplied. |
| **AuthZ scopes** | `scopes` is a signed JWT claim resolved **server-side** at mint (allowlists today, RBAC table next). Admin/internal authorization reads the token-derived `X-Scopes` — never a client header. |
| **Service-to-service** | `X-Internal-Service-Key` (shared secret) required by `RequireInternalKey` middleware; gateway injects it on proxied requests. |
| **Admin surfaces** | Gateway blocks any path containing `/internal/` unless the caller has `admin`/`moderator`/`superadmin` scope. |
| **AuthZ** | Per-handler scope/ownership checks (e.g., reviewer admin queue checks `X-Scopes`; post resubmit checks author). |
| **Rate limiting** | Redis sliding-window limiters (gateway + per-service, e.g. feed 120/min, login per-IP). **Fail-open** on Redis blips (availability > strictness for non-money paths). |
| **Abuse / sybil** | trust-safety (reports, strikes, trust score), dating device-fingerprint/IP velocity, reviewer graph-exclusion + rotation + audit + ring detection. |
| **Data protection (DPDP)** | KYC is DPDP-safe: **Aadhaar never stored** (only opaque DigiLocker ref), **PAN masked**; money layer separates `gst_ledger`/`tds_ledger`; schema-per-domain isolation. |
| **Money integrity** | Immutable double-entry `ledger_entries` + idempotency keys; payouts gated by KYC. |

### 2.1 Security gaps — status

**FIXED — identity-header spoofing / privilege escalation (was the #1 hole).**
Previously the JWT carried no `scopes` claim, admin pages asserted scope via a **client-sent
`X-Scopes` header**, and the gateway did **not** strip inbound identity headers — so a client
could send `X-User-Id: <victim>` (no token) or a valid low-priv token plus a forged
`X-Scopes: admin` and impersonate / escalate. Now:
- The gateway **deletes every inbound** `X-User-Id` / `X-Verified-User-Id` / `X-Scopes` /
  `X-Device-Id` / `X-Internal-Service-Key` at ingress (`stripInboundIdentityHeaders`,
  unconditional — including the no-token path) and re-derives them **only from the verified
  token**. (Unit-tested: `identity_strip_test.go`.)
- auth-service stamps a `scopes` claim into the access token, resolved **server-side** from
  allowlists (`SUPERADMIN_USER_IDS` / `ADMIN_USER_IDS` / `MODERATOR_USER_IDS`). A client can
  no longer grant itself a scope.
- **Operational note:** because scope is now server-authoritative, admin surfaces work only
  for users with a granted role — set `SUPERADMIN_USER_IDS=<your-user-id>` to bootstrap the
  first superadmin, or nobody is admin (secure default). Frontend code still *sends* `X-Scopes`
  (now ignored/stripped — harmless dead weight); cleanup is follow-up.

**IMPLEMENTED — RBAC roles table + admin grant API.**
Roles now live in `auth.user_roles` (DB), resolved into the token `scopes` claim at mint as
**env allowlist ∪ DB roles** (env = bootstrap so the first superadmin can exist before any DB
grant; DB = the ongoing source of truth). A superadmin manages roles via auth-service:
- `POST /v1/auth/admin/roles {user_id, role}` — grant
- `DELETE /v1/auth/admin/roles {user_id, role}` — revoke
- `GET /v1/auth/admin/roles/:userId` — list a user's roles
- `GET /v1/auth/admin/audit?limit=N` — read the privileged-action audit trail
Web UI: **`/admin/access`** (postbook-ui) — grant/revoke, role lookup, and the audit log.
Roles: `superadmin` ⊇ `admin` ⊇ `moderator`. **Authorization is enforced in the service layer
against the live env∪DB source of truth** (not a possibly-stale token scope). A new grant takes
effect on the target's next token mint (login/refresh) — no forced logout (honors the
no-auto-logout rule). DB-lookup failures fall back to env scopes (logins never blocked by a
roles-table outage). Tested: `internal/config/scopes_test.go`, `internal/service/roles_test.go`.

**IMPLEMENTED — privileged-action hardening (MFA gate + audit trail).**
- **MFA for admins:** when `REQUIRE_MFA_FOR_PRIVILEGED=true`, role grant/revoke require the
  acting superadmin to have 2FA enabled (else `403 MFA_REQUIRED`). Off by default so the
  first-superadmin bootstrap isn't locked out before enrolling. (TOTP 2FA + recovery codes +
  login enforcement already exist in auth-service; this makes it *mandatory* for privileged ops.)
- **Audit trail:** every role grant/revoke — **including denied attempts** — writes an immutable
  row to `auth.admin_audit` (actor, action, target, detail, allowed, ts). Revokes delete the
  `user_roles` row, so this is the durable record (SOC2-style).

**IMPLEMENTED (dual-mode, gated) — RS256 access tokens.**
The shared HS256 secret let *any* service that holds it mint platform-wide tokens. RS256 is now
supported end-to-end: auth-service signs with a **private** key; verifiers (api-gateway,
media-service, chat ws-gateway, and auth-service's own endpoints) verify with the **public**
key — so a compromised verifier can no longer mint. It is **additive and gated**:
- Verifiers accept **both** RS256 and HS256 (no `none`/alg-confusion). So enabling RS256 does
  **not** invalidate existing (long-lived) tokens — no forced logout.
- Signing stays **HS256 until `JWT_PRIVATE_KEY_PEM` is set** on identity-auth (deliberate opt-in).
- group-service doesn't verify user JWTs (it trusts the gateway header); it still *mints* a
  short-lived HS256 service token — that path is unaffected and works under dual-mode.

  **Enable it:**
  ```sh
  openssl genrsa -out jwt_priv.pem 2048
  openssl rsa -in jwt_priv.pem -pubout -out jwt_pub.pem
  # identity-auth (signer):
  export JWT_PRIVATE_KEY_PEM="$(cat jwt_priv.pem)"
  # gateway + media + ws-gateway (verifiers):
  export JWT_PUBLIC_KEY_PEM="$(cat jwt_pub.pem)"
  # then restart the stack; new logins mint RS256, old HS256 tokens still verify.
  ```
  **Final hardening (prod):** once old HS256 tokens have aged out (prod access TTL is 15 min),
  remove the shared `JWT_SECRET` from verifiers to fully retire HS256. Covered by tests:
  `api-gateway/.../jwtrsa_test.go`, `media-service/.../jwtrsa_test.go`. Also note: services
  that *mint* HS256 service-to-service tokens (e.g. group-service) should migrate to the
  `X-Internal-Service-Key`/mTLS path to remove the last shared-secret minting capability.

**PARTIAL — web tokens off `localStorage` → httpOnly cookies.**
auth-service already sets httpOnly `access_token`/`refresh_token` cookies (SameSite=Lax) + a
non-httpOnly CSRF cookie, and the gateway already reads the `access_token` cookie. Done now on
the **server side**: the Next.js proxy was forwarding the 3 `Set-Cookie` headers **folded into
one** (corrupting them) — fixed to forward each via `getSetCookie()` on success **and** error
paths; the `/api/auth/refresh` route now reads the httpOnly `refresh_token` cookie and forwards
the rotated `Set-Cookie` back. **Still to do (deferred — needs a running stack + browser to
verify, whole-app + realtime blast radius):** stop persisting tokens to `localStorage` across
the web app (`api.ts`, `AuthSessionStore.ts`, and ~6 other readers incl. **WebSocket** auth in
`notificationSocket.ts`/`messageService.ts` which pass `?token=` from `localStorage`), behind a
cookie-mode flag, then flip the default. Cross-origin WS auth is the tricky part (web-origin
cookie isn't sent to a different ws host) — likely route WS through a same-origin path too.

**Still open (sequenced):**
2. **No MFA/passkeys** on login or admin surfaces; **no mTLS** between services (shared header
   key only); **secrets via env** (no Vault/KMS rotation).
3. **Outbox not universal** — some services publish to Kafka after commit without a
   transactional outbox (dual-write risk).

---

## 3. Chat — designed for high availability

**Components:** `chat-message-service` (Go) · **ScyllaDB** (keyspace `chatservice`) · **Redis** ·
**Kafka** (`chat.events.v1`) · **chat-ws-gateway** (WebSocket fan-out).

**Why it stays up (and degrades gracefully):**
- **Stateless app tier.** chat-message-service holds no session state → run **N replicas**
  behind the gateway; any replica serves any request; a crashed replica just drops out of the
  pool. (Dev runs 1; prod scales horizontally.)
- **ScyllaDB for the message store.** Scylla is **masterless / multi-replica** (no single
  point of failure); with replication factor ≥ 3 it survives node loss and is built for
  high write throughput (millions of messages) — chosen precisely so chat has no hot single
  DB. (Dev runs a single Scylla node — **not** HA; prod = multi-node cluster.)
- **WebSocket via chat-ws-gateway**, decoupled from the message store. Clients **auto-reconnect**;
  message delivery is **fan-out via Redis pub/sub + Kafka**, so a ws-gateway restart drops only
  live sockets (clients reconnect and re-sync from the durable store) — no message loss.
- **Async decoupling via Kafka.** Reads/writes don't block on downstream side-effects
  (notifications, unread counts, search). If a consumer is down, events **buffer in Kafka** and
  process on recovery — the chat path keeps working.
- **Eventual-consistency reads.** Recent messages from Scylla + Redis cache; a Redis blip
  falls back to Scylla.

**To actually reach "no downtime" in prod:** ≥2 replicas of chat-message-service and
chat-ws-gateway behind a load balancer; multi-node Scylla (RF≥3); multi-broker Kafka;
Redis with replicas/sentinel; rolling deploys (drain ws connections). The **architecture
already supports all of this**; the dev compose is single-node for convenience.

---

## 4. Video — designed for high availability

**Components:** `media-service` + **media-worker** (transcode) · **MinIO** (object store) ·
**mediamtx** (RTMP/live ingest) · post-service `video_metadata` · live-service / live-service-v2.

**Why it stays up:**
- **Upload offloaded to object storage.** Clients PUT **directly to MinIO** via presigned
  URLs (`init`→PUT→`confirm`) — large files never traverse the app tier, so uploads don't
  saturate or stall services.
- **Transcode is decoupled & retryable.** Encoding runs in **media-worker** off a
  `transcoding_jobs` queue, not in the request. The post publishes immediately (review/`pending`
  states cover "not ready"); workers scale horizontally and **retry** failed jobs. A worker
  crash delays a video, it doesn't take the API down.
- **Playback is cache/CDN-friendly.** `/v1/media/:id/serve` issues a **307 redirect** to the
  storage/CDN URL; **HLS** renditions let players adapt bitrate and let a CDN absorb load — the
  app tier isn't in the bytes path for playback.
- **Read path independent of write path.** Browsing/watching hits Postgres metadata + object
  storage; an upload/transcode outage doesn't stop people watching already-published video.
- **Live (mediamtx / LiveKit-v2)** ingest is separate from VOD so a live spike can't starve
  uploads/playback; live chat has its own word-filter/moderation tables.

**To actually reach "no downtime" in prod:** multiple media-service + media-worker replicas;
MinIO in **distributed/erasure-coded** mode (or S3) behind a CDN; object storage replication;
autoscaling workers on queue depth. Again, the **design supports it**; dev is single-node.

---

## 5. Resilience patterns used across the platform
- **Stateless services** → horizontal scale + trivial replica replacement.
- **Right store for the job:** Postgres (relational/transactions), **Scylla** (high-write
  counters, watch sessions, chat), **Redis** (cache, presence, **sharded counters**, rankings),
  **Kafka** (durable async buffer + decoupling), **MinIO** (media).
- **Sharded counters** (post-service) avoid hot-row contention at celebrity scale; reconciled
  to Postgres by a background job.
- **Fail-open** on non-critical dependencies (rate-limit Redis blip → allow, don't 500).
- **Idempotency keys** (payments, comments, media confirm) → safe retries.
- **Outbox + Kafka** → side-effects survive crashes and replay.
- **Health/readiness + `depends_on: service_healthy`** (64 such conditions) order startup;
  `restart=unless-stopped` (via `stack.sh`) auto-restarts crashed containers.
- **Graceful degradation:** transcode lag → "processing" state; consumer down → events buffer;
  one mini-app down → core social/video unaffected (separate services/Date schemas).

### Honest current limitations (dev → prod gap)
- Dev compose = **single replica, single-node** infra → a crash = brief downtime until
  `restart` (seconds), not zero. Real HA needs the multi-replica/multi-node deployment above.
- Only a handful of explicit container **healthchecks** are defined (mostly infra); add
  readiness probes per service for production orchestration (k8s).
- No autoscaling / load balancer in the dev stack (gateway is the single entry).
```
```
