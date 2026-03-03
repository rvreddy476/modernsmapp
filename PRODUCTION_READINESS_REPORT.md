# Production Readiness & Completeness Audit

## Scope and method
- Repository-audit only (no assumptions outside checked-in code).
- Evidence sources: service compose files, service handlers/cmd entrypoints, config defaults, and local build/test checks.

## 1) Service Inventory

### Infrastructure declared
- **Postgres, Redis, Scylla, Redpanda (+console), MinIO, OpenSearch** are declared in the main compose stack. They are used as shared state/event/search/media dependencies across services.

### Runtime services currently wired in `Architecture/docker/docker-compose.yml`
| Service | Path | Responsibility (from code) | API surface evidence | Data stores | Event usage |
|---|---|---|---|---|---|
| api-gateway | `Architecture/services/api-gateway` | Reverse proxy + CORS + path routing | Prefix routing in `cmd/server/main.go` | none | none |
| identity-auth | `identity-platform/services/auth-service` | OTP/login/register/refresh/logout/OAuth/2FA/session mgmt | `/v1/auth/*` routes in handler | Postgres + Redis | Kafka producer + outbox relay |
| identity-user | `identity-platform/services/user-service` | user profile/settings (`/me`) | `/v1/users` routes in handler | Postgres + Redis | Kafka configured |
| identity-profile | `identity-platform/services/profile-service` | profile endpoints | `/v1/profiles` handler routes | Postgres + Redis | Kafka producer |
| user-service | `Architecture/services/user-service` | app user/channel/page/link domain | `/v1/users`, `/v1/channels`, `/v1/pages`, `/v1/links` | Postgres + Redis | Kafka consumer |
| graph-service | `Architecture/services/graph-service` | follow/block/friend graph | `/v1/graph/*` routes | Postgres + Redis | Kafka producer |
| group-service | `Architecture/services/group-service` | groups + integration with chat/post | `/v1/groups` routes | Postgres + Redis | indirect (service URL deps) |
| post-service | `Architecture/services/post-service` | posts/comments/stories/reactions/bookmarks/polls | `/v1/posts`, `/v1/stories`, `/v1/comments`, `/v1/saved`, hashtags | Postgres + Scylla + Redis | Kafka producers + consumers |
| feed-service | `Architecture/services/feed-service` | feed fanout/read model/backfill | `/v1/feed` routes | Postgres + Scylla + Redis | Kafka consumer |
| media-service | `Architecture/services/media-service` | media metadata/upload + worker-driven processing | `/v1/media` routes + worker cmd | Postgres + MinIO | Kafka producer + worker consumer |
| notification-service | `Architecture/services/notification-service` | inbox/stream/read prefs/device tokens | `/v1/notifications` routes | Scylla + Redis (+pg env in cmd) | Kafka consumer |
| search-service | `Architecture/services/search-service` | universal/user/post/hashtag search + discover | `/v1/search`, `/v1/discover` | Postgres + OpenSearch | Kafka consumer |
| trust-safety-service | `Architecture/services/trust-safety-service` | report intake/listing | `/v1/reports` | Postgres | Kafka writer |
| chat-message-service | `chat-service/services/message-service` | conversations/messages persistence | chat handler routes | Postgres + Scylla + Redis | Kafka producer + identity consumer |
| chat-ws-gateway | `chat-service/services/ws-gateway` | websocket fanout gateway | ws server/middleware files | Redis | none |
| monetization-service | `Architecture/services/monetization-service` | creator monetization primitives | `/v1/monetization` routes | Postgres + Redis | none visible |
| analytics-service | `Architecture/services/analytics-service` | ingest + scoring + creator dashboards | analytics handlers and dashboard routes | Postgres + Redis + Scylla | Kafka producer + consumers |
| feature-flag-service | `Architecture/services/feature-flag-service` | flag evaluation/admin | `/v1/flags/me`, `/v1/admin/flags` | Postgres + Redis | none visible |
| admin-service | `Architecture/services/admin-service` | suspension + audit style admin ops | `/v1/admin` handler | Postgres | Kafka configured |

### Services present in repo but **not** fully wired as deployable in compose
- `live-service`, `memories-service`, `shop-service`, `suggestion-service`, and `Architecture/services/message-service` exist, but some are missing cmd entrypoints and/or compose wiring and/or gateway->runtime alignment.

---

## 2) Gap Analysis (full-scale unified social app readiness)

### Missing or partially implemented modules
1. **Unified Ranking/Recommendation platform (P0 gap)**
   - Current feed exists, but no dedicated online ranking service with feature store + candidate generation + model serving + fallback policies.
   - Impact: feed quality/retention drops sharply at scale; poor personalization for Postbook/Postgram/PostTube surfaces.
   - Suggested owner: `feed-service` split into `ranking-service` + offline pipeline owned by ML/recs platform team.

2. **Short-video + long-video production pipeline depth (P0 gap)**
   - `media-service` exists but lacks explicit end-to-end adaptive bitrate packaging, DRM, and multi-region CDN orchestration in-repo.
   - Impact: poor QoE, high buffering, no protected premium content.
   - Suggested owner: `media-service` + dedicated `video-processing-service`.

3. **Commerce stack incompleteness (P0 gap)**
   - `shop-service` endpoints exist but service is not deploy-wired in primary stack; no payment/ledger/refund/webhook subsystem.
   - Impact: checkout can’t be production-safe for fraud/disputes/compliance.
   - Suggested owner: `shop-service` + new `payment-service` + `order-orchestrator`.

4. **Experimentation/AB platform depth (P1 gap)**
   - feature flags exist, but no assignment logging, experiment analysis, guardrail metrics, or stat engine.
   - Impact: risky rollouts and no trustworthy product iteration loop.
   - Suggested owner: `feature-flag-service` + `analytics-service` integration.

5. **Trust & Safety breadth (P0 gap)**
   - report intake/listing exists; missing policy engine, case workflow, appeals, automated classifiers, sanctions ladder.
   - Impact: abuse handling won’t keep up at India-scale.
   - Suggested owner: `trust-safety-service` + `admin-service`.

6. **Privacy/compliance engine (P0 gap)**
   - no central GDPR/DPDP deletion/export/retention policy service found.
   - Impact: legal/compliance risk and weak user trust.
   - Suggested owner: new `privacy-service` orchestrating all data domains.

7. **Global search quality pipeline (P1 gap)**
   - search service exists but no clear anti-spam ranking, query understanding, or indexing backfill/replay controls in-repo.
   - Impact: discoverability quality + abuse exposure.
   - Suggested owner: `search-service`.

8. **Operational control plane (P1 gap)**
   - No dedicated SRE ops service for circuit-breakers, global kill switches, replay controls, canary orchestration.
   - Impact: slower incident mitigation.
   - Suggested owner: platform team + `api-gateway`.

---

## 3) Production checklist by service group

### Cross-cutting Security
- **AuthN/AuthZ boundary**: several handlers consume `X-User-Id` directly; this must be trusted only from verified gateway token middleware.
- Add per-route authorization policies (owner/admin/mod scopes) for admin, trust-safety, monetization, commerce.
- Add strict rate limits (IP + user + token + endpoint + burst budgets).

### Reliability
- Enforce client/server timeouts + retry budgets + circuit breakers on all inter-service calls.
- Standardize idempotency keys on all mutating endpoints (orders/payments/messages/reactions).
- Add dead-letter queues and replay tooling for Kafka consumers.

### Data
- Formal migration pipeline for all services before startup.
- Index review + partition/sharding strategy documents (hot partitions in feed/chat/notifications).
- Backfill/reconciliation playbooks and runbooks.

### Observability
- Every service: structured logs + trace IDs + RED/USE metrics + alert rules + dashboards.
- SLOs: auth/login, post create/read, feed latency, media transcode latency, notification fanout, message send/receive.

### Config/Secrets
- Remove embedded creds/default secrets; use vault/secret manager and env validation on startup.

### Background Jobs
- Explicit ownership and lifecycle for cleanup, backfill, reconciliation workers.

### Error Handling
- Normalize error schema, map internal errors to stable external codes, and avoid leaking internals.

---

## 4) Critical Risks (P0/P1) with evidence and fix guidance

### P0-1: Build/runtime break risk from missing shared package
- Evidence: `post-service` imports `github.com/facebook-like/shared/server` in `cmd/server/main.go`, but corresponding package path is absent under `Architecture/shared`.
- Validation: `go test ./...` in `Architecture/services/post-service` fails with `cannot find module providing package github.com/facebook-like/shared/server`.
- Fix: add/restore `shared/server` package or switch services to existing server bootstrap utility; enforce CI build for every service.

### P0-2: Gateway has no auth/token verification layer
- Evidence: gateway is pure reverse proxy + CORS + path match; no JWT verification middleware in `Architecture/services/api-gateway/cmd/server/main.go`.
- Risk: internal services that trust headers can be spoofed if gateway boundary is bypassed/misconfigured.
- Fix: add authn/authz middleware at gateway and signed identity propagation (e.g., verified JWT -> internal claims header with HMAC).

### P0-3: Header-based identity trust in business handlers
- Evidence: `post-service` `CreatePost` reads `X-User-Id` directly and proceeds if UUID parses.
- Risk: forged identity on any path that is reachable without strict trusted proxy controls.
- Fix: consume verified auth context (claims), not raw caller-provided header.

### P0-4: Hardcoded/dev secrets and credentials in defaults/compose
- Evidence: `JWT_SECRET` fallback `dev_secret_change_me` in identity/chat configs; compose includes static OpenSearch admin password and MinIO creds.
- Risk: credential leakage + weak secret hygiene.
- Fix: fail-fast on missing secrets, rotate credentials, inject from secret manager only.

### P1-1: Service wiring mismatches and missing deploy targets
- Evidence: gateway includes `/v1/suggestions` route, but suggestion-service is not deployed in main compose; analytics default port mismatch in gateway (`8093`) vs compose (`8094`).
- Risk: 404/connection errors and broken product surfaces.
- Fix: align gateway route map and compose deployment matrix; add contract test that all routed services are reachable.

### P1-2: Test/contract drift in auth service
- Evidence: auth-service tests fail because stub no longer implements interface (`ForgotPassword` missing).
- Risk: reduced confidence in auth regression safety.
- Fix: repair test stubs + add CI required checks for identity services.

---

## 5) Definition of Done: ordered backlog to reach “prod-ready v1”

1. **Stop-ship hardening (Week 1)**
   - Fix compile blockers (`shared/server` issue) and route/port mismatches.
   - Introduce mandatory secret validation; remove insecure defaults.
   - Add gateway JWT verification + signed internal identity context.

2. **Reliability baseline (Week 2-3)**
   - Standardize timeouts/retries/circuit-breakers for all service clients.
   - Add idempotency to every write path (esp. monetization/shop/chat).
   - Add DLQ + replay jobs for Kafka consumers.

3. **Data & migrations discipline (Week 3-4)**
   - Ensure each service has forward-only migrations and startup checks.
   - Document partition/sharding strategy for feed/chat/notifications.

4. **Observability + SLO launch gate (Week 4)**
   - Define SLOs; deploy dashboards/alerts.
   - Enforce trace propagation from gateway through all services.

5. **Product completeness tranche (Week 5-8)**
   - Deploy missing/wip services (suggestion/shop/live/memories) or explicitly cut from v1.
   - Add payments/ledger/refunds subsystem and privacy compliance workflows.
   - Expand trust/safety automation and moderation workflows.

6. **Readiness signoff**
   - Game days, chaos drills, load tests (India peak assumptions), security review, and runbook acceptance.
