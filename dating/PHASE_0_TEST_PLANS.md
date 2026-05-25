# Phase 0 P0 — Test Plans for Deferred Items

**Date:** 2026-05-25
**Companion to:** `PRODUCTION_GAP_ANALYSIS.md`

Phase 0 instructions from the user: *"Do not add creative features until
all P0 safety, chat, reporting/admin, and scaling blockers have a
passing implementation or test plan."*

The items below are P0 in the gap analysis but not yet shipped as code
in this session — each is feature-sized and needs its own engineering
slice. This document is the gate: every item here must convert from a
test plan into a passing implementation (or be explicitly waived) before
Phase 0 closes and Phase 1 begins.

Items implemented in this session are tracked in commits:

- `bd7b883` — P0-1 (mobile) + P0-2 + P0-5 + P0-6 + archive
  Architecture/services/message-service
- `473bba5` — P0-1 (web)
- `ed673c3` — P0-3 dating_match chat type + send-path gate
- *(saga commit)* — P0-9 saga retry + close-on-unmatch consumer

---

## P0-4 — Real-time chat in web + mobile

**Status:** test plan. Foundation (M1 conversation-presence ZSET +
typing pipeline) shipped earlier this session; full WS rebase is the
remaining work.

### What's already in place

- `chat-service/shared/presence/` — Redis ZSET store for
  per-conversation presence + typing.
- `ws-gateway` accepts `conversation.enter / conversation.heartbeat /
  conversation.leave / typing.start` client messages.
- `message-service` exposes `GET /v1/conversations/:id/presence` for
  reading the active-viewer + typing set.
- Mobile + web chat surfaces (DmChat, ChatWindow, GroupPanel,
  Flutter `chat_detail_screen.dart`) already call the presence hook.

### Gaps that still ship REST-only chat

- No live `message.new` event subscription on web; Postmatch chat polls
  every 5s.
- Mobile Pulse chat appends locally after send but doesn't subscribe to
  incoming messages over WS.
- Delivery / read receipts are REST-driven, not pushed.
- Moderation hold / unhold isn't pushed live; the UI relies on poll +
  invalidate.
- No offline message queue with idempotent send retry on either
  platform.

### Acceptance tests (must pass before P0-4 is closed)

#### A. Two-device live delivery
- **Setup:** matched pair A↔B, both with WS connected.
- **Action:** A sends "ping".
- **Assertion:** B's chat screen renders "ping" within 1.5s without
  refresh, no REST poll in between.

#### B. Reconnect dedup
- **Setup:** A sends N=10 messages while B's WS is down.
- **Action:** B reconnects.
- **Assertion:** B sees exactly 10 messages, in order, with no
  duplicates. Idempotency keys on each message ensure replay safety.

#### C. Block closes chat live
- **Setup:** A↔B matched, both in chat screen, WS connected.
- **Action:** B blocks A.
- **Assertion (within 2s, no refresh):**
  - A's send button disables.
  - B's chat header changes to "Blocked".
  - A's next send returns 403 on the REST fallback.

#### D. Moderation hold live
- **Setup:** A↔B matched, scam-phrase detector enabled.
- **Action:** A sends "send me $50 on UPI 9876543210@upi".
- **Assertion (within 2s):**
  - A sees the message marked `held` (greyed, with "Pending review" tag).
  - B does NOT see the message in their chat.
  - Moderator approve action flips A's state to `sent` and pushes the
    message to B live.

#### E. Offline send queue (mobile)
- **Setup:** Mobile Pulse chat open, network disabled.
- **Action:** Type + send 3 messages.
- **Assertion (when network restores):**
  - All 3 messages send in order, dedup'd by idempotency key (no
    duplicates from server-side retry).
  - UI states walk: `queued` → `sending` → `sent` → `delivered`.

### Status owner / next slice

Owner: chat-service + mobile + web. Effort: ~5 working days.
Pre-req: M1 presence is done. WS subscription wiring + offline queue
+ moderation push are the three sub-slices.

---

## P0-7 — Fake account / scam defense

**Status:** test plan. Risk-scoring infrastructure does not exist;
implementation is a Phase 2 build.

### Required schema (Phase 2 deliverable)

```sql
CREATE TABLE IF NOT EXISTS dating_account_risk (
    user_id          UUID PRIMARY KEY,
    risk_score       INT  NOT NULL CHECK (risk_score BETWEEN 0 AND 100),
    risk_level       TEXT NOT NULL CHECK (risk_level IN
                       ('allow','reduce_reach','require_recheck',
                        'hide_from_discovery','chat_hold','admin_review',
                        'suspend')),
    signals          JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_evaluated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    enforcement_state TEXT NOT NULL DEFAULT 'allow'
);
CREATE INDEX idx_dating_account_risk_level ON dating_account_risk(risk_level);
CREATE INDEX idx_dating_account_risk_age ON dating_account_risk(last_evaluated_at);
```

### Signal inputs (each scored 0-100, combined via weighted sum)

| Signal | Source | Weight |
|---|---|---|
| Verification tier | dating_verifications | 25 |
| Profile completeness | profile fields populated | 10 |
| Photo approval state | dating_photos.moderation_status | 15 |
| Device reuse | new dating_device_fingerprints table | 15 |
| IP/ASN velocity | request-log aggregation | 10 |
| Report count + quality | dating_reports | 15 |
| Block rate | dating_blocks reported-as-blocker | 5 |
| Spark velocity | dating_sparks created/hour | 5 |

### Enforcement levels + behaviours

- **allow** — default; full discovery + chat.
- **reduce_reach** — discovery rank decay -50%; sparks still allowed.
- **require_recheck** — must re-do selfie verification; blocked from
  sparking until cleared.
- **hide_from_discovery** — does not appear in others' decks; can still
  receive sparks via existing matches.
- **chat_hold** — sends queue in moderation; no auto-delivery.
- **admin_review** — flagged in `/admin/dating/risk` queue; restricted
  from new matches.
- **suspend** — full dating-service ban; sparks/matches/chat all
  rejected.

### Acceptance tests

#### A. Mass-spark throttle
- **Setup:** newly created account, no verification, no approved photo.
- **Action:** attempt 20 sparks in 10 minutes.
- **Assertion:** spark rate-limit kicks in by spark #6; risk level
  flips to `reduce_reach`; subsequent sparks return 429 with retry-after.

#### B. Duplicate-image account
- **Setup:** two accounts upload the same primary photo
  (perceptual-hash match within 5%).
- **Assertion:** both accounts land in `admin_review` queue; neither
  appears in others' decks until an admin clears one.

#### C. Burst account creation from one IP
- **Setup:** 50 new accounts created from a single IP/24 in 1 hour.
- **Assertion:** all post-#10 land in `require_recheck` automatically;
  admin alert fires.

#### D. Scam-phrase chat detection
- **Setup:** matched pair, A sends "send $200 to BTC address …" or
  "let's move to WhatsApp +91…".
- **Assertion:** message goes to `chat_hold` state; admin queue gets
  the row with the matched phrase highlighted.

#### E. Verified-only filter
- **Setup:** viewer with `verified_only=true` preference.
- **Assertion:** discovery returns only candidates whose trust_tier >=
  selfie_verified.

### Status owner / next slice

Owner: trust-safety-service + dating-service. Effort: ~2 weeks.
Depends on: device fingerprinting infrastructure
(`dating_device_fingerprints` doesn't exist yet); image perceptual-hash
service (would extend existing media-service scanner).

---

## P0-8 — `/admin/dating` console

**Status:** test plan. Existing admin UI handles FiGo/Mopedu;
no dating-specific console yet.

### Required surfaces (Phase 2 deliverable)

#### 1. Safety dashboard `/admin/dating`
Top-line metrics:
- Open reports count by category
- Panic events in last 24h (red-bordered card if any unacknowledged)
- Failed safe-meet check-ins
- Blocked-user spike alert (Z-score > 3 vs baseline)
- Moderation holds awaiting review
- Fake-account queue length

#### 2. Reports queue `/admin/dating/reports`
- Filter: category, status, age, reporter trust tier
- Sort: priority (SLA-weighted), age, repeat-offender
- Row click → detail panel with:
  - Reported profile snapshot at report time
  - Reporter profile (one tap to anonymous mode for review)
  - Match/chat context (last 20 messages, redacted PII opt-out)
  - Prior reports against the reported user
  - Trust tier + device graph
  - Action toolbar (warn / restrict / suspend / preserve / escalate)
- Every action logged to `dating_admin_audit` table:
  ```sql
  CREATE TABLE dating_admin_audit (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      actor_admin_id UUID NOT NULL,
      action TEXT NOT NULL,
      target_user_id UUID,
      target_resource TEXT,
      reason TEXT NOT NULL,
      policy_code TEXT,
      internal_notes TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  ```

#### 3. Panic queue `/admin/dating/panic`
- One row per `dating_safety_events` of type 'panic'
- Real-time stream (SSE) for new events
- Acknowledgement workflow: admin claims event → 15min SLA timer →
  resolution code (call placed / dispatched / resolved-no-action /
  false-positive)
- Trusted-contact-notification delivery receipts panel

#### 4. Photo moderation `/admin/dating/photos`
- Grid view of `dating_photos WHERE moderation_status='pending'` ordered
  by oldest-first
- Bulk approve / reject with reason codes
- Reject reason notification fan-out to the photo owner

#### 5. Fake-account risk queue `/admin/dating/risk`
- Rows from `dating_account_risk WHERE risk_level IN
  ('admin_review','require_recheck','chat_hold','suspend')`
- Action: clear / extend / suspend / re-verify

#### 6. Appeals `/admin/dating/appeals`
- Surfaces user appeal submissions
- SLA timer (72h default)
- Reviewer assignment + outcome audit log

### Acceptance tests

#### A. Report-to-action flow
- User A reports User B for "scam/fraud".
- **Assertion:** within 2s the row appears in `/admin/dating/reports`
  with the right category badge, prior-report count, and last-N
  messages. Admin clicks "Suspend" → User B's profile flips to
  `suspended` server-side; `dating_admin_audit` row inserted with
  actor + reason.

#### B. Panic event SLA timer
- User panics from mobile (POST /v1/dating/safety/panic).
- **Assertion:**
  - Within 5s the event appears at top of `/admin/dating/panic` with a
    15min countdown.
  - On admin acknowledge, countdown stops and resolution form opens.
  - Unacknowledged at +15min: critical alert fires to on-call.

#### C. Photo moderation queue empty before discovery
- User uploads a photo.
- **Assertion:** photo state is `pending`, owner sees pending badge,
  no other user sees it in discovery. Admin approves → state flips,
  photo appears in discovery within the next deck refresh.

#### D. Action audit immutability
- Admin restricts User C.
- Try to UPDATE or DELETE the `dating_admin_audit` row.
- **Assertion:** trigger refuses. Append-only.

#### E. Appeal flow
- User C appeals the restriction.
- **Assertion:** row appears in `/admin/dating/appeals`; original
  restriction action is linked; reviewer outcome is logged separately.

### Status owner / next slice

Owner: web frontend (`postbook-ui`) + admin-service + trust-safety-
service. Effort: ~2 weeks. Pre-req: `dating_admin_audit` table +
SSE endpoint on dating-service for panic stream.

---

## P0-10 — Discovery scale

**Status:** test plan. `ORDER BY random()` + Go-side haversine in
current FetchCandidates won't survive a million users per city.

### Phased plan

#### Phase A (Phase 1, ~1 week)
- Add `dating_profiles.location_geohash` (already present per
  `dating_profiles` schema review) + a btree index on the prefix.
- Replace `ORDER BY random()` with `ORDER BY (random() * decay)` where
  decay weights recent activity + trust tier (still random-ish but
  ranked).
- Pre-filter candidates by geohash prefix match (4-char = ~20km cell)
  before pulling into Go.
- p95 target: 400ms at 100k profiles per city.

#### Phase B (Phase 2, ~2 weeks)
- Per-city/cohort candidate pools rebuilt every 5 minutes by an async
  worker.
- Online ranking computed from a `dating_candidate_features` materialised
  view (refreshed every 15 minutes) carrying:
  - profile_completeness_score
  - trust_tier
  - last_active_at decay
  - language_overlap
  - intent_match
- Service-layer combines hard-filter from the candidate pool with the
  feature scores; final diversity pass + 20-card return.

#### Phase C (Phase 3, ~3 weeks)
- PostGIS earthdistance index instead of geohash prefix.
- Redis sorted-set "candidate inbox" per viewer, populated by background
  generator + invalidated on:
  - candidate paused/deleted
  - candidate's primary photo rejected
  - viewer blocked candidate
  - viewer's preferences changed
  - candidate suspended
- Discovery API returns from Redis with O(log N) latency; only the
  background worker runs the SQL.

### Acceptance tests

#### A. p95 latency at scale
- **Setup:** 100k profiles per metro, simulated traffic at 500 RPS.
- **Assertion:** `GET /v1/dating/pulse/today` p95 < 200ms.

#### B. No-random in prod
- **grep** check on the production binary's SQL:
  `grep -i 'order by random' should return 0 matches`.

#### C. Stale-state never appears
- Block candidate B from viewer A → A's next deck refresh must not
  contain B (verify via the Redis-set invalidation event).
- Pause candidate B → same.
- Mark B's primary photo as rejected → same.
- Suspend B → same.

#### D. Cohort isolation
- Viewer A in Bangalore.
- **Assertion:** no candidate from outside the configured radius (e.g.
  100km default) appears in A's deck, even when the city's pool is
  shallow.

#### E. Deck cache invalidation timing
- Candidate B updates their preferences → B's outbound deck stays
  fresh (no caching of own-side data) AND inbound deck refreshes
  within 1 minute for everyone whose preferences B matched.

### Status owner / next slice

Owner: dating-service + platform. Effort: 3 phases totalling ~6
weeks. Pre-req for Phase B: feature-store / materialised-view
infrastructure decision.

---

## Closing this gate

Phase 0 closes when:

1. P0-1, P0-2, P0-3, P0-5, P0-6, P0-9 — **implementation merged + tests
   passing** ✅ (this session)
2. P0-4, P0-7, P0-8, P0-10 — **test plans written above + tracked as
   Phase 1/2/3 deliverables** ✅ (this document)
3. The Phase 1 work begins only after the above gate is acknowledged.

Phase 1 candidates (per the gap analysis §17) — onboarding, photo
moderation queue, selfie verification finish-line, discovery deck
reliability, spark/match reliability, real-time match chat (P0-4
acceptance tests above become the entry criteria), block/report from
every surface, notifications for core dating events.
