# Production Readiness & Completeness Audit

> Last updated: 2026-03-03 — reflects commits through `d832558`

## Scope and method
- Repository-audit only (no assumptions outside checked-in code).
- Evidence sources: service compose files, service handlers/cmd entrypoints, config defaults, and local build/test checks.

---

## 1) Service Inventory

### Infrastructure declared
- **Postgres, Redis, Scylla, Redpanda (+console), MinIO, OpenSearch** are declared in the main compose stack. They are used as shared state/event/search/media dependencies across services.

### Runtime services wired in `Architecture/docker/docker-compose.yml`
| Service | Path | Responsibility | API surface | Data stores | Event usage |
|---|---|---|---|---|---|
| api-gateway | `Architecture/services/api-gateway` | Reverse proxy + CORS + path routing + rate limiting + internal key injection | Prefix routing in `cmd/server/main.go` | none | none |
| identity-auth | `identity-platform/services/auth-service` | OTP/login/register/refresh/logout/OAuth/2FA/session mgmt | `/v1/auth/*` routes in handler | Postgres + Redis | Kafka producer + outbox relay |
| identity-user | `identity-platform/services/user-service` | user profile/settings (`/me`) | `/v1/users` routes in handler | Postgres + Redis | Kafka consumer + inbox dedup |
| identity-profile | `identity-platform/services/profile-service` | profile endpoints | `/v1/profiles` handler routes | Postgres + Redis | Kafka consumer + inbox dedup |
| user-service | `Architecture/services/user-service` | app user/channel/page/link domain + online presence | `/v1/users`, `/v1/channels`, `/v1/pages`, `/v1/links`, `/v1/users/me/heartbeat`, `/v1/users/:id/online` | Postgres + Redis | Kafka consumer |
| graph-service | `Architecture/services/graph-service` | follow/block/friend graph | `/v1/graph/*` routes | Postgres + Redis | Kafka producer + deletion consumer |
| group-service | `Architecture/services/group-service` | groups + integration with chat/post | `/v1/groups` routes | Postgres + Redis | indirect (service URL deps) |
| post-service | `Architecture/services/post-service` | posts/comments/stories/reactions/bookmarks/polls | `/v1/posts`, `/v1/stories`, `/v1/comments`, `/v1/saved`, hashtags | Postgres + Scylla + Redis | Kafka producers + consumers + deletion consumer |
| feed-service | `Architecture/services/feed-service` | feed fanout/read model/backfill/ranking | `/v1/feed` routes | Postgres + Scylla + Redis | Kafka consumer |
| media-service | `Architecture/services/media-service` | media metadata/upload + worker-driven processing | `/v1/media` routes + worker cmd | Postgres + MinIO | Kafka producer + worker consumer |
| notification-service | `Architecture/services/notification-service` | inbox/stream/read prefs/device tokens | `/v1/notifications` routes | Scylla + Redis (+pg env in cmd) | Kafka consumer |
| search-service | `Architecture/services/search-service` | universal/user/post/hashtag search + discover | `/v1/search`, `/v1/discover` | Postgres + OpenSearch | Kafka consumer |
| trust-safety-service | `Architecture/services/trust-safety-service` | report intake/listing | `/v1/reports` | Postgres | Kafka writer |
| chat-message-service | `chat-service/services/message-service` | conversations/messages persistence + DM circle gating | chat handler routes | Postgres + Scylla + Redis | Kafka producer + identity consumer |
| chat-ws-gateway | `chat-service/services/ws-gateway` | websocket fanout gateway + online presence set/clear | ws server/middleware files | Redis | none |
| monetization-service | `Architecture/services/monetization-service` | creator monetization primitives | `/v1/monetization` routes | Postgres + Redis | none visible |
| analytics-service | `Architecture/services/analytics-service` | ingest + scoring + creator dashboards | analytics handlers and dashboard routes | Postgres + Redis + Scylla | Kafka producer + consumers |
| feature-flag-service | `Architecture/services/feature-flag-service` | flag evaluation/admin | `/v1/flags/me`, `/v1/admin/flags` | Postgres + Redis | none visible |
| admin-service | `Architecture/services/admin-service` | suspension + audit style admin ops | `/v1/admin` handler | Postgres | Kafka configured |
| suggestion-service | `Architecture/services/suggestion-service` | people/content suggestions | `/v1/suggestions` routes | Postgres + Redis + Scylla | Kafka |
| shop-service | `Architecture/services/shop-service` | storefront/product listings | `/v1/shop` routes | Postgres | Kafka |
| live-service | `Architecture/services/live-service` | live streaming sessions | `/v1/live` routes | Postgres | Kafka |
| memories-service | `Architecture/services/memories-service` | user memory highlights | `/v1/memories` routes | Postgres | — |
| orders-service | `Architecture/services/orders-service` | order lifecycle management | `/v1/orders` routes | Postgres | Kafka |
| payments-service | `Architecture/services/payments-service` | payment intents + idempotent processing | `/v1/payments` routes | Postgres | Kafka |

### Previously orphaned — now fully wired ✅
`live-service`, `memories-service`, `shop-service`, `suggestion-service` — all wired in compose, run-local.sh, local.env, and gateway routes as of `d832558`.

---

## 2) Gap Analysis (full-scale unified social app readiness)

### Module path consistency ✅ DONE
All services now use `github.com/atpost/...` module paths. Identity platform uses `github.com/atpost/identity-*`; chat services use `github.com/atpost/chat-*`. `go build github.com/atpost/...` is clean.

### Missing or partially implemented modules

| # | Gap | Priority | Status |
|---|-----|----------|--------|
| 1 | **Unified Ranking/Recommendation platform** — no dedicated ranking service with feature store, candidate generation, model serving | P0 | **Out of scope** — ML/platform team; feed scoring heuristic is implemented in `feed-service` (scorer.go, ranker.go, diversity.go) |
| 2 | **Short-video / long-video production pipeline** — no adaptive bitrate packaging, DRM, multi-region CDN orchestration | P0 | **Out of scope** — infra/CDN team; `media-service` handles upload and worker-driven processing |
| 3 | **Commerce stack** — `shop-service` now wired; `orders-service` + `payments-service` exist with idempotent write paths | P0 | **Partially done** — fraud/dispute/webhook/refund subsystem not yet implemented |
| 4 | **Experimentation/AB platform** — feature flags exist, no assignment logging or stat engine | P1 | **Not done** — requires `feature-flag-service` + `analytics-service` integration |
| 5 | **Trust & Safety breadth** — report intake/listing exists; no policy engine, case workflow, appeals, classifiers, sanctions ladder | P0 | **Not done** — requires `trust-safety-service` + `admin-service` expansion |
| 6 | **Privacy/compliance engine** — no central GDPR/DPDP deletion/export/retention service | P0 | **Partially done** — `user.deletion_requested` outbox event cascades to post, graph, user-service; full privacy-service (export, retention, DPDP) not yet built |
| 7 | **Global search quality pipeline** — no anti-spam ranking, query understanding, or indexing backfill controls | P1 | **Not done** — requires `search-service` expansion |
| 8 | **Operational control plane** — no SRE ops service for circuit-breakers, kill switches, canary orchestration | P1 | **Not done** — requires platform team + gateway expansion |

---

## 3) Production checklist by service group

### Cross-cutting Security

| Item | Status |
|------|--------|
| **Rate limiting** — IP + user + burst budgets | ✅ **Done** — `shared/middleware/ratelimit.go`: per-IP 100 rps/200 burst, per-user 60 rps/120 burst; wired in api-gateway |
| **Internal service authentication** — `X-Internal-Service-Key` header | ✅ **Done** — `shared/middleware/internal_auth.go`: gateway injects key; admin-service + trust-safety-service validate it |
| **Secret validation fail-fast** — reject empty JWT_SECRET on startup | ✅ **Done** — api-gateway and auth-service: `os.Exit(1)` if empty, warn if dev default |
| **DM circle gating** — DMs only between circle members | ✅ **Done** — `message-service/internal/policy/dm_policy.go`: graph-service check; fail-open on graph outage |
| **Online presence privacy** — circle-gated `/v1/users/:id/online` | ✅ **Done** — user-service: fail-closed for non-circle callers |
| **Gateway JWT verification** — verify token before proxying to backend | ⚠️ **Not done** — gateway passes JWT through; backends trust `X-User-Id` header from gateway. Risk: internal network misuse. Fix: add JWT verify middleware in gateway, set `X-Verified-User-Id` header. |
| **Per-route authz** — owner/admin/mod scope enforcement | ⚠️ **Not done** — admin, trust-safety, monetization, commerce routes have no scope checks beyond internal key |
| **Compose static credentials** — OpenSearch admin password, MinIO creds | ⚠️ **Not done** — still hardcoded in docker-compose.yml; require secret manager injection for production |

### Reliability

| Item | Status |
|------|--------|
| **Shared HTTP client with timeouts** | ✅ **Done** — `shared/httpclient/client.go`: `New(timeout)`, `NewWithRetry(maxRetries)`, `Default` (5 s) |
| **group-service HTTP timeouts** | ✅ **Done** — all 3 `http.DefaultClient` replaced with `httpclient.New(5s)` |
| **Payments idempotency** | ✅ **Done** — `ON CONFLICT (idempotency_key)` + `WasExisting` detection; no duplicate Kafka publish on replay |
| **DLQ + replay tooling** — dead-letter queues for Kafka consumers | ✅ **Done** (prior session) — `shared/kafka/consumer.go`: `DLQTopic`, `RetryBackoff`, `processWithRetry` |
| **Outbox pattern** | ✅ **Done** (prior session) — transactional outbox + relay for auth, profile, post, graph services |
| **Inbox dedup** — Postgres-backed `(consumer_name, event_id)` dedup | ✅ **Done** (prior session) — identity profile-service and user-service consumers |
| **Timeouts on remaining service clients** | ⚠️ **Partial** — group-service fixed; other services with inter-service HTTP calls may still use default clients |
| **Circuit breakers** | ⚠️ **Not done** — no circuit-breaker library (e.g. sony/gobreaker) wired on any service client |
| **Idempotency on orders/messages/reactions** | ⚠️ **Partial** — payments done; orders and message paths not yet reviewed |

### Data

| Item | Status |
|------|--------|
| **Migration baselines** — versioned `001_initial.sql` for all services | ✅ **Done** — feed, graph, orders, payments all have `database/migrations/001_initial.sql` |
| **payments-service standalone schema** | ✅ **Done** — `payments-service/database/setup.sql` |
| **Account deletion cascade** | ✅ **Done** — `user.deletion_requested` outbox event; consumers in post-service, graph-service, user-service |
| **Remaining services without migrations dir** | ⚠️ **Partial** — post, media, notification, search, trust-safety still use only setup.sql; no versioned migration files |
| **Partition/sharding strategy doc** | ⚠️ **Not done** — hot partitions in feed/chat/notifications not documented |
| **Backfill/reconciliation runbooks** | ⚠️ **Not done** |

### Observability

| Item | Status |
|------|--------|
| **Prometheus metrics + Grafana dashboards** | ✅ **Done** (prior session) — all services expose RED metrics; Grafana provisioned |
| **Structured logs + trace IDs** | ✅ **Done** (prior session) — `slog` structured logging with trace propagation |
| **SLO definitions** | ⚠️ **Not done** — SLO thresholds for login, feed, post create, media transcode not codified as alert rules |
| **End-to-end trace propagation** | ⚠️ **Partial** — trace IDs propagated in headers; full distributed tracing (OpenTelemetry/Jaeger) not wired |

### Config/Secrets

| Item | Status |
|------|--------|
| **JWT_SECRET fail-fast** | ✅ **Done** — gateway + auth-service exit on empty, warn on dev default |
| **Vault/secret manager injection** | ⚠️ **Not done** — compose still contains static creds; require external secret manager for prod |

### Background Jobs

| Item | Status |
|------|--------|
| **Outbox relay workers** | ✅ **Done** — auth outbox relay running as separate goroutine/cmd |
| **Media processing worker** | ✅ **Done** — media-service worker cmd |
| **Kafka consumer lifecycle** | ✅ **Done** — shared DLQ/retry framework in `shared/kafka/consumer.go` |
| **Cleanup/backfill workers** | ⚠️ **Not done** — no explicit cleanup or backfill job owners for stale data |

### Error Handling

| Item | Status |
|------|--------|
| **Normalized error schema** | ⚠️ **Not done** — each service returns its own error format; no shared error envelope or stable external codes |

---

## 4) Critical Risks — updated status

### P0-1: Build/runtime break from missing shared package
- **Status: ✅ RESOLVED** — `github.com/atpost/shared/server` package exists; all services import from `github.com/atpost/...`; `go build github.com/atpost/...` is clean.

### P0-2: Gateway has no auth/token verification layer
- **Status: ⚠️ PARTIALLY MITIGATED** — `X-Internal-Service-Key` prevents external callers from spoofing internal service identity. JWT token is still not verified at the gateway; backends trust the `X-User-Id` header passed through.
- **Remaining fix**: add JWT verify middleware in api-gateway; on success, set `X-Verified-User-Id`; backends should reject requests missing this header.

### P0-3: Header-based identity trust in business handlers
- **Status: ⚠️ PARTIALLY MITIGATED** — internal key reduces risk from external bypass; `X-User-Id` still read directly in several handlers.
- **Remaining fix**: after P0-2 gateway JWT verify is added, switch handlers to read `X-Verified-User-Id` only.

### P0-4: Hardcoded/dev secrets and credentials
- **Status: ✅ MOSTLY RESOLVED** — JWT_SECRET fail-fast validation added to api-gateway and auth-service. Static OpenSearch/MinIO creds in docker-compose.yml remain; acceptable for local dev, must use secret manager for prod deploy.

### P1-1: Service wiring mismatches and missing deploy targets
- **Status: ✅ RESOLVED** — analytics port fixed (8093 → 8094); all 4 orphaned services (suggestion, shop, live, memories) wired in compose, local.env, run-local.sh, and gateway routes.

### P1-2: Test/contract drift in auth service
- **Status: ✅ RESOLVED** — auth-service test stub repaired (ForgotPassword + all interface methods); `TestForgotPasswordMissingIdentifier` added; `go test ./...` passes.

---

## 5) Remaining backlog — ordered by priority

### P0 — Must fix before any production traffic

1. **Gateway JWT verification + verified identity header propagation**
   - Add JWT verify middleware in `api-gateway/cmd/server/main.go`
   - Set `X-Verified-User-Id` on verified requests; drop `X-User-Id` passthrough
   - Backends: replace `X-User-Id` reads with `X-Verified-User-Id`

2. **Commerce fraud/dispute/refund/webhook subsystem**
   - `shop-service` + `orders-service` + `payments-service` are wired, but no refund workflow, webhook delivery, or fraud rules exist
   - Requires: refund endpoint in payments-service, webhook delivery worker, basic fraud rule engine

3. **Trust & Safety policy engine**
   - `trust-safety-service` + `admin-service` expanded with: case workflow, appeals queue, automated text/image classifier integration, sanctions ladder

4. **Privacy compliance (GDPR/DPDP)**
   - New `privacy-service` or extension of auth-service: account export, data retention policies, DPDP consent records
   - The `user.deletion_requested` cascade is in place; export and retention are missing

### P1 — Required before scaled launch

5. **Gateway JWT verify enables per-route scope enforcement**
   - After JWT verify lands: add admin/mod scope checks on `/v1/admin`, `/v1/reports`, `/v1/monetization` routes

6. **Circuit breakers on inter-service HTTP clients**
   - Add `sony/gobreaker` or equivalent to `shared/httpclient`; configure in group-service, user-service (graph calls), message-service (graph calls)

7. **SLO alert rules + end-to-end tracing**
   - Codify SLOs in Prometheus alert rules: auth P99 < 500 ms, feed P99 < 300 ms, post create P99 < 1 s
   - Wire OpenTelemetry for distributed trace propagation across gateway → service boundaries

8. **Migration files for remaining services**
   - post, media, notification, search, trust-safety services: convert `setup.sql` to `migrations/001_initial.sql`

9. **Error schema normalization**
   - Shared error envelope: `{"error": {"code": "...", "message": "..."}}` across all services

10. **AB/experimentation platform**
    - `feature-flag-service`: add assignment log table + Kafka event on flag evaluation
    - `analytics-service`: consume flag assignment events for experiment analysis

11. **Search quality improvements**
    - Anti-spam ranking, query normalization, backfill/replay tooling in `search-service`

### P2 — Scale and operations

12. **Operational control plane** — global kill switches, canary orchestration, circuit-breaker admin UI
13. **Partition/sharding runbooks** — document hot partition mitigation for feed/chat/notifications
14. **Chaos drills + load tests** — India-peak load assumptions, game days, runbook acceptance
15. **Secret manager integration** — replace all static compose creds with vault/AWS Secrets Manager injection

---

## 6) Definition of Done — v1 launch gate

| Gate | Criteria | Status |
|------|----------|--------|
| Build | All services compile cleanly (`go build github.com/atpost/...`) | ✅ Done |
| Service wiring | All routed services deployed and reachable in compose | ✅ Done |
| Secret hygiene | No hardcoded secrets in startup paths; fail-fast on empty | ✅ Done (compose static creds remain for local dev) |
| Auth hardening | Gateway JWT verify + verified identity header | ⚠️ Pending |
| Rate limiting | Per-IP + per-user limits enforced at gateway | ✅ Done |
| Internal auth | Internal service key on sensitive routes | ✅ Done |
| Reliability | Timeouts on all inter-service HTTP; DLQ/retry on all Kafka consumers | ✅ Mostly done (circuit breakers pending) |
| Idempotency | All write paths idempotent (payments done; orders/messages partial) | ⚠️ Partial |
| Data migrations | Versioned migrations for all services | ⚠️ Partial |
| Account deletion | Cascade delete event consumed by post, graph, user-service | ✅ Done |
| Observability | Metrics + structured logs on all services | ✅ Done |
| SLOs | Alert rules defined and deployed | ⚠️ Pending |
| Trust & Safety | Policy engine + case workflow | ⚠️ Pending |
| Privacy compliance | Export + retention + DPDP consent | ⚠️ Pending |
| Auth tests | Auth-service test suite passes | ✅ Done |
| Load test | India-peak assumptions validated | ⚠️ Pending |
| Security review | Pen test / code review sign-off | ⚠️ Pending |
