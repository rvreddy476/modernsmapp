# VChat / atPost — Platform Specification & Feature Catalog

> **Purpose of this document.** A granular, onboarding-grade map of everything the
> platform is made of — every backend service, its tables and endpoints, the mobile
> and web surfaces, the cross-cutting infrastructure, and a roadmap of what to build
> or improve next. Written for (a) new engineers ramping up, (b) product/feature
> planning, and (c) review.
>
> **Confidence note (read this).** The **Reviewer / video-review pipeline** section is
> first-hand and runtime-verified (built recently). Everything else is an accurate
> **inventory** (service list, real table names, real route prefixes extracted from the
> code) with **behaviour inferred from schema + routes** — treat those descriptions as
> a strong starting map, but confirm specifics in code before betting on them.
>
> _Generated 2026-06-21._
>
> **Companion docs:** `ARCHITECTURE.md` (service interaction, security model, chat/video
> resilience) · `modules/README.md` (per-module DDL + routes, extracted from source).

---

## 1. The product in one paragraph

VChat (a.k.a. atPost) is a **super-app**: a social network (feed, profiles, social
graph, groups, communities, messaging) wrapped around a **creator video platform**
(PostTube long-form + Reels/Flicks short-form), with **creator monetization**
(wallet, ledger, payouts, tips, subscriptions, creator fund, ads/RPM), a **trust &
safety** layer (reports, appeals, strikes, human review), and a suite of **mini-apps**
(commerce/marketplace, food delivery "FiGo", ride-hailing "Mopedu", bill-pay, dating
"Pulse", Q&A, live streaming, memories/slambooks). Indian-market oriented (UPI,
DigiLocker/PAN KYC, DPDP, TDS/GST in the money layer).

## 2. Tech stack & architecture

| Layer | Technology |
|---|---|
| Backend | **Go microservices** (Gin HTTP, pgx Postgres driver), one DB schema per domain |
| API edge | **api-gateway** (Go) — JWT verify (HS256, kid rotation), CORS, rate-limit, injects trusted `X-User-Id`/`X-Scopes`/`X-Internal-Service-Key` headers, prefix-routes `/v1/*` to services |
| Datastores | **PostgreSQL** (`app` DB + `identity_db`), **ScyllaDB** (high-write counters / watch sessions / chat), **Redis** (counters, cache, rate-limits, sorted-set rankings) |
| Eventing | **Redpanda/Kafka** (`social.events.v1`, `monetization.events`, etc.), outbox pattern in several services |
| Media | **media-service** + **media-worker** (transcode) + **MinIO** (S3-compatible object store) + **mediamtx** (RTMP/live) |
| Observability | Jaeger (OTLP traces), Prometheus metrics, structured slog |
| Mobile | **Flutter** (Dart), Riverpod state, Dio/ApiClient, go_router |
| Web | **Next.js** (App Router, React, Tailwind, react-query), proxies `/v1/*` → gateway |
| Orchestration | **docker-compose** (`atpost_stack`, 56 services incl. infra); `stack.sh` blessed runner |

**Cross-service conventions:** UUID PKs (`gen_random_uuid()`), snake_case, schema-per-domain
(`trust.*`, `wallet.*`, `food.*`, `reviewer.*`, …), idempotent `ensureSchema` self-heal on boot
(guards against the recurring "live DB lags migrations" drift), `X-Internal-Service-Key`
for service-to-service auth, gateway blocks `/internal/` paths from non-admins.

**Counts:** 31 application services · 56 compose units (incl. infra) · ~40 mobile feature
modules · ~40 web route groups · several hundred Postgres tables.

---

## 3. Backend services — catalog (purpose · key tables · routes)

### 3.1 Identity, social graph & users
- **identity-auth** (identity_db) — registration, login, JWT issuance (access+refresh, kid
  rotation), sessions, step-up/anomaly. Routes `/v1/auth`. *Tokens currently carry no
  `scopes` claim (see Gaps).*
- **user-service** (22 tables) — profiles, channels, business pages, links, endorsements,
  reputation, digital-wellbeing/screen-time, referrals. Tables: `users, user_about,
  user_links, user_settings, channels, channel_members, channel_subscriptions,
  channel_milestones, business_pages, business_reviews, page_roles, page_followers,
  page_verification_documents, portfolio_items, profile_pins, profile_qr_codes,
  endorsements, user_reputation, digital_wellbeing, screen_time_log, referrals`. Routes
  `/v1/users /v1/channels /v1/pages /v1/onboarding /v1/links`.
- **graph-service** (13 tables) — follows, friends, connections, circles, close-friends,
  blocks, mutes, favorites, relationship labels; one-round-trip relationship check.
  Tables: `follows, friends, friend_requests, connections, connection_requests,
  circles, circle_members, close_friends, blocks, graph.mutes, favorites,
  relationship_labels, counts`. Routes `/v1/graph /v1/permissions`. **(Reused by the
  Reviewer anti-collusion check.)**
- **suggestion-service** (5) — "people/▼ you may know" candidates, impressions, cooldowns,
  dismiss patterns. `/v1/suggestions`.
- **search-service** (5) — multi-entity ranked search + discovery, history, saved searches,
  click analytics. `/v1/search /v1/discover`.

### 3.2 Posts, video & feed (the content core)
- **post-service** (46 tables) — the heart: posts, comments, reactions, polls, stories,
  reels/flicks, long-form video metadata, playlists, series, crossposts, hashtags, saved
  items, mentions, engagement counts, **review_status gate**, outbox. Key tables: `posts,
  post_media, post_engagement_counts, post_mentions, post_outbox_events, comments,
  reactions, tunes, polls/poll_options/poll_votes, stories/story_interactive,
  reel_drafts, reel_hashtags, video_metadata, video_series/_episodes, flick_series/_items,
  playlists/playlist_items, watch_progress, media_chapters, video_cards,
  video_end_screens, post_product_tags, saved_items, topics, link_previews,
  app_feedback, moderation_reviews`. Routes `/v1/posts /v1/comments /v1/reels /v1/videos
  /v1/stories /v1/feedback /v1/playlists /v1/creators /v1/crossposts /v1/events`.
- **feed-service** (8) — ranked timeline: candidate ranking (author affinity, velocity,
  CQS), hides/mutes, impressions, see-less/see-more signals. Tables: `user_interactions,
  user_preferences, viewer_media_prefs, feed_hides, feed_mutes, post_impressions,
  celeb_authors, event_dedup`. Routes `/v1/feed` (incl. `/v1/feed/signal`).
- **media-service** (14) + **media-worker** — uploads (init→PUT(MinIO)→confirm), transcode
  jobs, renditions, variants, subtitles, audio library, clips, resumable uploads. Tables:
  `media_assets, media_renditions, media_variants, media_subtitles, media_clips,
  transcoding_jobs, resumable_uploads/_parts, audio_tracks, audio_library,
  post_audio_refs, owner_media_slots/_resolved`. Routes `/v1/media /v1/audio /v1/subtitles
  /v1/clips`. Serves via `/v1/media/:id/serve` (307→MinIO).
- **analytics-service** (12) — per-content rollups (hourly/daily, partitioned), raw events,
  **Content Quality Score (CQS)**, plus a gamification block (loyalty points, missions,
  badges, streaks). Tables: `analytics.content_hourly_agg(+partitions),
  content_daily_summary, events_raw(+partitions), missions, mission_progress,
  loyalty_points, point_transactions, user_badges, user_streaks`. Routes `/v1/analytics`.

### 3.3 Messaging, notifications, live
- **chat/message-service** (8, chat schema) — conversations, members, messages, reactions,
  reads, E2E key bundles. `/v1/chat`.
- **notification-service** (5) — devices, preferences, bundles/digests, dedup, realtime.
  `/v1/notifications /v1/unread /v1/read-marker`.
- **live-service** (14) — live + audio rooms: streams, scheduled streams, viewer sessions,
  DVR, gifts, guests, polls, chat, word filters. `/v1/live /v1/audio-rooms`.
- **live-service-v2** (5) — LiveKit browser-native broadcast: streams, viewer events, chat
  + moderation. `/v1/livestream`.

### 3.4 Communities, groups, Q&A, memories
- **community-service** (15) — communities, spaces, members, posts, events, wiki, modlog,
  bans, reports. `/v1/communities`.
- **group-service** (23) — groups, channels, posts (+approval queue), events, invites,
  rules, resources, wiki, word-blocklist, member stats. `/v1/groups`.
- **channel-service** (11) — broadcast channels + updates (sparks/stashes/echoes/views),
  comments, polls, event RSVPs. `/v1/broadcast-channels`.
- **qa-service** (36) — full Q&A/Quora-style: questions, answers, votes, comments, drafts,
  media, translations, topics, reputation, badges, communities, moderation. `/v1/qa`.
- **memories-service** (17) — "memories", on-this-day, collections, and **slambooks**
  (templates, cards, invites, collaborators, responses, moderation). `/v1/memories`.

### 3.5 Money & monetization
- **monetization-service** (29) — creator economy: double-entry `accounts` + immutable
  `ledger_entries` + `balance_snapshots`; `creator_ledger`/`transactions`; payouts
  (`payout_requests/_batches/_statements/methods`), subscriptions, tips, donations,
  fundraisers, affiliate links/conversions, creator fund + RPM rates, taxes
  (`gst_ledger, tds_ledger, creator_tax_profiles`), disputes, fraud reviews. `/v1/monetization`.
- **wallet-service** (6, wallet schema) — consumer wallet: `balances, transactions,
  kyc_records` (DigiLocker/PAN, **DPDP-safe** — Aadhaar never stored, PAN masked, tiers
  minimal/full/enhanced), recipients, partner-bank settlements, idempotency. `/v1/wallet`.
  **(Reused by Reviewer KYC gate.)**
- **payments-service** (6, payments schema) — payment intents, holds, refunds, webhooks,
  audit, outbox. `/v1/payments`.

### 3.6 Trust, safety & moderation
- **trust-safety-service** (9, trust schema) — reports (state machine + assignment),
  content appeals, user strikes, keyword filters, media labels, teen accounts, verification
  requests, IT-Rules grievances, periodic user trust-score reconciler. `/v1/reports
  /v1/appeals /v1/strikes /v1/grievances /v1/verification-requests /v1/keyword-filters
  /v1/media-labels /v1/teen-accounts`.
- **reviewer-service** (7, reviewer schema) — **human video review pipeline** — see §6 for
  the full granular spec. `/v1/reviewer`.
- **ai-service** (2) — AI jobs + AI moderation results (`ai_jobs, moderation_ai_results`);
  `/v1/ai/moderation/check` currently a **stub**. `/v1/ai`.
- **feature-flag-service** (3) — flags, experiments/conversions, audit. `/v1/flags /v1/admin/flags`.
- **admin-service** (7) — admin console backend: audit log, suspensions, data-export
  requests, mini-app registry, OAuth clients/tokens. `/v1/admin /v1/apps /v1/oauth`.

### 3.7 Mini-apps (the "super-app" suite)
- **commerce-service** (61 tables!) — full marketplace: products/variants/brands/categories,
  carts, orders, payments, refunds, shipments/tracking, inventory + reservations, sellers +
  onboarding + payout accounts, coupons, reviews, RFQs/quotes, organizations, COD
  remittances, support tickets, fraud scores, outbox. `/v1/commerce`.
- **food-service** (56) — "FiGo" food delivery: restaurants/menus/addons, carts, orders,
  delivery partners + assignments + batches + tracking, ratings, loyalty, coupons, refunds,
  settlements, support. `/v1/food`.
- **rider-service** (30) — "Mopedu" ride-hailing: partners + vehicles + documents + KYC
  (Aadhaar/DigiLocker), rides + offers + payments + status history, fare rules, zones,
  safety (trusted contacts, incidents, masked calls, share tokens), subscriptions,
  settlements. `/v1/rider`.
- **dating-service** (26) — "Pulse" dating: profiles, photos, preferences, prompts, matches,
  meets, sparks/stashes/tunes/passes, premium plans/subscriptions, verifications, vouches,
  **device-fingerprint + IP anti-sybil**, risk, moderation, consent log, data export. `/v1/dating`.
- **bill-pay-service** (9) — BBPS bill-pay: providers, categories, bills, accounts, payments,
  scheduled payments, reminders, mobile plans. `/v1/billpay`.

---

## 4. Mobile app (Flutter) — surfaces

**Feature modules (`lib/features/`):** auth, home, shell, profile, discover, explore, search,
reels, posttube, create, stories, comments, social, groups, communities, channels, pages,
chat, calls, notifications, live, memories, qa, pulse (dating), commerce/shop/seller/orders,
figo (food), mopedu (rides), billpay, wallet, monetization, hashtag(_feed), bookmarks,
mini_apps, settings, **reviewer**.

**Repositories (`lib/data/repositories/`):** ~45, incl. post, feed, feed_signal, feedback,
playlist, reviewer, captions, product_tags, profile_extras, monetization, tips, wallet,
groups/group_posts, communities/community_posts, qa, live/live_streams, memories, mopedu,
figo_rewards, b2b, commerce, shop, orders, billpay, pulse, search/search_extras, ai,
analytics, presence, studio, user, wellbeing, notification, mini_apps, broadcast_channels, calls.

**Cross-cutting:** Riverpod providers, ApiClient (Dio, envelope unwrap, auth interceptor),
go_router, persisted toggles (data-saver, autoplay), shared `video_more_sheet` (unified
Love/More/Report/Playlist/Quality across Reels+PostTube+feed).

## 5. Web app (Next.js) — surfaces

**Route groups (`src/app/`):** admin (incl. **/admin/review** super-admin console), auth/login/
register/session, profile/u, posttube, reels, create, discover, search, trending, hashtag,
communities, groups, channels, circle, messenger, notifications, monetization, commerce/
products/cart/checkout/orders/rfq/seller, figo, live, memories, qa, pages, organizations,
mini-apps, settings, saved, post, **reviewer + reviewer/console**, postmatch.

**Feature modules (`src/features/`):** posttube, reels, live, upload (UploadStudio), figo,
slambooks, settings, postboek. API via `src/lib/api.ts` (axios + Bearer interceptor),
`/v1/*` rewritten to `/api/proxy` → gateway.

---

## 6. Reviewer / human video-review pipeline — FULL SPEC (first-hand)

**What it is.** A human review layer for video (PostTube long + Flicks/Reels short): flagged
content is routed to a single blind reviewer who **Approves** (publishes) or **Escalates**
(with comments) to a **super-admin**, who **Rejects / Requests-edits / Approves**; on
"request edits" the creator gets the notes, edits, and **re-submits** (loops back to review).
Reviewers are KYC-gated and paid; their judgment is graded against engagement; integrity is
defended with audits/anomaly/ring detection. Built in 4 phases + the workflow rework.

**Service:** `reviewer-service` (Go, port 8120), schema `reviewer.*`.

**Tables (7):**
| Table | Purpose |
|---|---|
| `reviewers` | opt-in reviewers: status (probation/active/suspended), tier (probation/trusted/senior), `reviewer_accuracy` (EWMA 0..1), languages, region, **kyc_verified**, max_concurrent, is_online |
| `review_queue` | content awaiting a human (content_id, creator_id, content_type, languages, content_seconds, claimed) |
| `review_assignments` | one row per (content, reviewer) attempt; `one_active_review` partial-unique index scoped to `kind='primary'`; kind=primary/audit/shadow; decision=approve/escalate; watched_seconds (capped 1.2×); graded flag |
| `escalations` | reviewer→super-admin hand-off: reviewer_comments, status open/resolved, admin_decision (reject/request_edits/approve), admin_notes |
| `content_review_outcome` | engagement ground truth: engagement_pctile, finalized_at |
| `reviewer_ledger` | append-only pay accruals (paise): base/bonus/penalty/clawback |
| `reviewer_flags` | integrity hits: audit_mismatch/shadow_mismatch/anomaly_rubberstamp/anomaly_approval_rate/ring_suspect |

**Endpoints (`/v1/reviewer`):** `POST /opt-in`, `GET /me`, `GET /me/stats` (dashboard),
`POST /verify-kyc`, `POST /online`, `GET /queue`, `GET /assignments/next` (resumes in-flight;
optional `?content_id=` to target), `POST /assignments/:id/heartbeat`,
`POST /assignments/:id/decision` (approve|escalate+comments), `GET /content/:id/feedback`
(creator), `GET /admin/stats`, `GET /admin/escalations`, `POST /admin/escalations/:id/decision`,
`POST /internal/enqueue`. Plus post-service: `POST /v1/posts/internal/review-status`,
`POST /v1/posts/internal/visibility`, `POST /v1/posts/:id/resubmit`.

**Phases delivered:**
1. **Pipeline** — opt-in, matcher (pull-based: language + rotation-cap in SQL, **graph
   anti-collusion** via graph-service, atomic claim, blind review, watch heartbeat cap,
   expiry sweeper), capped base pay accrual.
2. **Grading** — cohort-normalized engagement percentile (`PERCENT_RANK` over
   analytics rollups) as the answer key → EWMA accuracy, tiers, correctness bonus.
3. **Integrity** — rate-sampled silent **audit** of approvals + **shadow** of rejects;
   mismatch → flag + clawback + accuracy ding + auto-suspend; periodic anomaly
   (rubber-stamp / approval-rate) + **ring detection** (reviewer↔creator clusters).
4. **Scale** — **ML pre-filter** (`prefilter.Classifier`, heuristic baseline gating the
   queue: auto-reject / auto-approve / needs-human) + **staged test-audience** visibility
   tier + promotion worker.

**KYC-gated onboarding** — `next` returns **403 KYC_REQUIRED** until `kyc_verified`;
verification reuses **wallet-service** (DigiLocker/PAN, tier full/enhanced); `verify-kyc`
syncs the flag. Mobile dashboard + web `/reviewer` show the verify gate.

**Auto-enqueue** — post-service routes flagged video to the queue; **`REVIEW_ALL_VIDEOS`**
flag (dev=on) routes *every* video (incl. `pending`/transcoding) to review.

**UI:** mobile (`/reviewer/dashboard`, `/reviewer` console, `/reviewer/feedback/:id` creator
loop) + web (`/reviewer` dashboard+queue, `/reviewer/console`, `/admin/review` super-admin
console with stats). PostTube web sidebar has a **Review** link.

**Config flags:** `REVIEWER_GRADING_*`, `REVIEWER_INTEGRITY_ENABLED/AUDIT_RATE/SHADOW_RATE/
SUSPEND_THRESHOLD`, `REVIEWER_PREFILTER_ENABLED/REJECT_AT/APPROVE_BELOW`, `REVIEWER_PROMOTE_*`,
`REVIEWER_CREDIT_ENABLED` (monetization settlement, **off** pending paise/rupee unit
verification), `REVIEW_ALL_VIDEOS`.

**Status / honest gaps (Reviewer):**
- Real **payout settlement** to the monetization ledger is **gated off** (pay accrues
  locally in `reviewer_ledger` until the unit convention is verified).
- "Don't recommend channel" maps to `muteUser` (feed signal is post-level only).
- Video **playback** required resolving short videos from the post's `media[]` (not
  `video_metadata`, which is long-form only) — fixed in the web console.
- Branches `feat/reviewer-flow` (both repos) are **local, not pushed**; some web/UI tweaks
  uncommitted.

---

## 7. Cross-cutting gaps & risks (whole platform)

1. **JWT carries no `scopes`** — admin/superadmin authorization currently relies on a
   client-sent `X-Scopes` header (the existing admin pages do this); the gateway does **not
   strip inbound identity headers**. *Fix: issue scopes in the JWT + strip inbound
   `X-User-Id/X-Scopes/X-Internal-Service-Key` at the gateway ingress.* (Highest-value
   security hardening.)
2. **Schema drift** — live DBs lag migrations; mitigated by idempotent `ensureSchema`, but
   not every service has it.
3. **Outbox coverage uneven** — some services publish to Kafka directly (no dual-write
   guarantee).
4. **Money units** — DECIMAL-rupees vs int64-paise inconsistency in monetization; verify
   before any new payout wiring.
5. **Media/transcode** — uploads land but transcode→playback can lag; no per-content
   "ready" signal surfaced uniformly.

## 8. Roadmap — what to build / improve next

**Reviewer (natural next steps):** real monetization-ledger settlement + reviewer payouts;
per-viewer N% staged-audience sampling in the feed (currently staged = served to organic
audience); replace the heuristic pre-filter with the real ai-service model; author-level
"don't recommend"; reviewer leaderboard + appeals for clawbacks; SLA dashboards; bulk/admin
queue tooling; reviewer notifications.

**Platform-wide quick wins:** (1) JWT scopes + gateway header-stripping (security); (2) a
unified "creator earnings" view across monetization + reviewer + creator-fund; (3) consistent
outbox across money/safety services; (4) a single `content_engagement_percentile` table in
analytics (reused by feed ranking + reviewer grading); (5) push notifications for
review/needs-changes/escalation events; (6) admin consoles for the services that only have
backend (food/rider/commerce moderation surfaces).

**Bigger bets:** ML pre-moderation across all media (extend ai-service); creator-grade
analytics studio (web); cross-mini-app loyalty (analytics already has loyalty tables);
recommendation quality (feed velocity + CQS + signals already exist — productize them).

---

## 9. How to run (dev)

```bash
# backend
cd modernsmapp/Architecture/docker && ./stack.sh up      # brings up atpost_stack, smoke-tests login
# web
cd postbook-ui && bun run dev                            # Next.js on :3000, proxies /v1 → gateway:8080
# mobile
cd modernsmapp/mobile/atpost_app && flutter run          # uses ./tool/fl.sh to suppress KGP build noise
```
Gateway `:8080`; reviewer-service `:8120`; MinIO `:9000`; Postgres `:5432`. Branches of the
recent reviewer work: **`feat/reviewer-flow`** in both `modernsmapp` and `postbook-ui`.
