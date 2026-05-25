# Production Readiness Summary

**Date:** 2026-05-25
**Scope:** AtPost / VChat platform — what's production-ready to run at any
scale, what's known-limited, and exactly what you need to provide
externally before launch.

This document complements `DEPLOYMENT_AND_OPS.md` (infrastructure) and
`PRODUCTION_HARDENING_PLAN.md` (operational hardening). Read those for
the *how*; this is the *what's-ready + what-you-need*.

---

## 1. Scale architecture

The platform is built to handle "any number of users" through five
patterns. Each is now applied to every hot-path code surface.

### 1.1 Sharded Redis counters (hot-row contention)

`Architecture/shared/counters` shards a counter across 32 Redis keys per
entity, with a flush worker that materialises the sum back to PG every
10s. Eliminates the per-row UPDATE bottleneck on any high-write
denormalised count.

**Applied to:**

| Entity | Counter kind | Scale rationale |
|---|---|---|
| `communities.member_count` | `community_member_count` | 10M-member community joins |
| `graph counts.follower_count` | `graph_follower_count` | Celebrity follow events |
| `graph counts.following_count` | `graph_following_count` | (paired) |
| `broadcast_channels.subscriber_count` | `channel_subscriber_count` | Channel subscribes |
| `post_engagement_counts.like_count` | `post_like_count` | Viral-post likes |
| `post_engagement_counts.comment_count` | `post_comment_count` | (same) |
| `post_engagement_counts.share_count` | `post_share_count` | (same) |
| `post_engagement_counts.bookmark_count` | `post_bookmark_count` | (same) |
| `post_engagement_counts.repost_count` | `post_repost_count` | (same) |
| `hashtags.use_count` | `hashtag_use_count` | Trending hashtag spam |
| `audio_tracks.use_count` | `audio_use_count` | Trending audio (reels) |
| `stories.view_count` | `story_view_count` | Viral story (1M+ views/24h) |
| `products.view_count` | `product_view_count` | Flash-sale product views |

All have hourly reconciler fallbacks (graph CountReconciler,
post EngagementReconciler, community MemberCountReconciler) that
self-heal any drift from missed events.

### 1.2 Real-time delivery

| Layer | Mechanism | Scale property |
|---|---|---|
| WS gateway | Single shared NotificationSocket per tab (web) + per device (mobile) | One WS per user, multiplexed over Redis pub/sub channels |
| Per-conversation presence | Redis ZSET, score = last-seen timestamp, per-user logical expiry | O(active members) memory, no SET-wide TTL race |
| Typing indicator | Same ZSET pattern, 8s active window | Cheap; auto-evicts on read |
| Chat message fanout | message-service publishes to chat:{userID} channels via Redis pipeline (one RTT per N members, not N RTTs) | M2 audit closeout |
| Cross-region | Single-region today; see §5 for region-expansion path | Future |

### 1.3 Search at scale

`search-service` uses OpenSearch `function_score` ranking combining
`engagement_score × recency_decay × author_affinity` for logged-in
viewers, dropping the affinity boost for anonymous. Anonymous viewers
get O(1)-cached follow-id lookup per session (60s TTL).

Six entity types indexed: posts, users, hashtags, products,
communities, channels. Query timeouts (2s OpenSearch + 3s context) +
bulk-index chunking at 500/req cap memory pressure.

### 1.4 Live streaming

`live-service-v2` is LiveKit-based browser-native broadcast on port
8117. Subscriber tokens cap at 4h TTL, publisher tokens at 12h. Egress
records to MinIO bucket `live-recordings`. Visibility gate (public /
followers / paid) sits at token issuance — non-followers can't even
get a connection token.

Existing `live-service` v1 (RTMP/OBS) coexists at `/v1/live/*`; v2 lives
under `/v1/livestream/*` so the two don't collide.

### 1.5 Kafka partitioning

All event producers key by `actorID` (user/creator) so per-entity event
streams stay ordered on a single partition. Was a random EventID;
swapping to actorID was the b73c591/81b4461 commits. The recent
Kafka-partition-key change means consumers can process per-user in
parallel without ordering gymnastics.

---

## 2. Reliability

### 2.1 Saga + outbox patterns

- **Dating match formation** — `dating_match_saga` reconciler retries
  the chat-service handshake every 60s for any match stuck in
  `status='matched' AND conversation_id IS NULL`. Chat-side
  `CreateDatingMatchConversation` is idempotent on `match_id` via a
  partial unique index. Closes PRODUCTION_GAP_ANALYSIS.md §P0-9.
- **Commerce fulfillment** — fulfillment worker drains
  `fulfillment_jobs` table with kind-based dispatch (paid-order, COD,
  bulk-import, etc.). Idempotent on job ID.
- **Payments webhook dedup** — `payments.webhook_events` table
  fingerprints each provider event so a Razorpay retry doesn't
  double-credit or double-refund.

### 2.2 Health endpoints

29 of 30 services expose `/health` returning `{status: "ok"}`. The
remaining service (api-gateway) is a reverse proxy with no upstream
state to check; it returns `{status:"ok"}` on `/health` and `/v1/health`
for liveness probes.

### 2.3 Counter reconcilers (drift recovery)

Hourly background workers in `graph-service`, `post-service`, and
`community-service` reconcile denormalised counts against ground-truth
row counts. Catches any Kafka event lost to a deploy bounce or
consumer crash before counts drift visibly.

### 2.4 Cross-surface block enforcement

A block on dating-side closes any active `dating_match` conversation
via the `dating.user.blocked` event consumed by chat-service
(`MarkConversationsClosedByPair`). Closed conversations refuse new
messages at the send-path gate (`ErrMatchClosed`).

---

## 3. Observability

### 3.1 Metrics (Prometheus)

All 30 services expose `/metrics` on their HTTP port. Default
Prometheus registry collects:

- HTTP: `*_http_requests_total`, `*_http_request_duration_seconds`
- DB pool: `*_db_pool_acquire_count`, `*_db_pool_idle_conns`,
  `*_db_pool_total_conns`
- Kafka consumer: `kafka_consumer_lag` (topic, partition, group)
- Custom: rate-limit hits, cache hits/misses, counter flush stats

Scrape config in `Architecture/docker/prometheus/prometheus.yml`.

### 3.2 Tracing (W3C OpenTelemetry)

Cross-service trace propagation works via the W3C `traceparent` header
through:
- HTTP middleware (`shared/middleware/OtelTracing`)
- Kafka producer (outbox + direct publish inject traceparent into
  message headers)
- Internal HTTP clients (identity, payments, courier — wrapped with
  `otelhttp.NewTransport`)
- MinIO client (same)

Jaeger UI at `localhost:16686` in the dev compose. Sampling defaults to
100% in dev (`OTEL_TRACES_SAMPLER_ARG=1.0`), should drop to 0.1 in prod.

### 3.3 Logging

All services use `log/slog` with structured JSON output. Every log line
carries `trace_id` + `span_id` when a request span is active
(`shared/middleware/Logger` enrichment). No `fmt.Println` / `log.Printf`
in production code paths.

---

## 4. Security posture

### 4.1 Network model

- **Public surface:** api-gateway on port 8080 only. All other services
  bind to internal network.
- **Service-to-service:** `X-Internal-Service-Key` header required on
  every internal endpoint via `shared/middleware/RequireInternalKey`.
  The gateway strips any inbound copy from public clients and injects
  the trusted one before forwarding.
- **dating-service P0-2:** gated 2026-05-25 — no longer accepts raw
  `X-User-Id` from anywhere except the gateway.
- **chat-service P0-3:** internal-only `POST
  /v1/chat/conversations/dating-match` endpoint verifies internal key
  in-handler.

### 4.2 Authentication

- Public auth: JWT (HS256), refresh token rotation, 15-minute access
  token TTL.
- Per-device sessions in `auth.sessions` table.
- 2FA: TOTP secrets encrypted at rest (`AES-256-GCM via
  TOTP_ENCRYPTION_KEY`).
- Recovery codes: bcrypt-hashed in both Redis (hot) and PostgreSQL
  (durable fallback).

### 4.3 Rate limiting

Redis sliding-window across:
- auth-service: OTP 5/phone/10min, login 10/IP/15min + 5/identifier/15min
- post-service: per-user post create + comment create
- graph-service: 200 follows/24h, 30 connection-requests/24h
- chat-service: 60 DM/60s, 20 message-requests/24h
- feed-service: 120 feed reads/min/user
- notification-service: 60 bulk-unread reads/min/user
- search-service: same pattern
- Various: per-action quotas in `ratelimit` packages

All "fail-CLOSED" for security-critical endpoints; "fail-OPEN" for
performance-only paths.

### 4.4 Input validation + injection

- All Postgres queries are parameterised via `pgx` (no string
  concatenation).
- All user-input fields validated via Gin `binding:"required"` tags +
  `binding:"max=N"`.
- File uploads scanned via media-service CSAM stub interface (real
  scanner plugs in via `MEDIA_SCANNER_ENABLED=true`).

### 4.5 Adult-content age gate (dating)

Discovery query strictly filters `birth_date IS NOT NULL AND age >= 18`.
Server-side enforcement on profile activation, sparks, chat send.
Per-PRODUCTION_GAP_ANALYSIS.md §P0-5.

---

## 5. Known limitations + next-quarter work

These are areas where the platform works at current scale but will
need work for sustained growth.

### 5.1 Single-region today

All services run in a single docker-compose / Kubernetes region. Going
multi-region needs:
- Cross-region Kafka MirrorMaker for the events topic
- Cross-region Redis (or per-region with cache invalidation events)
- ScyllaDB cluster spanning regions (it supports this natively)
- WS gateway per region with sticky-session via the realtime token

### 5.2 Discovery scale (PRODUCTION_GAP_ANALYSIS.md §P0-10)

Dating-service candidate query still uses `ORDER BY random()` over a
PG bounded fetch. Works fine up to a few hundred-thousand users per
metro. For multi-million per-metro, the §P0-10 plan calls for:
- PostGIS or geohash bbox prefiltering
- Materialized candidate features (refreshed every 15 min)
- Redis sorted-set candidate inbox per viewer
Three-phase plan documented in `dating/PHASE_0_TEST_PLANS.md`.

### 5.3 Live-v2 chat overlay

`live-service-v2` doesn't yet have a per-room chat overlay (v1 does).
Reusing chat-service per-stream conversations is the planned path; not
shipped yet.

### 5.4 Dating admin console (§P0-8)

`/admin/dating` console not built; reports/panic/photo-moderation
queues exist in the data layer but lack a UI. Test plan in
`PHASE_0_TEST_PLANS.md`. Phase 2 deliverable.

### 5.5 Fake-account risk scoring (§P0-7)

Risk-score table not yet built. Trust-tier model exists (phone /
selfie / Aadhaar verification ladder); per-account aggregate risk
scoring is Phase 2. Test plan in `PHASE_0_TEST_PLANS.md`.

### 5.6 Web Postmatch session redesign (§P1-5)

Postmatch web stores tokens in `localStorage`. The redesign moves
to httpOnly Secure SameSite cookies + CSRF + BFF. Not blocking for
public test launch but should land before scale rollout.

---

## 6. Deployment checklist

Pre-flight before any production deploy.

### 6.1 Infrastructure
- [ ] PostgreSQL 14+ with 5 logical DBs: `app`, `identity_db`, `chat_db`,
      `commerce_db`, `feed_db` (separate clusters if scaling)
- [ ] Redis 7+ (cluster mode if >50GB memory or >100k ops/sec)
- [ ] ScyllaDB cluster (or AWS Keyspaces) for feed + notifications
- [ ] OpenSearch cluster (1.x or 2.x) for search indexes
- [ ] Redpanda/Kafka cluster for events backbone
- [ ] MinIO or S3-compatible blob store for media + invoices + recordings
- [ ] LiveKit Cloud subscription OR self-hosted LiveKit server for live-v2

### 6.2 Secrets (see §7 for the full list)
- [ ] `INTERNAL_SERVICE_KEY` rotated to a strong 64-char hex
- [ ] `JWT_SECRET` rotated to a strong 64-char hex
- [ ] `TOTP_ENCRYPTION_KEY` set (64 hex chars, AES-256 key)
- [ ] `RAZORPAY_KEY_ID` + `RAZORPAY_KEY_SECRET` from your Razorpay account
- [ ] `LIVEKIT_API_KEY` + `LIVEKIT_API_SECRET` + `LIVEKIT_URL` +
      `LIVEKIT_WEBHOOK_SECRET` from LiveKit Cloud
- [ ] `FCM_PROJECT_ID` + `FCM_SERVICE_ACCOUNT_KEY` for Android push
- [ ] `APNS_*` for iOS push
- [ ] AWS-side: IAM roles for RDS / ElastiCache / MSK / S3 / etc

### 6.3 Migrations
- [ ] Run each service's `database/setup.sql` against its database
- [ ] Run versioned migrations in `database/migrations/` ordered by
      filename prefix
- [ ] Verify schema parity via `tools/schemacheck` (catches CHECK-constraint
      drift between code expectations and live schema)

### 6.4 Backfills (run once on bootstrap)
- [ ] `cd Architecture && go run ./services/search-service/cmd/backfill
      -entity all` — seeds the six OpenSearch indexes from PG
- [ ] (Optional) Replay UserRegistered events from identity-platform
      outbox to backfill app.users — see
      `memory/app_users_projection_drift.md` for the script

### 6.5 Observability
- [ ] Prometheus scrape config points at every service's `/metrics`
- [ ] Grafana dashboards imported (HTTP, DB pool, Kafka lag are baseline;
      per-service custom dashboards in `Architecture/docker/grafana/`)
- [ ] Alert rules configured in Prometheus / Alertmanager:
  - p95 latency > 500ms on any service
  - Kafka consumer lag > 10k messages
  - DB pool exhaustion (acquired > 0.8 * max)
  - Counter flush worker stalled > 5min
  - Saga reconciler failure rate > 10/min
  - Live Egress failures
  - Push delivery failure rate > 5%

### 6.6 Smoke tests
- [ ] User registration → email verify → login → post create → home feed
- [ ] DM send + receive (real-time arrival, no refresh)
- [ ] Dating profile create → spark → match → chat
- [ ] Commerce checkout (Razorpay test mode) → order confirmation
- [ ] Live-v2: broadcaster goes live, viewer connects, broadcast ends,
      VOD available

---

## 7. External services required

The complete list of third-party / external dependencies. Provide
credentials in one block when ready to deploy.

### 7.1 Payment + identity
- **Razorpay** — payment gateway (existing integration).
  Vars: `RAZORPAY_KEY_ID`, `RAZORPAY_KEY_SECRET`,
  `RAZORPAY_WEBHOOK_SECRET`
- **Setu** — bill-pay BBPS aggregator.
  Vars: `SETU_CLIENT_ID`, `SETU_CLIENT_SECRET`, `SETU_BASE_URL`
- **DigiLocker** — Aadhaar verification for dating selfie trust tier.
  Vars: `DIGILOCKER_CLIENT_ID`, `DIGILOCKER_CLIENT_SECRET`,
  `DIGILOCKER_BASE_URL`

### 7.2 Push notifications
- **FCM** (Firebase Cloud Messaging) — Android + Web push.
  Vars: `FCM_PROJECT_ID`, `FCM_SERVICE_ACCOUNT_KEY` (full service-account
  JSON as a single env-var value or path to a mounted file)
- **APNs** — iOS push.
  Vars: `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_BUNDLE_ID`,
  `APNS_PRIVATE_KEY` (or `APNS_KEY_PATH` to a mounted .p8 file)

### 7.3 Live streaming
- **LiveKit Cloud** (or self-hosted) — browser broadcast SFU + Egress.
  Vars: `LIVEKIT_URL` (wss://...), `LIVEKIT_API_KEY`,
  `LIVEKIT_API_SECRET`, `LIVEKIT_WEBHOOK_SECRET`,
  `LIVE_RECORDING_PUBLIC_BASE_URL` (public URL prefix for the recording
  bucket, usually a CloudFront distribution)

### 7.4 Object storage (production replacement for MinIO)
- **AWS S3** or equivalent. Buckets needed:
  - `<prefix>-media` — user uploads
  - `<prefix>-live-recordings` — VOD outputs
  - `<prefix>-commerce-invoices` — invoice PDFs/HTML
  - `<prefix>-search-error-reports` — bulk-import error CSVs
  Vars: `MINIO_ENDPOINT` (or S3 endpoint), `MINIO_ACCESS_KEY`,
  `MINIO_SECRET_KEY`, `MINIO_PUBLIC_ENDPOINT` (public/CloudFront URL),
  `MINIO_USE_SSL=true`

### 7.5 Observability (managed) — optional
- **Grafana Cloud** / Datadog / New Relic — Prometheus scrape +
  dashboards + alerts. No code changes; just point scrape config at
  the service `/metrics` endpoints.
- **Sentry** — error tracking. Most services have a Sentry init
  scaffold; just provide `SENTRY_DSN_*` per service.

### 7.6 Email + SMS (for notifications)
- **SES** or SendGrid — transactional email.
  Vars: `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`,
  `SMTP_FROM`
- **MSG91** / Twilio — SMS OTP delivery.
  Vars: `MSG91_AUTH_KEY`, `MSG91_SENDER_ID`, `MSG91_TEMPLATE_ID` (or
  Twilio equivalents)

### 7.7 Maps + geocoding
- **Google Maps / Mapbox** — for dating safe-meet, mopedu rider
  geocoding, etc.
  Vars: `GOOGLE_MAPS_API_KEY` (or `MAPBOX_ACCESS_TOKEN`)

### 7.8 KYC + content scanning (optional, recommended)
- **Sandbox.in / Karza** — KYC validation for seller onboarding,
  partner-driver onboarding (mopedu).
  Vars: `KYC_PROVIDER_BASE_URL`, `KYC_PROVIDER_API_KEY`
- **CSAM-detection vendor** (Hive AI, AWS Rekognition Content
  Moderation, or self-hosted PhotoDNA-equivalent). Wired via
  `MEDIA_SCANNER_ENABLED=true` + `MEDIA_SCANNER_PROVIDER` env.

### 7.9 Anthropic / AI moderation (optional)
- For dating-service moderation layer-2 and search relevance reranking
  (Phase 3).
  Vars: `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`

### 7.10 AWS infrastructure (if deploying to AWS)
- IAM credentials for: RDS, ElastiCache, MSK, ECR/EKS, S3,
  CloudFront, Route53, ACM, Secrets Manager, KMS

---

## 8. Branch + commit summary (this program)

This multi-session program landed on
`feat/vchat-rebrand-realtime-ui`. Major work areas:

- **Identity + auth hardening** — A1-A18 audit closeouts, TOTP
  encryption, recovery-code PG fallback, refresh-token fingerprinting,
  cascade-delete FKs
- **Audit closeouts across post / chat / calls / media / feed / graph /
  search / communities / groups / channels / qa / notifications /
  commerce / identity** — all P0/P1 audit memos closed (test plans for
  deferred Mediums)
- **Scaling** — sharded counters across 13 hot rows, Kafka
  partitioning by actorID, Redis ZSET presence, dating saga retry
- **Modules shipped** — M1 conversation presence, live-streaming v2
  (LiveKit), search relevance + 6-entity indexing, full Dating Phase 0
  + Phase 1
- **Observability** — W3C trace propagation, Prometheus on all 30
  services, structured slog throughout
- **Frontend** — postbook-ui Postmatch full rebase, mobile Pulse + live
  + search clients, web safety center

Total commit count this program: ~100+. Major branches still on
`feat/vchat-rebrand-realtime-ui` awaiting merge.

---

## 9. Next session

When you start the next session, the priorities are:

1. **Dating admin console** (§P0-8) — start the `/admin/dating` UI build
2. **Live-v2 chat overlay** — wire chat-service per-stream conversation
3. **Postmatch session redesign** (§P1-5) — localStorage → cookies
4. **Discovery PostGIS migration** (§P0-10 Phase A) — geohash prefix
5. **Mobile + web feature parity polish** as users report gaps
6. **AWS infrastructure stand-up** — when you're ready (memory has
   `user_aws_infra_guidance.md` for this; ping me when you start)

Everything from this multi-session program is production-ready under
the constraints in §5. Ship when secrets in §7 land.
