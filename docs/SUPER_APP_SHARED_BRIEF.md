# atPost India-First Social Super-App — Shared Product Brief

## Purpose

This is the canonical context document for parallel work between Codex and Claude. Both assistants should use it as the source of truth, state assumptions explicitly, and return decisions in the shared output format defined below.

## Product vision

atPost is an India-first social operating system:

- Instagram-style visual identity, Stories, Reels, discovery, and creators.
- X/Twitter-style public conversation, text posts, threads, reposts, and real-time topics.
- YouTube-style long video, channels, subscriptions, playlists, search, and durable creator monetization.
- WhatsApp/Telegram-style private chat, groups, voice calls, and video calls.
- WeChat-style wallet, official accounts, mini-apps, and everyday services.

The goal is not to reproduce every competitor screen. The product must create a stronger connected loop:

`Discover -> create/discuss -> message -> transact -> review -> share`

The main differentiator is **actionable social content**: content can lead directly to buying, booking, ordering, paying, joining, donating, learning, or launching a mini-app without leaving the atPost identity and trust system.

## Product principles

1. **One identity, many contexts.** Social, creator, buyer, seller, driver, restaurant, and dating identities share authentication but expose only context-appropriate data.
2. **Canonical content, optional distribution.** Content is stored once and can be referenced by feeds, communities, search, messages, and mini-apps.
3. **Creator choice and viewer choice.** Creators control distribution; viewers control feed composition, autoplay, language, privacy, and data usage.
4. **India-native by design.** Indian languages, transliteration, low-data mode, resumable upload, UPI-oriented payments, local discovery, and DPDP-conscious consent are core features.
5. **Trust before growth hacks.** Identity-header integrity, moderation, appeals, provenance, privacy, child safety, payment auditability, and anti-fraud controls are release requirements.
6. **Mini-apps use platform capabilities.** Identity, payments, notifications, maps, chat, reviews, analytics, and safety should be SDK/platform capabilities rather than being rebuilt per mini-app.
7. **MVP means complete loops, not many shallow screens.** Every launched module needs a reliable acquisition, transaction/interaction, support, safety, and retention loop.

## Confirmed long-video decision

Long video is a separate YouTube-like product surface called **PostTube**, connected to the main social application only according to creator and viewer choices.

### Canonical ownership

- One canonical long-video record owns playback, watch time, monetization, copyright state, chapters, captions, and analytics.
- Social feeds, communities, Reels, search, and messages store distribution references, not copied videos.
- Removing a feed reference never deletes the PostTube video.

### Creator publication choices

- PostTube only.
- PostTube plus main-feed preview.
- PostTube plus selected communities.
- PostTube plus an automatically or manually created Reel teaser.
- Subscriber notification on/off.
- Public, unlisted, private, scheduled, or subscriber-only visibility.
- Publish to PostTube now and promote socially later.

### Viewer choices

- Long-video frequency: hidden, reduced, balanced, or preferred.
- Short-only, long-only, or mixed discovery surfaces.
- Long-video autoplay and data-saver controls.
- Subscribe to PostTube without following all social posts.
- Follow socially without subscribing to long video.

### Proposed distribution policy

```json
{
  "posttube": true,
  "main_feed": true,
  "notify_subscribers": true,
  "create_reel_preview": false,
  "communities": [],
  "visibility": "public"
}
```

For the MVP, existing `media-service`, `post-service.video_metadata`, feed, playlists, series, and PostTube mobile surfaces can support this model. A dedicated video metadata service is a later scaling boundary, not an MVP prerequisite.

## Existing codebase capabilities

The repository already contains:

- Go API gateway with JWT verification, trusted identity headers, routing, rate limiting, tracing, and internal-service authentication.
- Identity auth/profile/directory services with sessions, OAuth, 2FA/passkeys, RBAC, role audit, and event propagation.
- Social user, graph, suggestion, search, post, feed, media, and analytics services.
- Chat message, WebSocket gateway, call, notification, live v1, and LiveKit-based live v2 services.
- Groups, communities, broadcast channels, Q&A, and memories/slambooks.
- Monetization, wallet, payments, commerce, food, dating, rider, and bill-pay services.
- Trust and safety, reviewer pipeline, AI jobs, feature flags, and admin services.
- Flutter application with Riverpod, Dio, go_router, secure storage, realtime streams, WebRTC, LiveKit, repositories, providers, and approximately forty product surfaces.
- Docker Compose development stack and AWS/Azure Kubernetes infrastructure.

Important caveats:

- The Next.js web application described by documentation is not present in this workspace.
- Some platform documentation is stale; source code is authoritative.
- Outbox coverage is inconsistent between services.
- Money values are not universally consistent between rupees and paise.
- Three similarly named modules exist: identity-user, identity-profile, and the social user-service.
- Communities exist in code but are currently disabled in Flutter navigation and consolidated into Groups.

## Feature-audit sequence

Work through modules in this order:

1. Core posting and creative tools.
2. Feed, discovery, search, and recommendations.
3. Profiles, identity, and social graph.
4. Reels, Stories, PostTube, and live video.
5. Messaging, groups, voice calls, and video calls.
6. Creator studio, analytics, and monetization.
7. Trust, safety, privacy, provenance, and child protection.
8. Communities, Q&A, live events, and local discovery.
9. Wallet and payment platform.
10. Commerce.
11. Food.
12. Dating.
13. Ride-hailing and bill payment.
14. Third-party mini-app SDK and marketplace.
15. Admin, analytics, experimentation, and operations.

## Phase-one product boundary

### Core platform

- Registration, recovery, sessions, privacy, circles, blocking, and reporting.
- Unified social composer for text, image, mixed carousel, voice, poll, Story, Reel, and links.
- Threads, replies, reposts, quote-posts, hashtags, mentions, drafts, and scheduling.
- Following and interest feeds, search, trends, notifications, and private messaging.
- Reliable media upload, processing, captions, accessibility, and low-data delivery.

### Video and creators

- Separate PostTube surface with optional social synchronization.
- Reels, Stories, long video, channels, subscriptions, playlists, watch history, and basic live streaming.
- Creator analytics, tips, memberships/subscriptions, product tagging, and transparent earnings.
- Human/AI moderation and appeals.

### Initial mini-app pilots

- Commerce: discovery to purchase to fulfillment/return/support.
- Food: restaurant discovery to order to tracking/refund/support.
- Dating: onboarding to discovery to match/chat to safety/reporting.

Rider and bill-pay can remain controlled pilots until payment, dispute, support, fraud, and operational readiness are proven.

## Creative concepts to evaluate

### Action Post / Post Canvas

A block-based social object combining text, media, voice, polls, products, maps, events, donations, bookings, and mini-app actions.

### Language Twins

Publish once and produce synchronized caption, translation, transliteration, text-to-speech, and optional voice-preserving dubbing variants across Indian languages.

### Co-create and split earnings

Multiple creators own content and automatically split advertising, tips, affiliate income, subscriptions, and product revenue.

### Forkable content with provenance

Creators allow extensions, remixes, duets, or translations while preserving the origin chain, attribution, permissions, and optional revenue sharing.

### Local Pulse

Privacy-conscious local discovery for creators, public conversations, events, food, shops, services, and community alerts.

### Trust Passport

A consent-controlled verification and reputation layer reusable across commerce, dating, rider, wallet, and creator monetization without exposing raw KYC data between modules.

## Parallel ownership

### Claude owns product development and implementation

- Turn an approved module objective into a detailed product/technical plan.
- Design the user journey, screens, interaction states, and retention loop.
- Audit the relevant repository code before making changes.
- Implement backend, Flutter, schemas, APIs, events, migrations, and feature flags.
- Add or update unit, integration, and UI tests.
- Preserve backward compatibility and existing user work.
- Document assumptions, architectural decisions, rollout, and rollback.
- Return a precise change summary with files changed, tests run, and known limitations.

Claude is the primary builder. It should not stop at mockups or generic recommendations: its deliverable is a coherent, tested implementation of the approved scope.

### Codex owns product strategy, independent review, and release quality

- Define the product thesis, module goals, differentiation, and MVP boundary.
- Research competitive patterns and India-specific opportunities.
- Review personas, journeys, information architecture, and feature prioritization.
- Inspect Claude's code changes independently against the actual repository.
- Review service boundaries, canonical data ownership, APIs, events, and migrations.
- Find security, privacy, abuse, reliability, performance, and concurrency risks.
- Verify Flutter state, navigation, offline, loading, error, and recovery behavior.
- Run or inspect tests and identify missing regression coverage.
- Detect duplicated capabilities, stale documentation, and cross-module conflicts.
- Produce actionable review findings and give the final readiness recommendation.

Codex is the independent reviewer and product-quality gate. Codex may implement narrowly scoped fixes when explicitly requested, but the normal loop sends review findings back to Claude for correction.

### Shared responsibility

- Final scope and acceptance criteria.
- Cross-module consistency.
- Safety and privacy review.
- India-specific language and accessibility design.
- Monetization fairness and user trust.
- Resolving product desirability versus engineering cost.

## Required output for every module

Both assistants should return work using this structure:

1. **Objective and primary user loop**
2. **Personas and jobs-to-be-done**
3. **Existing capabilities**
4. **Missing essentials**
5. **Differentiating/creative features**
6. **MVP, next, and later scope**
7. **User journeys and important UI states**
8. **Data ownership and service dependencies**
9. **API/event/schema impact**
10. **Safety, privacy, abuse, and compliance risks**
11. **Failure, retry, offline, and recovery behavior**
12. **Metrics and acceptance criteria**
13. **Open decisions and explicit assumptions**

Every requirement should be tagged:

- `P0`: required for a safe, coherent launch.
- `P1`: important for retention or creator value shortly after launch.
- `P2`: differentiated enhancement.
- `EXPERIMENT`: validate before committing to permanent architecture.

## Prompt to give Claude

```text
You are the primary product-development and implementation engineer for the
atPost India-first social super-app. Read SUPER_APP_SHARED_BRIEF.md completely
and treat it as canonical context. For the assigned module, inspect the
existing repository first, write a concrete implementation plan, and then
implement the approved P0/P1 scope end to end across backend, Flutter, schemas,
events, migrations, flags, and tests as required. Preserve backward
compatibility and unrelated user changes. Cover loading, empty, error,
offline, privacy, moderation, retry, rollback, and recovery behavior. Return
the files changed, tests run, remaining risks, and explicit assumptions. Do
not merge PostTube long video into the main feed: keep one canonical video
with optional distribution references controlled by creator and viewer
choices. Codex will independently review the product behavior, architecture,
security, regressions, and release readiness.
```

## Synchronization workflow

1. Codex produces the module strategy, scope boundary, risks, and acceptance criteria.
2. The user shares that brief with Claude.
3. Claude audits the code and returns a technical plan plus an `exists`, `partial`, `missing`, or `conflicting` gap map.
4. After scope approval, Claude implements the P0/P1 slice and runs relevant tests.
5. The user shares Claude's diff/change summary with Codex or makes the workspace changes available.
6. Codex independently reviews product behavior, code, architecture, security, migrations, tests, and rollout safety.
7. The user sends Codex's actionable findings to Claude for fixes.
8. Codex re-reviews until no blocking findings remain and gives a readiness recommendation.
9. Production metrics determine whether experiments graduate, change, or are removed.

## Decision discipline

- Do not add a feature without identifying its primary user loop and success metric.
- Do not create duplicate canonical records merely to show content in another surface.
- Do not launch a money or safety workflow without reconciliation, audit, support, and recovery paths.
- Do not let mini-apps bypass central identity, consent, payment, notification, or trust controls.
- Prefer a smaller complete experience over a larger collection of disconnected screens.
