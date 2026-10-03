# First production deployment on AWS — plan

Prepared 2 October 2026 from two read-only audits of this repo (branch `codex/module-01-02-launch-safety`): the deployment assets (`infra/terraform`, `charts/`, `deploy/`, `.github/workflows`) and the running system (services, data stores, third parties, boot-time secrets, launch switches).

Founder decisions this plan assumes: everything runs on AWS in a **new AWS account**; email is sent through AWS (Amazon SES); SMS is deferred; going live stays pilot-only at launch.

Nothing in this plan has been run. The repo's AWS design has never been applied (`docs/runbooks/aws-media-smoke.md`: "Nothing is deployed to AWS"). All costs below are rough estimates from list prices, not quotes — check them in the AWS Pricing Calculator before committing.

## 1. Where we stand

**Already in the repo**
- Terraform for Mumbai (`ap-south-1`): network, EKS (Kubernetes), Aurora PostgreSQL, Kafka (MSK), Redis-compatible cache (ElastiCache Valkey), OpenSearch, media bucket + CloudFront, WAF, ECR image registries, Secrets Manager with External Secrets, ArgoCD, Scylla on EKS, monitoring (Prometheus/Grafana, Loki, Tempo), Karpenter.
- One shared Helm chart and production values for 31 backend services and 11 web zones.
- GitHub Actions that build images, push them to ECR and bump image tags; a promote workflow for production.

**What blocks a first deployment today** (each is a task in section 5)
1. Kafka: Terraform creates MSK Serverless (IAM login only); our Kafka client only supports PLAIN/SCRAM. No topics are defined, and 8 topics used in code are not in the topic list at all.
2. Rendering: no ApplicationSet passes the AWS account id, so every chart fails to render; ArgoCD's project rejects this repository's URL; no repo credentials.
3. Images: the ECR workflow builds the 6 identity and chat services under the wrong names, so they are never built; values say `tag: latest` but repositories are immutable and CI pushes commit shas.
4. Certificates: no TLS certificate for `api.`, `ws.`, `app.`, `admin.cleestudio.com`; certificate and WAF references in values are placeholders.
5. Secrets: every service expects `atpost/prod/<service>` in Secrets Manager; Terraform creates none of them and there is no seeding script. post-service and rider-service are also missing required secrets in their values.
6. Permissions: service roles only allow Kafka. Missing: SES (identity-auth), Rekognition (media), KMS (commerce PII — the module exists but is not wired), S3 for commerce invoices and live recordings.
7. Storage: commerce and food write files with MinIO static keys only; they need S3 through the service role. `S3_BUCKET` in media's values does not match the bucket Terraform creates; the media KMS key does not let CloudFront read; no CloudFront signing key exists although media-service refuses to start in production without one.
8. Connections: search-service has no OpenSearch URL or credentials; no service sets the TLS flags ElastiCache requires; many services have no database or Kafka address in their production values.
9. Missing from the deployment set: food, reviewer, memories, feature-flag services (the gateway still routes to them); `suggestion-service` has values but no image registry or role.
10. The web: production values describe 11 micro-frontend zones from another repository. What we actually run and have been building is the single `postbook-ui` app and the admin console in `atpost-web-ui`. Neither has production deployment values.
11. Chart gaps: values use `migrationJob`, `webhookIngress` and `networkPolicy`, which the chart has no templates for (silently ignored) — so commerce migrations and the payment webhook entry would not exist.
12. Live video: nothing for LiveKit, recording (egress) or TURN on AWS.
13. Process: no `release/prod` branch; production is set to sync automatically although the documents say manual; Azure workflows still run and fail on every push; the Terraform CI role does not exist; no committed variable files for CI.
14. Safety: `OTP_BYPASS_CODE` has no production guard; port mismatches (identity-user 8082 vs 8110; ai-service set to identity-profile's port).
15. Email: no SES domain, DKIM or configuration set; a signed-in user cannot verify their email at all (only at registration).

## 2. Two decisions that shape everything

### 2.1 Size: start lean, grow later (recommended)

The Terraform as written is a full-scale design. For a launch with a small pilot audience it is far larger than needed.

| | As written in Terraform | Lean first stage (recommended) |
|---|---|---|
| Kubernetes nodes | 9–24 general + 3–6 memory + 3–6 system | 3–4 general (m7g.xlarge), autoscaling up to 8 |
| Service copies | 3–12 each | 1–2 each (2 for gateway, auth, post, chat) |
| PostgreSQL | Aurora r7g.large writer + reader | Aurora Serverless v2 (0.5–4 ACU) or one small instance, 7-day backups |
| Kafka | MSK Serverless | MSK Provisioned, 2 small brokers, SCRAM login (works with our client) |
| Cache | 3 × r7g.large | 1 primary + 1 replica, small |
| Search | 6 nodes | 1–2 small nodes |
| Scylla | 3 × r7g.xlarge | 3 smaller nodes (replication 3 kept) |
| NAT gateways | 3 | 1 |
| Rough monthly cost | about $5,000–7,000 | about $1,000–1,500 |

Both keep the same architecture, so growing later is a settings change, not a rebuild. Estimates exclude data transfer, CloudFront, Rekognition and LiveKit usage, which scale with real traffic.

### 2.2 Live video: stay on LiveKit Cloud at launch (recommended)

Self-hosting LiveKit on AWS needs public UDP load balancers, a TURN server, a recording (egress) fleet and its own scaling. Dev already uses a LiveKit Cloud project. Recommendation: create a separate production LiveKit Cloud project, point recordings at an S3 bucket in the new account (direct S3, no Cloudflare in the path, so the dev-only `minio-edge` proxy is not needed), and revisit self-hosting when usage justifies it.

## 3. What launches, and what stays switched off

| Area | At launch | Why |
|---|---|---|
| Accounts, feed, posts, reels, PostTube, search, notifications, chat | On | Core product |
| MStore (shop) with Razorpay | On only after Razorpay live-mode approval and the tax adviser's sign-off | Real money and GST invoices |
| Live streaming | Pilot list only (`LIVE_ACCESS_MODE=pilot`) | Needs a named moderation owner before opening |
| Offline copies | On | |
| Voice/video calls | Off (`CALLS_ENABLED=false`) until TURN and call push are set up | |
| Dating, Mopedu (rides), Feast (food), wallet, bill pay, reviewer programme | Off at the gateway | Not launch-ready or no production provider |
| Communities | Invite-only pilot (empty allowlists = nobody) | Needs a moderation owner |
| Creator payouts | Off (`MONETIZATION_PAYOUTS_ENABLED=false`) | Tax and provider work outstanding |
| Sign-up with Google/Apple | Off until production OAuth apps exist | |

Switches that must be set explicitly in production: `PAYMENTS_ALLOW_STUB` unset; `OTP_BYPASS_CODE` unset (and a guard added); `COURIER_PROVIDER=shiprocket` only when the Shiprocket production account is ready, otherwise shop shipping stays manual; `COMMERCE_PLATFORM_COUPONS_ENABLED=false`; `MOPEDU_COUPONS_ENABLED=false`; access-token lifetime at the 15-minute default (dev uses a year).

## 4. What is needed from AWS and from you (the founder's list)

Do these first; several have waiting periods.

**A. Secure the new account (day 1)**
1. Turn on multi-factor sign-in for the root user; do not use root after this.
2. In IAM Identity Center create an administrator user for yourself (and one for deployments); sign in with that.
3. Billing: set a monthly budget with email alerts (suggest alerts at $500, $1,000, $1,500) and enable Cost Explorer.
4. Confirm the region: Asia Pacific (Mumbai), `ap-south-1`.

**B. Requests that take time (day 1, then wait)**
5. Service Quotas → EC2 → "Running On-Demand Standard instances": new accounts are often limited to a few vCPUs. Request 64 vCPUs.
6. Amazon SES → request production access (out of the "sandbox"): until approved, mail can only go to addresses you verify by hand. Usually about one working day.
7. If CloudFront asks for account verification when first used, open the support case it points to.

**C. Domain and DNS (cleestudio.com stays at Cloudflare)**
8. Decide the production host names. Suggested: `api.cleestudio.com`, `ws.cleestudio.com`, `app.cleestudio.com` (or the bare domain) for the web, `admin.cleestudio.com`, `media.cleestudio.com`.
9. You will be given DNS records to add in Cloudflare: certificate validation, SES (DKIM, SPF, DMARC), and the final host records. Dev currently uses `cleestudio.com` and `api-dev.` through a tunnel; cutover is the last step.

**D. Access for the deployment**
10. Install and sign in to the AWS CLI on the deployment machine with the Identity Center deployment user (`aws configure sso`). Never paste access keys into chat or commit them.
11. GitHub: after Terraform creates the CI role, add its ARN as repository secret `AWS_CI_ROLE_ARN` (and the Terraform role ARN, state bucket and lock table names).

**E. Production accounts with other companies**
12. Razorpay: live-mode activation (business KYC), live key pair, webhook to `https://api.cleestudio.com/v1/payments/webhook`.
13. LiveKit Cloud: a production project, webhook to `https://api.cleestudio.com/v1/livestream/webhooks/livekit`.
14. Firebase: production `google-services.json` and the server key file for push notifications.
15. Shiprocket production account (only if courier booking is on at launch).
16. Google Play: production app listing, signing key, and (later) the Play Billing policy check noted for Dating.

**F. People and paperwork (not AWS, but they gate the launch)**
17. A named moderation owner (reports, live, communities).
18. Tax adviser sign-off on GST invoices and coupons; grievance officer details.
19. The superadmin account for production and an authenticator app on it.

## 5. Work before the first deployment (repo and infrastructure)

Done by parallel agents, each verified and committed; nothing here touches the AWS account.

| # | Workstream | Contents |
|---|---|---|
| W1 | Terraform, lean profile | A `prod` variable set for the lean sizes; MSK Provisioned with SCRAM and the full topic list (including the 8 missing topics); single NAT; smaller cache, search, Scylla; S3 buckets for commerce invoices, live recordings and food files; SES domain, DKIM, configuration set and bounce handling; CloudFront signing key and custom media domain; media KMS key policy for CloudFront; public certificates for the five hosts; AWS Backup, CloudTrail, GuardDuty, budgets; a Terraform apply role |
| W2 | Permissions | Service roles gain exactly what the code calls: SES (identity-auth), Rekognition (media, media-worker), KMS (commerce PII module wired in), S3 (media, commerce, live recordings) |
| W3 | Secrets | One generated secret per service in Secrets Manager (fresh production keys, never the dev ones): JWT keys, internal key, service-token key pairs, PII keys and salts, moderation HMAC keys, webhook secrets. A seeding script that prompts for third-party values (Razorpay, LiveKit, Firebase) without echoing them. Add the missing post-service and rider-service entries |
| W4 | Chart and ArgoCD | Pass the account id; allow this repository; repo credentials; real image tags; templates for the migration job, webhook entry and network policy; production set to manual sync; metrics-server; TLS flags for cache and Kafka; database, Kafka, Scylla and OpenSearch addresses on every service; fix the port mismatches |
| W5 | Build pipeline | Correct image names for the identity and chat services; add the missing registries; switch off the Azure workflows; create `release/prod`; stop the rebuild-on-promote |
| W6 | Web | Production image and values for `postbook-ui` (the web app) and the admin console; retire or park the 11 unused zone definitions |
| W7 | Code changes | Commerce and food file storage through S3 with the service role; search-service OpenSearch login; Scylla keyspaces with replication 3; refuse `OTP_BYPASS_CODE` in production; gateway routes for the services that stay off; signed-in email verification (send and confirm) with SES, and the "Verify email" screens on web and Android |
| W8 | Live on AWS | Production LiveKit Cloud project wiring, recordings to S3, webhook; calls stay off |

## 6. Deployment order (after sections 4 and 5)

1. **State and foundation.** Terraform bootstrap (state bucket, lock table), then network, EKS, registries. Two passes are needed the first time (cluster first, then the in-cluster pieces).
2. **Data stores.** PostgreSQL (five databases created: `app`, `identity_db`, `chat_db`, `call_db`, `commerce_db`, with PostGIS, pg_trgm, pgcrypto), cache, Kafka with topics, OpenSearch, Scylla with keyspaces, buckets and CloudFront.
3. **Secrets.** Run the seeding script; External Secrets shows every service secret present.
4. **First slice — prove the path.** identity-auth, identity-user, identity-profile, api-gateway, user, graph, and the web. Check: register with a real email (SES), sign in, load the home page through `api.` and `app.` with valid certificates.
5. **Core social.** post, feed, media + worker, search (create indices, run the backfill), notification, chat. Check: upload a video and watch it through CloudFront; a push notification arrives; chat delivers.
6. **Money.** payments and commerce with Razorpay in test mode first; one full order; then live keys when approved.
7. **Live (pilot).** live-service-v2 with the production LiveKit project; one stream, one recording becoming a video.
8. **Admin.** admin-service and the console on `admin.`; superadmin enrolled with an authenticator.
9. **Hardening.** Backups verified by a restore, alarms routed to email, WAF on the public entry points, a load test of the first slice, a written rollback (ArgoCD history) drill.
10. **Cutover.** Point the production host names at AWS; the dev stack keeps `api-dev.` and its tunnel.

No dev data is migrated: production starts empty. Confirm this is intended.

## 7. Open decisions

1. Lean first stage (about $1,000–1,500 a month) or the full design (about $5,000–7,000)? Recommended: lean.
2. LiveKit Cloud for production, or self-host on AWS? Recommended: LiveKit Cloud.
3. Which products are on at launch (section 3)? In particular: is the shop taking real payments on day one?
4. One environment (production only) or production plus a small staging copy? A staging copy roughly adds 40–60% to the lean cost. Recommended: production only at first, with dev as the test bed.
5. The web address: `app.cleestudio.com` or the bare `cleestudio.com`?
6. Who runs Terraform and the first deployment: you, following numbered steps, or this session through an AWS CLI sign-in on this machine?

## 8. Risks worth knowing now

- The members-only and audience checks, payments and PII sealing were all tested on dev only. The first slice in step 4 exists to catch configuration surprises early.
- Costs grow with media: transcoding CPU, CloudFront data transfer and Rekognition checks are per-use. Budgets and alerts in step A matter.
- A single small database and single-zone pieces in the lean stage mean an AWS zone outage causes downtime. Acceptable for a pilot; revisit before a public launch.
- EKS, Aurora and chart versions pinned in the Terraform are months old; W1 checks each against what AWS currently supports.
