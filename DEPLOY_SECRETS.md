# Deploy Secrets & Config Reference

Consolidated env-var inventory for production deployment. Sweeps every
`os.Getenv` / `env("...")` call across `Architecture/services/`,
`identity-platform/`, `chat-service/`, and `postbook-ui/`.

Generate random secrets with:

```bash
# 64-char hex (32 random bytes)
openssl rand -hex 32

# ECDSA P-256 PEM (for MINI_APP_SESSION_PRIVATE_KEY_PEM)
openssl ecparam -name prime256v1 -genkey -noout
```

The template file `Architecture/docker/.env.production.example` mirrors
the list below and can be edited in place.

---

## 1. Money & payments — hard blockers

Without these, **real money will not move**. The payments service
refuses to boot when `DEPLOY_ENV=production` and Razorpay is
unconfigured, unless `PAYMENTS_ALLOW_STUB=true` is explicitly set.

| Secret | Source | Used by | Without it |
|---|---|---|---|
| `RAZORPAY_KEY_ID` | Razorpay dashboard → Settings → API Keys | payments-service, commerce-service, frontend | Stub gateway approves every payment |
| `RAZORPAY_KEY_SECRET` | Same | payments-service | Same |
| `RAZORPAY_WEBHOOK_SECRET` | Razorpay dashboard → Settings → Webhooks | payments-service `/v1/payments/webhook` | Webhook accepts unsigned calls (dev fallback) |
| `NEXT_PUBLIC_RAZORPAY_KEY_ID` | Same key_id (it is public) | postbook-ui checkout SDK | Checkout button won't render |
| `RAZORPAY_BASE_URL` | `https://api.razorpay.com/v1` | payments-service | Defaults to sandbox |
| `RAZORPAY_MODE` | Literal `http` (else `mock`) | payments-service | Mock client |

**Required dashboard config:** Configure Razorpay to POST refund +
payment events to `https://<api-gateway-domain>/v1/payments/webhook`.

---

## 2. Identity & security — hard blockers

These secrets are required for every service to start safely. The
services log loud warnings but will run anyway if they are missing —
do not deploy without them.

| Secret | Generate with | Used by | Without it |
|---|---|---|---|
| `JWT_SECRET` | `openssl rand -hex 32` | auth-service, api-gateway | Token signing fails |
| `INTERNAL_SERVICE_KEY` | `openssl rand -hex 32` | Every Go service | Inter-service auth disabled — services reachable from anywhere inside the pod |
| `TOTP_ENCRYPTION_KEY` | `openssl rand -hex 32` (AES-256) | auth-service | 2FA secrets stored plaintext in Postgres |
| `REALTIME_TOKEN_SECRET` | `openssl rand -hex 32` | notification-service, ws-gateway | WS gateway token verification fails |
| `CHAT_PROXY_SIGNING_SECRET` | `openssl rand -hex 32` | chat-service | Chat proxy auth fails |
| `MINI_APP_SESSION_PRIVATE_KEY_PEM` | `openssl ecparam -name prime256v1 -genkey -noout` | auth-service | Mini-app session minting fails |
| `BCRYPT_COST` | Integer, default `10`; recommend `12` for prod | auth-service | Uses dev default |

---

## 3. OAuth providers — only if you ship social sign-in

| Secret | Source | Notes |
|---|---|---|
| `OAUTH_GOOGLE_CLIENT_ID` | Google Cloud Console → APIs & Services → Credentials → OAuth client | Web application type |
| `OAUTH_GOOGLE_CLIENT_SECRET` | Same | Add `https://<your-domain>/v1/auth/oauth/google/callback` to authorized redirect URIs |
| `OAUTH_APPLE_CLIENT_ID` | Apple Developer → Identifiers → Services ID | |
| `OAUTH_APPLE_CLIENT_SECRET` | JWT generated from your `.p8` key — see Apple Sign In docs | Rotates every 6 months |
| `OAUTH_GITHUB_CLIENT_ID` | GitHub → Settings → Developer settings → OAuth Apps | |
| `OAUTH_GITHUB_CLIENT_SECRET` | Same | Callback URL: `https://<your-domain>/v1/auth/oauth/github/callback` |
| `FRONTEND_URL` | `https://<your-domain>` | OAuth pre-creation redirects here to `/auth/oauth/complete-signup` |

---

## 4. Push notifications — required for mobile push delivery

| Secret | Source | Notes |
|---|---|---|
| `FCM_PROJECT_ID` | Firebase Console → Project settings → General | Android push |
| `FCM_SERVICE_ACCOUNT_KEY` | Firebase Console → Service accounts → Generate new private key | Full JSON contents as single env var (or mount as a file) |
| `APNS_TEAM_ID` | Apple Developer → Membership | iOS push |
| `APNS_KEY_ID` | Apple Developer → Keys → APNs auth key | |
| `APNS_PRIVATE_KEY_PATH` | Path to mounted `.p8` file | |
| `APNS_BUNDLE_ID` | Your iOS app bundle ID | |

> ⚠️ **Wiring gap**: `NewFCMPusher` and `NewAPNSPusher` exist but are
> not yet constructed in `notification-service/cmd/server/main.go`.
> The secrets above are read into the env, but a small wiring change
> is needed before push actually fires.

---

## 5. Live streaming

| Secret | Source | Used by |
|---|---|---|
| `LIVEKIT_URL` | LiveKit Cloud (`wss://...`) or self-hosted endpoint | live-service-v2, frontend |
| `LIVEKIT_API_KEY` | LiveKit dashboard → API Keys | live-service-v2 |
| `LIVEKIT_API_SECRET` | Same | live-service-v2 |
| `LIVEKIT_WEBHOOK_SECRET` | LiveKit dashboard → Webhooks | live-service-v2 ingest webhook |
| `ICE_SERVERS_JSON` | Custom TURN/STUN provider | Frontend WebRTC (LiveKit Cloud bundles defaults) |
| `LIVE_INGEST_URL` / `LIVE_PLAYBACK_BASE_URL` / `LIVE_PUBLISH_URL_TEMPLATE` | RTMP ingest + HLS playback URLs from your LiveKit/streaming provider | live-service-v2 |

---

## 6. Courier / fulfillment

| Secret | Source | Used by | Without it |
|---|---|---|---|
| `COURIER_PROVIDER` | Literal `shiprocket` (else `stub`) | commerce-service | Stub courier auto-marks "shipped" with no real label |
| `SHIPROCKET_EMAIL` | Shiprocket dashboard | commerce-service | |
| `SHIPROCKET_PASSWORD` | Same | commerce-service | |
| `SHIPROCKET_PICKUP_LOCATION_ID` | Shiprocket → Pickup locations | commerce-service | Bookings fail validation |
| `SHIPROCKET_WEBHOOK_TOKEN` | Shiprocket → Webhooks | commerce-service | Webhook events ignored |
| `SHIPROCKET_WEBHOOK_HMAC` | Same | commerce-service | Webhooks accept unsigned calls |

---

## 7. KYC / Aadhaar verification (dating + rider)

| Secret | Source | Notes |
|---|---|---|
| `DIGILOCKER_MODE` | Literal `http` (else `mock`) | dating-service, rider-service |
| `DIGILOCKER_BASE_URL` | DigiLocker partner portal | |
| `DIGILOCKER_API_KEY` | DigiLocker partner portal | |
| `DIGILOCKER_CLIENT_ID` | DigiLocker partner portal | |
| `DIGILOCKER_REDIRECT_URI` | `https://<your-domain>/auth/digilocker/callback` | |
| `DIGILOCKER_AUTHORIZE_URL` | From DigiLocker partner docs | |
| `DIGILOCKER_SANDBOX` | `true` for staging, `false` for prod | |

---

## 8. Bill-pay / wallet — only if shipping BBPS or wallet

| Secret | Source | Notes |
|---|---|---|
| `SETU_MODE` | Literal `http` (else mock) | bill-pay-service |
| `SETU_CLIENT_ID` / `SETU_CLIENT_SECRET` | Setu BBPS partner portal | bill-pay-service |
| `SETU_BASE_URL` | Setu BBPS endpoint | |
| `SETU_WEBHOOK_SECRET` | Setu dashboard → Webhooks | |
| `BANK_PARTNER` | Sponsor bank ID (`yes_bank`, `icici`, etc) | wallet-service |
| `PARTNER_BANK_VPA` | Pooled VPA from sponsor bank | wallet-service |
| `PARTNER_BANK_POOL_REF` | Sponsor bank pool reference | wallet-service |
| `ICICI_API_KEY` | ICICI partner integration | wallet-service (only for ICICI rails) |

---

## 9. AI / moderation (optional)

| Secret | Source | Effect |
|---|---|---|
| `LLM_MODERATION_URL` | Internal LLM gateway or external provider | dating-service moderation layer 2 |
| `LLM_MODERATION_API_KEY` | Same | If unset, falls back to heuristic mock |
| `ANTHROPIC_API_KEY` | Anthropic console | AI service features |
| `OPENAI_API_KEY` | OpenAI dashboard | media-service Whisper transcription |
| `OPENAI_WHISPER_MODEL` | Default `whisper-1` | Override only if needed |
| `NEXT_PUBLIC_GEMINI_API_KEY` | Google AI Studio | Frontend AI-assist features (if shipped) |

---

## 10. Email — required for OTP + transactional mail

| Secret | Notes |
|---|---|
| `SMTP_HOST` | Any SMTP provider — SES / SendGrid / Mailgun / Postmark |
| `SMTP_PORT` | Usually `587` (STARTTLS) or `465` (SMTPS) |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Provider credentials |
| `SMTP_FROM_ADDR` | e.g. `noreply@atpost.in` |
| `SMTP_FROM_NAME` | e.g. `AtPost` |

> ⚠️ **No SMS provider adapter is wired today.** OTP delivery via
> phone in production needs an adapter (Twilio / MSG91 / Gupshup are
> common India options). Email OTP works out of the box once SMTP
> is set. `OTP_BYPASS_CODE` is a dev override — must be empty in prod.

---

## 11. Infrastructure — your cloud provider

### Postgres (5 databases)

The platform splits data across separate Postgres databases for
isolation:

| Database | DSN env | Used by |
|---|---|---|
| `app_db` | `POSTGRES_DSN` | post, feed, graph, community, etc. |
| `identity_db` | `POSTGRES_DSN` (in identity-platform services) | auth, user, profile |
| `chat_db` | `POSTGRES_DSN` (in chat-service) | message-service |
| `commerce_db` | `POSTGRES_DSN` (in commerce, payments, orders) | commerce + payments |
| `feed_db` | `POSTGRES_DSN` (in feed-service) | feed |

| Secret | Notes |
|---|---|
| `POSTGRES_DSN` | `postgresql://user:pass@host:5432/dbname?sslmode=require` per service |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` | App user credentials |
| `IDENTITY_POSTGRES_USER` / `IDENTITY_POSTGRES_PASSWORD` | Separate credentials for the identity DB |
| `POSTGRES_MAX_CONNS` / `POSTGRES_MIN_CONNS` | Pool sizing — defaults 25 / 5, scale up per service in prod |

### Redis

| Secret | Notes |
|---|---|
| `REDIS_ADDR` | `host:6379` |
| `REDIS_USERNAME` / `REDIS_PASSWORD` / `REDIS_DB` | Optional auth |
| `REDIS_TLS_ENABLED` | `true` for managed Redis (ElastiCache, etc) |
| `REDIS_TLS_INSECURE_SKIP_VERIFY` | `false` in prod |
| `REDIS_TLS_SERVER_NAME` | TLS SNI hostname |
| `REDIS_CA_CERT_FILE` / `REDIS_CLIENT_CERT_FILE` / `REDIS_CLIENT_KEY_FILE` | mTLS cert paths |

### Kafka / Redpanda

| Secret | Notes |
|---|---|
| `KAFKA_BROKERS` | Comma-separated `host:9092` list |
| `KAFKA_TLS_ENABLED` | `true` for MSK |
| `KAFKA_TLS_INSECURE_SKIP_VERIFY` | `false` in prod |
| `KAFKA_TLS_SERVER_NAME` | TLS SNI |
| `KAFKA_CA_CERT_FILE` / `_CLIENT_CERT_FILE` / `_CLIENT_KEY_FILE` | mTLS cert paths |
| `KAFKA_SASL_MECHANISM` | `SCRAM-SHA-256` for MSK SASL |
| `KAFKA_SASL_USERNAME` / `KAFKA_SASL_PASSWORD` | SASL creds |
| `KAFKA_DIAL_TIMEOUT` | Default `30s` |

Per-domain topic overrides (defaults usually fine):

```
KAFKA_TOPIC, KAFKA_QA_TOPIC, KAFKA_DATING_TOPIC, KAFKA_WALLET_TOPIC,
KAFKA_BILLPAY_TOPIC, KAFKA_RIDER_TOPIC, KAFKA_FOOD_TOPIC,
KAFKA_MONETIZATION_TOPIC, KAFKA_CALL_NOTIFICATIONS_TOPIC,
CHAT_KAFKA_TOPIC, CHAT_KAFKA_GROUP_ID, IDENTITY_KAFKA_TOPIC
```

### Object storage (MinIO / S3)

| Secret | Notes |
|---|---|
| `MINIO_ENDPOINT` | S3 endpoint (e.g. `s3.amazonaws.com` or MinIO host) |
| `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY` | IAM access key + secret (or MinIO root) |
| `MINIO_PUBLIC_ENDPOINT` | Browser-reachable URL — e.g. `https://cdn.<your-domain>` so presigned URLs resolve from outside the cluster |
| `MINIO_USE_SSL` | `true` for S3 |
| `MINIO_REGION` | `ap-south-1` or your region |
| `MINIO_BUCKET` | Default bucket for media |
| `COMMERCE_INVOICE_BUCKET` | Bucket for invoice PDFs |
| `FOOD_BLOB_BUCKET` | Bucket for FiGo restaurant images |
| `MINIO_BUCKET_LIVE_RECORDINGS` | Bucket for live recording VODs |
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | Only if self-hosting MinIO |

### Other stores

| Secret | Notes |
|---|---|
| `OPENSEARCH_URL` | OpenSearch Service endpoint |
| `OPENSEARCH_INITIAL_ADMIN_PASSWORD` | Only if self-hosting |
| `SCYLLA_HOSTS` | Comma-separated Scylla / Cassandra hosts (or AWS Keyspaces endpoint) |

---

## 12. Observability — strongly recommended

| Secret | Notes |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP HTTP endpoint — Jaeger / Tempo / Datadog OTLP / Honeycomb |
| `OTEL_TRACES_SAMPLER_ARG` | `0.1` (10% sampling) for prod, `1.0` for dev |
| `GF_SECURITY_ADMIN_PASSWORD` | Grafana admin (only if self-hosting) |
| `LOG_LEVEL` | `info` for prod, `debug` for dev |
| `LOG_ADD_SOURCE` | `false` for prod |

---

## 13. CORS / domain / proxy

| Secret | Notes |
|---|---|
| `DOMAIN` | Your prod domain |
| `CORS_ORIGINS` | Comma-separated frontend origins |
| `ALLOWED_ORIGINS` | Same — both names checked |
| `COOKIE_DOMAIN` | `.your-domain.com` for cross-subdomain cookies |
| `COOKIE_SECURE` | `true` in prod |
| `TRUSTED_PROXIES` | ALB/CloudFront CIDR list — required for `c.ClientIP()` to resolve correctly behind a load balancer. Matters for A3 OTP IP rate-limit + A13 anomaly detection. |
| `WS_ALLOW_QUERY_TOKEN` | `false` in prod (forces header-based auth on WS) |

---

## 14. Service-to-service URLs

Internal HTTP between services. Defaults work for Docker Compose; set
to actual DNS names in Kubernetes (e.g.
`http://auth-service.identity.svc.cluster.local:8081`).

```
AUTH_SERVICE_URL, USER_SERVICE_URL, PROFILE_SERVICE_URL,
GRAPH_SERVICE_URL, POST_SERVICE_URL, FEED_SERVICE_URL,
SEARCH_SERVICE_URL, NOTIFY_SERVICE_URL, NOTIFICATION_SERVICE_URL,
MEDIA_SERVICE_URL, MESSAGE_SERVICE_URL, CALL_SERVICE_URL,
COMMUNITY_SERVICE_URL, GROUP_SERVICE_URL, CHANNEL_SERVICE_URL,
QA_SERVICE_URL, DATING_SERVICE_URL, COMMERCE_SERVICE_URL,
PAYMENTS_SERVICE_URL, RIDER_SERVICE_URL, FOOD_SERVICE_URL,
WALLET_SERVICE_URL, BILL_PAY_SERVICE_URL, LIVE_SERVICE_URL,
MONETIZATION_SERVICE_URL, MEMORIES_SERVICE_URL, AI_SERVICE_URL,
ADMIN_SERVICE_URL, TRUST_SAFETY_SERVICE_URL, FLAGS_SERVICE_URL,
SUGGESTION_SERVICE_URL, APP_USER_SERVICE_URL, FEATURE_FLAG_SERVICE_URL,
WS_GATEWAY_URL
```

---

## 15. Tunables (sensible defaults — override only if needed)

### Commerce
```
COMMERCE_COMMISSION_PCT=5.0
COMMERCE_PLATFORM_FEE_PCT=2.0
COMMERCE_TDS_PCT=1.0
PAYMENTS_ALLOW_STUB=false        # MUST be false in prod
MEDIA_SCANNER_ENABLED=true
MEDIA_SCANNER_ALLOW_STUB=false   # MUST be false in prod
```

### Auth
```
ACCESS_TOKEN_TTL=15m
REFRESH_TOKEN_TTL=720h           # 30 days
OTP_DIGITS=6
OTP_EXPIRY=5m
OTP_MAX_ATTEMPTS=5
OTP_BYPASS_CODE=                 # MUST be empty in prod
RATE_LIMIT_ENABLED=true
LOGIN_ANOMALY_ENFORCE=shadow     # flip to "enforce" after step-up UI is verified
TWOFA_ISSUER=AtPost
MINI_APP_SESSION_TTL=5m
MINI_APP_SESSION_ISSUER=atpost-mini-app-runtime
MINI_APP_SESSION_KEY_ID=mini-app-session-1
```

### Other
```
HTTP_PORT=                       # per-service default
METRICS_PORT=
DPDP_POLICY_VERSION=             # set to current consent policy version
DEFAULT_MOBILE_CIRCLE=           # rider service region
AUDIT_LOG_RETENTION_DAYS=90
INVOICE_WKHTMLTOPDF_BIN=         # path to wkhtmltopdf binary on the image
```

---

## 16. Frontend (postbook-ui, Next.js — build-time)

| Secret | Notes |
|---|---|
| `NEXT_PUBLIC_API_BASE_URL` | Your api-gateway URL |
| `NEXT_PUBLIC_WS_BASE_URL` | Your ws-gateway URL |
| `NEXT_PUBLIC_SITE_URL` | Your prod domain |
| `NEXT_PUBLIC_RAZORPAY_KEY_ID` | Same as backend `RAZORPAY_KEY_ID` |
| `NEXT_PUBLIC_POSTMATCH_API_URL` | PostMatch BFF base path |
| `NEXT_PUBLIC_GEMINI_API_KEY` | Only if AI-assist features are shipped |
| `NEXT_PUBLIC_AUTH_STRATEGY` | Default `remote` |
| `NEXT_PUBLIC_AUTH_LOGIN_PATH` | Default `/v1/auth/login` |
| `NEXT_PUBLIC_AUTH_REGISTER_PATH` | Default `/v1/auth/register` |
| `NEXT_PUBLIC_ENABLE_STUB_PAYMENTS` | `false` in prod |

---

## 17. TLS certificate volumes

For mTLS to managed Redis / Kafka:

| Secret | Notes |
|---|---|
| `TRANSPORT_CERTS_DIR` | Host path containing `ca.crt`, `redis.crt`, `redis.key`, `redpanda.crt`, `redpanda.key` |
| `REDPANDA_TLS_CERT_FILE` / `_KEY_FILE` / `_CA_CERT_FILE` | Redpanda broker certs |
| `REDPANDA_SUPERUSER_USERNAME` / `_PASSWORD` | Only if self-hosting Redpanda |

---

## Minimum-viable launch set

The smallest set required to deploy the core stack with real payments
and email OTP (no push, no OAuth, no KYC):

```bash
# Identity & security
JWT_SECRET
INTERNAL_SERVICE_KEY
TOTP_ENCRYPTION_KEY
REALTIME_TOKEN_SECRET
CHAT_PROXY_SIGNING_SECRET
MINI_APP_SESSION_PRIVATE_KEY_PEM

# Infrastructure
POSTGRES_DSN                      # × 5 databases
POSTGRES_USER / POSTGRES_PASSWORD
IDENTITY_POSTGRES_USER / IDENTITY_POSTGRES_PASSWORD
REDIS_ADDR / REDIS_USERNAME / REDIS_PASSWORD
KAFKA_BROKERS / KAFKA_SASL_USERNAME / KAFKA_SASL_PASSWORD
MINIO_ENDPOINT / MINIO_PUBLIC_ENDPOINT
MINIO_ACCESS_KEY / MINIO_SECRET_KEY
OPENSEARCH_URL
SCYLLA_HOSTS

# Money
RAZORPAY_KEY_ID
RAZORPAY_KEY_SECRET
RAZORPAY_WEBHOOK_SECRET
NEXT_PUBLIC_RAZORPAY_KEY_ID

# Email (for OTP)
SMTP_HOST / SMTP_PORT
SMTP_USERNAME / SMTP_PASSWORD
SMTP_FROM_ADDR / SMTP_FROM_NAME

# Domain
DOMAIN
CORS_ORIGINS / ALLOWED_ORIGINS
COOKIE_DOMAIN
COOKIE_SECURE=true
TRUSTED_PROXIES

# Frontend build
NEXT_PUBLIC_API_BASE_URL
NEXT_PUBLIC_WS_BASE_URL
NEXT_PUBLIC_SITE_URL
```

Everything else (OAuth, push, courier, KYC, BBPS, AI, live streaming)
is feature-gated and can be added when you wire each feature live.

---

## Known wiring gaps (not secrets, but worth flagging)

These are code wiring issues, not missing secrets:

1. **Push notifications** — `NewFCMPusher` / `NewAPNSPusher` exist
   but aren't constructed in `notification-service/cmd/server/main.go`.
   Push won't fire even with `FCM_*` / `APNS_*` secrets set until the
   wiring lands.

2. **SMS provider** — no adapter for Twilio / MSG91 / Gupshup wired.
   Phone-OTP defers to `OTP_BYPASS_CODE` (dev) or email-only (prod).

3. **Maps / geocoding** — not wired anywhere. Address autocomplete
   would need a separate adapter (Mapbox / Google Maps / LocationIQ).

4. **CAPTCHA** — no reCAPTCHA / hCaptcha / Turnstile gate on signup.

5. **Mobile 2FA UI** — login surfaces a placeholder snackbar when
   the server returns `requires_2fa`. A13 anomaly step-up is wired
   (mobile + web) but legacy 2FA mobile screen is pre-existing gap.

---

## Pre-deploy operational checklist

Code-level work is done; these are operational steps before flipping
prod traffic:

1. **Migration `006_amount_minor`** — backfill manually off-hours
   before letting startup apply `SET NOT NULL`. Verify zero NULLs
   in `payment_intents.amount_minor` before proceeding.
2. **Configure Razorpay webhook URL** in their dashboard to point at
   `https://<api-gateway>/v1/payments/webhook`.
3. **Verify TLS certs** mounted at `TRANSPORT_CERTS_DIR` for managed
   Redis + Kafka mTLS.
4. **Sandbox-test Razorpay** end-to-end (partial + full refund) before
   flipping `RAZORPAY_MODE=http`.
5. **Merge** `feat/vchat-rebrand-realtime-ui` → `main`.
6. **Deploy with `LOGIN_ANOMALY_ENFORCE=shadow`** initially. Verify
   step-up UI works (web + mobile), then flip to `enforce`.

---

## Reference

The editable template lives at:

```
Architecture/docker/.env.production.example
```

Service-by-service env consumption can be re-derived with:

```bash
grep -rohE 'os\.Getenv\("[A-Z_]+"\)|env\("[A-Z_]+"' \
  Architecture/services/*/cmd/server/main.go \
  identity-platform/services/*/cmd/server/main.go \
  chat-service/services/*/cmd/server/main.go \
  | sort -u
```
