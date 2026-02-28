# Postbook — My Circle System

> Suggestion Ranking, Request Flow & Real-Time Notification Architecture
> v1.1 — February 2026 | 15 Review Items Addressed

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Circle Suggestion Ranking](#2-circle-suggestion-ranking)
3. [Add to Circle Request Flow](#3-add-to-circle-request-flow)
4. [Real-Time Notification & Accept/Block Flow](#4-real-time-notification--acceptblock-flow)
5. [Anti-Abuse & Rate Limits](#5-anti-abuse--rate-limits)
6. [Event Catalog](#6-event-catalog)
7. [Database Schemas](#7-database-schemas)
8. [API Reference](#8-api-reference)
9. [v1.1 Revision Fixes](#9-v11-revision-fixes)
10. [Observability & SLOs](#10-observability--slos)

---

## 1. System Overview

My Circle is Postbook's social connection system. Unlike an asymmetric follow model, Circle is a **mutual, consent-based** relationship — both parties must agree. This builds trust and keeps the social graph high-quality.

### Design Principle

> User A sends a request → User B explicitly accepts or blocks. No silent adding. No asymmetric relationships.

### Terminology

| Term | Definition |
|------|-----------|
| **Circle** | The set of users mutually connected (equivalent to "friends" — both accepted) |
| **Circle Request** | A pending invitation from one user to another (unidirectional until accepted) |
| **Suggestion** | A ranked list of users the system recommends for Circle addition |
| **Mutual Connection** | A user in both User A's and User B's circles (key ranking signal) |
| **Affinity Score** | Computed score predicting how likely two users are to form a meaningful connection |
| **Block** | Permanent rejection — blocked user cannot send future requests or appear in suggestions |

### Service Ownership

| Service | Responsibility |
|---------|---------------|
| **Profile Service** | Circle graph storage (connections table), user profiles, block list |
| **Suggestion Service** | Ranking pipeline, affinity computation, candidate generation |
| **Notification Service** | Push notifications, in-app notification storage and delivery |
| **WebSocket Gateway** | Real-time delivery of circle request events to online users |
| **Feed Service** | Post-acceptance feed rebuilds (new circle member's posts enter inbox) |

---

## 2. Circle Suggestion Ranking

The suggestion system answers: given a user, which other users should we recommend they add to their circle?

### 2.1 Candidate Generation

Before ranking, we generate a candidate pool from 5 sources (executed in parallel, merged, deduped):

**Source 1: Friends-of-Friends (FoF)**
- 2-hop BFS on adjacency list (Redis or PostgreSQL)
- For each user C in viewer's circle → for each user F in C's circle → if F not in viewer's circle: add F
- Typically yields 50–500 candidates

**Source 2: Interaction-Based**
- Users the viewer has liked/commented/shared but are NOT in their circle
- Query: `engagement_log WHERE viewer_id = {id} GROUP BY target_author_id`
- Typically yields 10–50 candidates

**Source 3: Co-Group Members**
- Users who share groups/communities with viewer but are NOT in their circle
- Query: `group_members WHERE group_id IN (viewer's groups)`
- Typically yields 20–100 candidates

**Source 4: Profile Similarity**
- Jaccard index on interests + location proximity (same city = 0.8, same country = 0.3)
- Fallback source for users with small circles
- Typically yields 20–50 candidates

**Source 5: Registration Contacts (optional)**
- Phone/email contacts uploaded during onboarding, matched against user database
- Highest-intent signal (they already know each other)
- v1.1: SHA-256 hashed client-side, never stored in plaintext (see §9)

After merge + dedup + filtering (already in circle, blocked, pending requests, self): **100–700 candidates** pass to ranking.

### 2.2 Ranking Formula: Circle Affinity Score

```
affinity(viewer, candidate) =
    0.35 × mutual_connections_score
  + 0.25 × interaction_score
  + 0.15 × co_group_score
  + 0.10 × profile_similarity_score
  + 0.10 × recency_boost
  + 0.05 × contact_match_bonus
  - penalty_declined
```

### Signal Breakdown

| Signal | Weight | Range | Computation | Data Source |
|--------|--------|-------|-------------|-------------|
| `mutual_connections` | 0.35 | 0.0–1.0 | `min(mutual_count / 10, 1.0)` — 10+ mutuals = max score | 2-hop query on circle graph |
| `interaction_score` | 0.25 | 0.0–1.0 | v1.1: `min(log2(raw + 1) / log2(GLOBAL_P95 + 1), 1.0)` — log-scaled, capped at global p95 | engagement_log table |
| `co_group_score` | 0.15 | 0.0–1.0 | `min(shared_group_count / 5.0, 1.0)` — 5+ shared groups = max | group_members table |
| `profile_similarity` | 0.10 | 0.0–1.0 | Jaccard(interests) × 0.5 + location_score × 0.5 | users.interests JSONB + location |
| `recency_boost` | 0.10 | 0.0–0.5 | `exp(-0.1 × days_since_interaction)` — 7-day half-life | Last interaction timestamp |
| `contact_match` | 0.05 | 0/1.0 | Binary: 1.0 if candidate's phone/email matches uploaded contacts | contact_imports table |
| `penalty_declined` | n/a | -0.5 | Applied if viewer previously dismissed this suggestion | suggestion_dismissals table |

### Weight Tuning

- **Phase 1**: Use defaults (cold start)
- **Phase 2**: After 10K accept/reject events, train logistic regression (label = 1 if accepted, 0 if dismissed)
- **Phase 3**: Periodic retraining (weekly) on rolling 30-day window
- **A/B target**: Baseline random ~2–5% accept rate → Ranked target >15%

### 2.3 Diversity & Freshness Constraints (v1.1: Structural Only)

| Constraint | Rule | Reason |
|-----------|------|--------|
| Source diversity | At least 3 of 5 sources represented in top 20 | Don't show only FoF |
| Circle-cluster cap | Max 50% from viewer's top-5 most-connected circles | Prevents one social cluster dominating |
| Interaction-recency | At least 30% with no prior interaction | Surfaces truly new connections |
| New-user boost | At least 2 of top 20 are accounts < 30 days old | Helps new users get discovered |
| Freshness rotation | 20% of slots reserved for never-shown candidates | Prevent stale suggestions |
| Daily limit | Max 30 new suggestions per day | Quality over quantity |

> **v1.1 change**: Removed gender/location demographic constraints. All diversity rules are now structural (source, cluster, recency, account-age). Region-configurable via `suggestion_diversity_config` table.

### 2.4 Pipeline Architecture (v1.1: 3-Tier)

**Tier 1: Event-Driven Incremental (real-time)**
- Triggering events: `circle.accepted`, `circle.removed`, `circle.blocked`, `user.joined_group`, `circle.request.declined`
- Events add user_id to Redis set `dirty:suggestions`
- Worker pool (3 workers) continuously pops and recomputes (~50–200ms per user)
- Rate limit: max 1 recompute per user per 5 minutes

**Tier 2: Nightly Batch Backfill (safety net)**
- Runs 02:00–04:00 local
- Only processes users NOT recomputed in last 24h (typically 10–20% of active users)
- If overruns 04:00: pause, resume next night
- Alert if >30% of users not recomputed in 48h

**Tier 3: On-Demand (user opens page)**
- Check Redis `suggestions:{user_id}` TTL
- If stale (>6h) or empty: serve stale + enqueue recompute
- Next page load gets fresh results

---

## 3. Add to Circle Request Flow

### 3.1 Request State Machine

```
pending ───▶ accepted ─▶ (both users in each other's circle)
  │
  ├───▶ declined_hidden ─▶ (hidden, resend after 30d cooldown)
  │
  ├───▶ blocked ─▶ (permanent, cannot send again)
  │
  ├───▶ cancelled ─▶ (sender withdrew)
  │
  └───▶ expired ─▶ (auto-expire after 30 days, resend allowed)
```

### v1.1: Dual-State Architecture

Each request has two state fields:

| Internal `status` | `sender_view` | Recipient View | Analytics |
|-------------------|---------------|----------------|-----------|
| `pending` | `pending` | Actionable | Active request |
| `accepted` | `accepted` | Connected | Accepted |
| `declined_hidden` | `pending` ← (unchanged!) | Dismissed | Declined (countable) |
| `blocked` | `expired` ← (fake expiry) | Gone | Blocked (countable) |
| `cancelled` | `cancelled` | Gone | Cancelled |
| `expired` | `expired` | Gone | Expired |

> The sender **never** knows they were declined. Their view shows "Pending" until expiry.

### 3.2 Send Request: Step-by-Step

```
User A taps "Add to Circle" on User B:

Step 1: CLIENT (optimistic)
  ├─ Immediately show "Request Sent" state, disable button
  ├─ Generate Idempotency-Key (UUID)
  └─ Fire API call in background

Step 2: API
  POST /api/v1/circle/request
  Headers: Authorization: Bearer {jwt}, Idempotency-Key: {uuid}
  Body: { target_user_id: "user_b_id", message: "optional note" }

Step 3: PROFILE SERVICE
  ├─ v1.1: Atomic idempotency gate (Redis Lua SETNX)
  │   If exists → return cached response (200 OK)
  ├─ Validate (see validation rules below)
  ├─ DB Transaction:
  │   INSERT circle_requests + INSERT outbox_events
  ├─ Cache success response in idempotency key (TTL 24h)
  └─ Return 201 Created

Step 4: EVENT FANOUT (async via NATS)
  ├─ Notification Service: create in-app + push notification
  ├─ WebSocket Gateway: deliver real-time event to recipient
  └─ Suggestion Service: remove candidate from sender's suggestions

Step 5: CLIENT (confirmation)
  ├─ 201 → optimistic state confirmed
  ├─ 4xx → rollback button to "Add to Circle"
  └─ Toast: "Circle request sent to {name}"
```

### 3.3 Validation Rules

| Check | Error Code | HTTP | Client Action |
|-------|-----------|------|---------------|
| Target does not exist | `USER_NOT_FOUND` | 404 | Remove from suggestions |
| Target is self | `SELF_REQUEST` | 400 | Should never happen |
| Already in circle | `ALREADY_CONNECTED` | 409 | Show "In Circle" state |
| Blocked by target | `REQUEST_BLOCKED` | 403 | Show generic "Unable to send" |
| Pending request exists | `DUPLICATE_REQUEST` | 409 | Show "Request Pending" |
| Reverse request exists (B→A pending) | `REVERSE_REQUEST_EXISTS` | 409 | **Auto-accept both!** |
| Daily rate limit exceeded | `RATE_LIMITED` | 429 | Show "Try again tomorrow" |
| Account too new (< 24h) | `ACCOUNT_TOO_NEW` | 403 | Show "Complete profile first" |
| Cooldown active (resend too soon) | `COOLDOWN_ACTIVE` | 429 | Show retry-after timestamp |

### 3.4 Reverse-Request Auto-Merge

If User A has a pending request to User B, and User B sends a request to User A:

1. Auto-accept A's original request in one transaction
2. Create `circle_connections` entry
3. DON'T create B's request (redundant)
4. Audit log: `reason: 'reverse_request_auto_merge'`
5. Notify both: "You and {name} are now connected!"

### 3.5 v1.1: Atomic Idempotency Gate

```lua
-- Redis Lua script: idempotency_gate.lua
-- KEYS[1] = idempotency:{service}:{idempotency_key}
-- ARGV[1] = response_payload (JSON)
-- ARGV[2] = TTL in seconds (86400 = 24h)

local existing = redis.call('GET', KEYS[1])
if existing then
  return {0, existing}  -- duplicate: return cached response
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
return {1, ARGV[1]}    -- first time: proceed
```

If DB transaction fails → `DEL` the idempotency key so client can retry. If it succeeds → update key with full response payload.

### 3.6 v1.1: Transactional Locking for State Transitions

All state transitions (accept/decline/block/cancel) use:

```go
// 1. Lock row
SELECT ... FROM circle_requests WHERE id = $1 FOR UPDATE

// 2. Validate state under lock
if req.Status == targetState { return 200 OK (idempotent) }
if req.Status != "pending" { return error (invalid state) }

// 3. Update + outbox in single transaction
UPDATE circle_requests SET status = ...
INSERT INTO circle_request_audit (...)
INSERT INTO outbox_events (...)

// 4. Post-commit: update Redis cache
```

---

## 4. Real-Time Notification & Accept/Block Flow

### 4.1 Notification Delivery Pipeline

```
User A sends circle request to User B:

① Profile Service publishes event:
   NATS subject: social.circle.request_sent
   Payload: { event_id, request_id, sender_id, recipient_id,
              sender_name, sender_avatar, message, timestamp }

② Notification Service consumes event (< 50ms):
   ├─ INSERT INTO notifications (type: 'circle_request', actionable: true,
   │     actions: ['accept', 'decline', 'block'])
   ├─ UPDATE user_notification_meta SET unread_count = unread_count + 1
   ├─ Push notification via FCM/APNs
   └─ Post-commit: SET notifications:unread:{user_b} {count} EX 3600

③ WebSocket Gateway consumes SAME event (parallel, < 100ms):
   ├─ Look up: HGETALL ws:user:{user_b} (all connections)
   ├─ Dedup: SETNX ws:dedup:{user_b}:{event_id} (per-user, not per-connection)
   ├─ If online: send WebSocket frame to ALL connections (web + mobile)
   │   { type: 'circle.request.received', data: { request_id, sender profile,
   │     message, actions, timestamp } }
   └─ If offline: no-op (push notification handles it)

④ Client receives WebSocket event (< 200ms end-to-end):
   ├─ Show in-app notification toast with Accept/Decline
   ├─ Update notification bell badge count
   └─ If on Requests page: prepend new request to list
```

### 4.2 Accept Flow

```
User B taps "Accept":

CLIENT: animate request card → "Connected!" celebration
API: POST /api/v1/circle/request/{id}/accept

Profile Service (under FOR UPDATE lock):
  ├─ UPDATE circle_requests SET status = 'accepted', sender_view = 'accepted'
  ├─ INSERT INTO circle_connections (user_a < user_b ordering)
  ├─ INSERT INTO circle_request_audit
  ├─ INSERT INTO outbox_events (circle.accepted)
  └─ Post-commit:
      Redis SADD circle:{A} {B}, SADD circle:{B} {A}
      Redis INCR circle:count:{A}, INCR circle:count:{B}

Event consumers (circle.accepted):
  ├─ Notification Service: notify User A "accepted!"
  ├─ WebSocket Gateway: real-time event to User A
  ├─ Feed Service: rebuild inbox for both users
  └─ Suggestion Service: recompute for both users
```

### 4.3 Decline Flow

```
User B taps "Decline":

  ├─ UPDATE status = 'declined_hidden', sender_view remains 'pending'
  ├─ User A is NOT notified (privacy)
  ├─ A's sent requests: still shows "Pending" until expiry
  ├─ Resend allowed after 30-day cooldown (resend_after field)
  └─ Suggestion Service: add mild penalty (-0.2) for this pair
```

### 4.4 Block Flow

```
User B taps "Block" (behind ⋯ menu, not primary action):

  ├─ UPDATE status = 'blocked', sender_view = 'expired'
  ├─ INSERT INTO circle_blocks (blocker_id, blocked_id)
  ├─ If in circle: remove circle_connection
  ├─ Publish circle.blocked event → all services enforce (see §4.5)
  ├─ A sees: request disappears (as if account deleted)
  └─ B can unblock later via Settings > Blocked Users
```

### 4.5 v1.1: Cross-Service Block Enforcement (3 Layers)

**Layer 1: Shared Block Policy Library**

```go
// pkg/policy/block.go — imported by every service
type BlockChecker interface {
  IsBlocked(ctx, userA, userB UUID) bool
  // Returns true if EITHER user has blocked the other
}
// Redis-backed: SISMEMBER blocks:{a} {b} || SISMEMBER blocks:{b} {a}
```

**Layer 2: Per-Service Enforcement**

| Service | Check Point | Blocked Behavior |
|---------|------------|-----------------|
| Profile | `GET /users/{id}` | Return 404 |
| Search | Results filtering | Excluded from results |
| Suggestions | Candidate generation + serve-time | Never appear |
| Feed | Fan-out + feed read | Skip fan-out, filter on read |
| Chat | Message send + conversation list | Cannot send, conversation hidden |
| Notifications | Before creating notification | Drop events silently |
| Engagement | Like/comment/share | Return 403 |
| Mentions | @ mention parsing | Strip silently |
| WebSocket | Event routing | Never route events |

**Layer 3: API Gateway Middleware**

```go
// BlockGuardMiddleware — runs on every request involving two users
func BlockGuardMiddleware(next Handler) Handler {
  targetUserID := extractTargetUser(req)
  if blockChecker.IsBlocked(ctx, currentUser.ID, targetUserID) {
    return 404 // behave as if target doesn't exist
  }
  return next(ctx, req)
}
```

### 4.6 v1.1: WebSocket Multi-Device Model

```
Redis data model:
  HSET ws:user:{user_id} {conn_id} {device_type, gateway_node, connected_at, last_heartbeat}
  SET  ws:conn:{conn_id} {user_id} EX 120    -- refreshed by heartbeat
  SADD ws:node:{gateway_node} {conn_id}

Heartbeat: client sends every 30s → server refreshes TTL
Cleanup: every 60s per node, remove expired connections
Event delivery: to ALL connections per user, dedup by event_id per user
Reconnect replay: send missed events from outbox (last 5 min, max 50)
```

### 4.7 Performance Targets

| Metric | Target |
|--------|--------|
| Request send (API) | < 100ms (p99) |
| Notification persistence | < 50ms (p99) |
| WebSocket delivery (end-to-end) | < 200ms (p99) |
| Push notification | < 3s (p95) |
| Accept → Feed rebuild | < 5s (p99) |
| Suggestion recompute | < 500ms (p99) per user |

---

## 5. Anti-Abuse & Rate Limits

| Action | Limit | Window | Penalty on Exceed |
|--------|-------|--------|-------------------|
| Send circle request | 50 | 24 hours | 429 + backoff. Flagged if repeated. |
| Accept requests | 200 | 24 hours | 429. Unlikely legitimate. |
| Decline requests | No limit | n/a | No limit needed. |
| Block users | 100 | 24 hours | 429. Excessive blocking → review. |
| Dismiss suggestions | 100 | 24 hours | 429. "No more suggestions today." |
| Resend after decline | 1 per target | 30 days | `COOLDOWN_ACTIVE` error. |
| New account requests | 10 | First 24 hours | Reduced limit prevents spam bots. |

### Spam Detection

- Rapid-fire (> 10 in 1 min) → 1-hour cooldown
- Low accept rate (< 5% over 100 requests) → flagged for review
- Same message to 10+ users → quarantine + review
- Blocked by 5+ users in 24h → request sending disabled for 7 days
- New account (< 24h) + no profile photo + rapid requests → captcha gate

---

## 6. Event Catalog

| Event | NATS Subject | Producer | Consumers |
|-------|-------------|----------|-----------|
| `circle.request.sent` | `social.circle.request_sent` | Profile Service | Notification, WebSocket, Suggestion |
| `circle.request.accepted` | `social.circle.accepted` | Profile Service | Notification, WebSocket, Feed, Suggestion |
| `circle.request.declined` | `social.circle.declined` | Profile Service | Suggestion |
| `circle.request.blocked` | `social.circle.blocked` | Profile Service | Suggestion, Feed, Chat, Search, All |
| `circle.request.cancelled` | `social.circle.cancelled` | Profile Service | Notification, WebSocket |
| `circle.removed` | `social.circle.removed` | Profile Service | Feed, WebSocket, Suggestion |
| `circle.unblocked` | `social.circle.unblocked` | Profile Service | Suggestion, Search |
| `circle.auto_accepted` | `social.circle.auto_accepted` | Profile Service | Notification, WebSocket, Feed, Suggestion |

### v1.1: Consumer Idempotency

```
Every event has a unique event_id.

Before processing:
  dedup = redis.SetNX('consumed:{consumer}:{event_id}', '1', EX: 86400)
  if !dedup → ACK and skip

Handler design: ALL handlers are idempotent even without dedup.
  • INSERT ... ON CONFLICT DO NOTHING
  • Check current state before updating
  • SET (not INCR) when possible

Retry: max 3 retries (1s, 5s, 25s exponential backoff)
DLQ: NATS JetStream dead letter queue, alert if depth > 100
```

---

## 7. Database Schemas

### circle_requests (v1.1)

```sql
CREATE TABLE circle_requests (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sender_id     UUID NOT NULL,
  recipient_id  UUID NOT NULL,

  status         TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN (
      'pending', 'accepted', 'declined_hidden',
      'blocked', 'cancelled', 'expired'
    )),

  sender_view    TEXT NOT NULL DEFAULT 'pending'
    CHECK (sender_view IN ('pending', 'accepted', 'expired', 'cancelled')),

  message        TEXT CHECK (length(message) <= 200),
  responded_at   TIMESTAMPTZ,
  expires_at     TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 days',
  resend_after   TIMESTAMPTZ,
  attempt_count  INTEGER NOT NULL DEFAULT 1,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

  UNIQUE (sender_id, recipient_id)
    WHERE status IN ('pending', 'declined_hidden')
);

CREATE INDEX idx_circle_req_recipient_pending
  ON circle_requests (recipient_id, created_at DESC)
  WHERE status = 'pending';
```

### circle_request_audit (v1.1)

```sql
CREATE TABLE circle_request_audit (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id    UUID NOT NULL REFERENCES circle_requests(id),
  from_status   TEXT NOT NULL,
  to_status     TEXT NOT NULL,
  actor_id      UUID NOT NULL,
  reason        TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_request ON circle_request_audit (request_id);
```

### circle_connections

```sql
CREATE TABLE circle_connections (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_a_id   UUID NOT NULL,
  user_b_id   UUID NOT NULL,
  request_id  UUID REFERENCES circle_requests(id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

  CHECK (user_a_id < user_b_id),  -- canonical ordering for dedup
  UNIQUE (user_a_id, user_b_id)
);

CREATE INDEX idx_circle_user_a ON circle_connections (user_a_id);
CREATE INDEX idx_circle_user_b ON circle_connections (user_b_id);
```

### circle_blocks

```sql
CREATE TABLE circle_blocks (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  blocker_id  UUID NOT NULL,
  blocked_id  UUID NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (blocker_id, blocked_id)
);
```

### circle_suggestions

```sql
CREATE TABLE circle_suggestions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  viewer_id       UUID NOT NULL,
  candidate_id    UUID NOT NULL,
  affinity_score  FLOAT NOT NULL,
  mutual_count    INTEGER NOT NULL DEFAULT 0,
  source          TEXT NOT NULL
    CHECK (source IN ('fof','interaction','co_group','profile','contact')),
  computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (viewer_id, candidate_id)
);
CREATE INDEX idx_suggestions_viewer
  ON circle_suggestions (viewer_id, affinity_score DESC);
```

### suggestion_dismissals

```sql
CREATE TABLE suggestion_dismissals (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  viewer_id       UUID NOT NULL,
  candidate_id    UUID NOT NULL,
  dismissed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (viewer_id, candidate_id)
);
```

### notifications

```sql
CREATE TABLE notifications (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID NOT NULL,
  type           TEXT NOT NULL
    CHECK (type IN ('circle_request','circle_accepted',
      'circle_request_cancelled','post_liked','post_commented',
      'post_shared','mention','group_invite','system')),
  actor_id       UUID,
  reference_id   UUID,
  reference_type TEXT,
  title          TEXT NOT NULL,
  body           TEXT,
  read           BOOLEAN NOT NULL DEFAULT false,
  actionable     BOOLEAN NOT NULL DEFAULT false,
  action_taken   TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notifications_user
  ON notifications (user_id, read, created_at DESC);
```

### user_notification_meta (v1.1)

```sql
CREATE TABLE user_notification_meta (
  user_id       UUID PRIMARY KEY,
  unread_count  INTEGER NOT NULL DEFAULT 0 CHECK (unread_count >= 0),
  last_read_at  TIMESTAMPTZ,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### contact_imports (v1.1)

```sql
CREATE TABLE contact_imports (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  uploader_id      UUID NOT NULL,
  hash_type        TEXT NOT NULL DEFAULT 'sha256',
  contact_hash     TEXT NOT NULL,       -- SHA-256(normalized_value)
  contact_type     TEXT NOT NULL CHECK (contact_type IN ('phone','email')),
  matched_user_id  UUID,
  consent_granted  BOOLEAN NOT NULL DEFAULT false,
  consent_timestamp TIMESTAMPTZ,
  uploaded_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at       TIMESTAMPTZ NOT NULL DEFAULT now() + interval '180 days',
  UNIQUE (uploader_id, contact_hash)
);
```

### dead_letter_events (v1.1)

```sql
CREATE TABLE dead_letter_events (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id      TEXT NOT NULL,
  event_type    TEXT NOT NULL,
  consumer_name TEXT NOT NULL,
  payload       JSONB NOT NULL,
  error_message TEXT NOT NULL,
  retry_count   INTEGER NOT NULL DEFAULT 0,
  resolution    TEXT CHECK (resolution IN ('replayed','skipped')),
  resolved_by   UUID,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at   TIMESTAMPTZ
);
```

---

## 8. API Reference

### Circle Requests

| Endpoint | Method | Auth | Description | Rate Limit |
|----------|--------|------|-------------|-----------|
| `/api/v1/circle/request` | POST | Bearer + Idempotency-Key | Send circle request | 50/day |
| `/api/v1/circle/request/{id}/accept` | POST | Bearer (recipient) | Accept pending request | 60/min |
| `/api/v1/circle/request/{id}/decline` | POST | Bearer (recipient) | Decline request (hidden) | 60/min |
| `/api/v1/circle/request/{id}/block` | POST | Bearer (recipient) | Block sender permanently | 60/min |
| `/api/v1/circle/request/{id}/cancel` | POST | Bearer (sender) | Cancel pending request | 60/min |
| `/api/v1/circle/requests/pending` | GET | Bearer | List pending inbox (paginated) | 30/min |
| `/api/v1/circle/requests/sent` | GET | Bearer | List sent requests (paginated) | 30/min |

### Circle Management

| Endpoint | Method | Auth | Description | Rate Limit |
|----------|--------|------|-------------|-----------|
| `/api/v1/circle/members` | GET | Bearer | List circle members (paginated, searchable) | 60/min |
| `/api/v1/circle/{user_id}/remove` | POST | Bearer | Remove from circle | 20/min |
| `DELETE /api/v1/circle/blocks/{user_id}` | DELETE | Bearer (blocker) | Unblock user | 20/day |

### Suggestions

| Endpoint | Method | Auth | Description | Rate Limit |
|----------|--------|------|-------------|-----------|
| `/api/v1/circle/suggestions` | GET | Bearer | Top 20 ranked suggestions | 30/min |
| `/api/v1/circle/suggestions/{id}/dismiss` | POST | Bearer | Dismiss suggestion | 60/min |
| `/api/v1/circle/suggestions/refresh` | POST | Bearer | Force recompute (async 202) | 5/hour |

### Contacts

| Endpoint | Method | Auth | Description | Rate Limit |
|----------|--------|------|-------------|-----------|
| `/api/v1/contacts/import` | POST | Bearer + consent | Upload hashed contacts | 1/day |

---

## 9. v1.1 Revision Fixes

### Summary

| # | Severity | Issue | Fix |
|---|----------|-------|-----|
| 1 | **MUST FIX** | Idempotency race-prone | Lua atomic SETNX gate + response caching |
| 2 | **MUST FIX** | Uniqueness strategy | Single-row-per-pair + audit log + resend rules |
| 3 | **MUST FIX** | No transactional locking | SELECT FOR UPDATE + single-txn state machine |
| 4 | **MUST FIX** | Decline state ambiguous | Dual-state: `status` (internal) vs `sender_view` (API) |
| 5 | **MUST FIX** | Block not cross-service | 3-layer: policy lib + per-service + gateway middleware |
| 6 | **SHOULD FIX** | Score normalization unstable | Fixed transforms: log scaling, global p95 |
| 7 | **SHOULD FIX** | Pipeline timing optimistic | 3-tier: event-driven + batch backfill + on-demand |
| 8 | **SHOULD FIX** | Contact privacy missing | SHA-256 hashing, consent, 180d retention, regional toggle |
| 9 | **SHOULD FIX** | Counter drift | DB source of truth + Redis cache + heartbeat reconciliation |
| 10 | **SHOULD FIX** | WebSocket too simple | Multi-device registry, heartbeat, dedup, reconnect replay |
| 11 | **NICE TO HAVE** | No unblock endpoint | `DELETE /blocks/{id}` + post-unblock rules |
| 12 | **NICE TO HAVE** | No observability | 10 SLOs + 10 tracked metrics |
| 13 | **NICE TO HAVE** | No consumer dedup | SETNX dedup, 3-retry backoff, DLQ |
| 14 | **NICE TO HAVE** | Remove rebuild unclear | Feed cleanup, mutual decrements, re-eligibility |
| 15 | **NICE TO HAVE** | Diversity uses demographics | Structural diversity only, region-configurable |

---

## 10. Observability & SLOs

### SLOs

| Metric | Target | Alert Threshold |
|--------|--------|----------------|
| Request send latency | p99 < 100ms | > 200ms for 5min |
| Accept latency (API) | p99 < 150ms | > 300ms for 5min |
| WebSocket delivery | p99 < 200ms (e2e) | > 500ms for 5min |
| Push notification | p95 < 3s | > 10s for 5min |
| Suggestion serve | p99 < 50ms (Redis) | > 100ms for 5min |
| Suggestion recompute | p99 < 500ms/user | > 2s for 5min |
| Accept success rate | > 99.9% | < 99% for 10min |
| Duplicate request rate | < 0.1% | > 1% for 10min |
| Block propagation | p99 < 2s all services | > 5s for 5min |
| Counter drift | < 1% mismatch | > 5% for 10min |

### Key Metrics

- `circle_request_sent_total` — Counter (labels: source, status)
- `circle_request_accepted_total` — Counter (labels: response_time_bucket, was_reverse_merge)
- `suggestion_accept_rate` — Gauge per source (target: >15%)
- `suggestion_accept_rate_by_rank` — Gauge by position (1–20)
- `spam_flags_triggered_total` — Counter (labels: reason)
- `block_propagation_lag_seconds` — Histogram
- `ws_event_delivery_lag_seconds` — Histogram
- `dead_letter_events_total` — Counter (labels: event_type, consumer, reason)
- `suggestion_recompute_duration_seconds` — Histogram (labels: trigger)
- `notification_counter_reconciliation_total` — Counter (labels: direction, delta_bucket)

### Unread Counter: DB-Authoritative with Redis Cache

```
Write: INSERT notification → UPDATE user_notification_meta unread_count + 1 → SET Redis cache
Read:  Check Redis → cache miss? Query DB, cache result
Mark-read: UPDATE all to read → recount from DB → SET Redis to 0
Reconcile: On WebSocket heartbeat (every 5 min), compare Redis vs DB, push correction
```

### Remove from Circle Rebuild

```
circle.removed event triggers:
  Feed:        Remove posts from both users' inboxes (bidirectional)
  Mutual Cache: DECR mutual_count for all shared connections
  Suggestions:  Recompute both (30-day cooldown before re-appearing)
  Chat:         DM conversation remains accessible (circle gates feed, not chat)
  Content:      Group posts still visible (group membership is separate)
```

---

*Postbook · Postgram · PostTube — February 2026*
