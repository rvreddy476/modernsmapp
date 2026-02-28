# Postbook — Engagement System Architecture (v1.1)

> **Stack**: Go + Fiber · PostgreSQL · ScyllaDB · Redis · NATS JetStream  
> **Scope**: Likes · Comments · Replies · Shares · Bookmarks · Comment Likes  
> **SLA**: Like toggle < 20ms · Comment create < 100ms · Feed hydration (20 posts) < 5ms  
> **This is the SOLE implementation reference. Implement exactly what is specified.**

---

## 1. Design Philosophy

### 1.1 Dual-Write with Reconciliation

Every engagement action writes to TWO paths simultaneously:

| Path | Technology | Purpose | Latency | Durability |
|------|-----------|---------|---------|------------|
| **Hot Path** | Redis | Counter reads, membership checks ("did I like this?"), liker previews | < 1ms | Volatile (TTL + reconciliation) |
| **Durable Records** | ScyllaDB | Individual like/comment-like/share/bookmark rows | 3–10ms | Replicated, partition-tolerant |
| **Counters + Analytics** | PostgreSQL | Denormalized counts, reporting, comment content | 10–50ms | Full ACID |
| **Event Bus** | NATS JetStream | Async write propagation, notification fan-out | < 5ms | At-least-once, DLQ |

**Flow**: User taps like → Lua script atomically toggles Redis (< 1ms) → API returns 200 (< 20ms) → NATS event fires async → ScyllaDB Writer + PG Counter + Notification + WebSocket consumers process in parallel.

**Users NEVER wait for a database write.** The UI updates optimistically, Redis confirms in < 20ms, durable writes happen async. If a durable write fails, NATS retry ensures eventual consistency. Every 5 minutes, reconciliation corrects any drift between Redis and the authoritative stores.

### 1.2 Authority Chain

```
Likes/Shares/Bookmarks:  ScyllaDB engagement_counters  →  Redis (display)  +  PostgreSQL (analytics, derived)
Comments:                PostgreSQL COUNT(*)             →  Redis (display)
Redis is NEVER authoritative. It is a fast cache corrected by reconciliation.
```

---

## 2. Engagement Types

| Type | Target | Cardinality | Toggle? | Visibility | Primary Store |
|------|--------|------------|---------|-----------|---------------|
| **Like** | Post | Many→One | Yes (on/off) | Count public, liker avatars public | ScyllaDB |
| **Comment** | Post | Many→One | No (create/delete) | Full content public | PostgreSQL |
| **Comment Like** | Comment | Many→One | Yes (on/off) | Count only, likers hidden | ScyllaDB |
| **Reply** | Comment | One→One **(post owner only)** | No (create/delete) | Full content public | PostgreSQL |
| **Share** | Post | Many→One | No (one-time) | Count public, sharers hidden | ScyllaDB |
| **Bookmark** | Post | Many→One | Yes (on/off) | **PRIVATE** to user only | ScyllaDB |

### Reply Rule (HARD ENFORCEMENT)

- **Only the post owner can reply to comments on their post.**
- Other users CANNOT reply. They can only comment (top-level) or like comments.
- Max 1 reply per comment (`reply_count` is always 0 or 1).
- Cannot reply to a reply (max depth = 2: Comment → Reply).
- API check: `if req.UserID != post.AuthorID → 403 REPLY_OWNER_ONLY`

### Comment Thread Structure

```
Post: "Just wrapped up the new dashboard design..."
  │
  ├─ Comment (Arjun): "Looks amazing! What tool did you use?"
  │   └─ Reply (Priya, POST OWNER): "Thanks! Figma + custom CSS"
  │
  ├─ Comment (Kiran): "The glassmorphism is on point"
  │   └─ Reply (Priya, POST OWNER): "Glad you noticed that detail!"
  │
  └─ Comment (Sneha): "Inspiring work!"
      (no reply yet — post owner hasn't responded)
```

---

## 3. Event Envelope (All Events)

Every engagement event carries a full envelope with ordering metadata. This prevents out-of-order processing and enables consumer dedup.

```go
type EngagementEvent struct {
    // Identity
    EventID     string    `json:"event_id"`      // UUIDv7 (time-sortable)
    EventType   string    `json:"event_type"`     // e.g. "engagement.post.liked"

    // Ordering
    ActionTS    time.Time `json:"action_ts"`      // wall-clock from Lua script
    UserSeqNo   int64     `json:"user_seq_no"`    // per-user monotonic counter (Redis INCR)

    // Payload
    PostID      uuid.UUID `json:"post_id"`
    UserID      uuid.UUID `json:"user_id"`
    AuthorID    uuid.UUID `json:"author_id"`
    TargetType  string    `json:"target_type"`    // "post" or "comment"
    TargetID    uuid.UUID `json:"target_id"`      // post_id or comment_id
    Action      string    `json:"action"`         // "like", "share", "bookmark", "comment", "reply"
    IsSet       bool      `json:"is_set"`         // true = created, false = removed

    // Schema
    Version     int       `json:"version"`        // 1
}
```

**UserSeqNo**: Per-user monotonic counter via `INCR eng:seq:{user_id}` in Redis (24h TTL). Ensures strict ordering for a given user's actions even if wall-clock is ambiguous.

**ActionTS**: Captured INSIDE the Lua script, BEFORE async NATS publish. This is the user's action time, not the consumer's receive time.

---

## 4. Database Schemas

### 4.1 ScyllaDB — Engagement Records

#### Post Likes (SHARDED — Fix #4)

```sql
-- Partition by (post_id, shard) to avoid hot partitions on viral posts
-- shard = hash(user_id) % 16
CREATE TABLE post_likes (
    post_id     UUID,
    shard       SMALLINT,   -- 0..15
    user_id     UUID,
    created_at  TIMESTAMP,
    PRIMARY KEY ((post_id, shard), user_id)
) WITH CLUSTERING ORDER BY (user_id ASC);

-- Reverse: "what posts did I like?"
CREATE TABLE user_likes (
    user_id     UUID,
    post_id     UUID,
    created_at  TIMESTAMP,
    PRIMARY KEY ((user_id), created_at DESC)
) WITH CLUSTERING ORDER BY (created_at DESC);
```

**Shard calculation**:
```go
const LIKE_SHARDS = 16

func likeShard(userID uuid.UUID) int16 {
    h := binary.BigEndian.Uint64(userID[:8])
    return int16(h % LIKE_SHARDS)
}
```

**"Did I like this?"** is still O(1): shard is deterministic from user_id, so the query targets one partition. No scatter-gather needed for the hot-path membership check.

#### Comment Likes

```sql
CREATE TABLE comment_likes (
    comment_id  UUID,
    user_id     UUID,
    created_at  TIMESTAMP,
    PRIMARY KEY ((comment_id), user_id)
) WITH CLUSTERING ORDER BY (user_id ASC);
```

No sharding needed — individual comments won't get millions of likes.

#### Shares (SHARDED)

```sql
CREATE TABLE post_shares (
    post_id     UUID,
    shard       SMALLINT,   -- 0..15, same logic as likes
    user_id     UUID,
    created_at  TIMESTAMP,
    share_type  TEXT,        -- 'repost', 'quote', 'external'
    quote_text  TEXT,
    PRIMARY KEY ((post_id, shard), user_id)
) WITH CLUSTERING ORDER BY (user_id ASC);

CREATE TABLE user_shares (
    user_id     UUID,
    post_id     UUID,
    created_at  TIMESTAMP,
    share_type  TEXT,
    PRIMARY KEY ((user_id), created_at DESC)
) WITH CLUSTERING ORDER BY (created_at DESC);
```

#### Bookmarks (Collection in PK — Fix #6)

```sql
-- Collection as partition component (no ALLOW FILTERING)
CREATE TABLE user_bookmarks (
    user_id     UUID,
    collection  TEXT,        -- 'default', 'read-later', 'inspiration', ...
    created_at  TIMESTAMP,
    post_id     UUID,
    PRIMARY KEY ((user_id, collection), created_at, post_id)
) WITH CLUSTERING ORDER BY (created_at DESC, post_id ASC);

-- Membership check: "did I bookmark this?"
CREATE TABLE bookmark_check (
    user_id     UUID,
    post_id     UUID,
    collection  TEXT,
    PRIMARY KEY ((user_id), post_id)
);
```

Querying "my read-later bookmarks" = single partition scan on `(user_id, 'read-later')`. No filtering needed.

#### Engagement Counters (Fix #3 — Replaces COUNT(*))

```sql
-- NOT Scylla native counters (they have consistency issues).
-- Regular table with LWT for atomic updates.
CREATE TABLE engagement_counters (
    target_type   TEXT,       -- 'post', 'comment'
    target_id     UUID,
    counter_type  TEXT,       -- 'likes', 'shares', 'bookmarks'
    count         BIGINT,
    updated_at    TIMESTAMP,
    PRIMARY KEY ((target_type, target_id), counter_type)
);
```

Updated by ScyllaDB Writer consumer via read-then-CAS (lightweight transaction). This is the reconciliation authority for likes/shares/bookmarks. Never use `COUNT(*)` on large partitions.

#### Comment Timeline Index (Enhancement — Fix #8)

```sql
-- Optional at launch. Enable when PG comment read p99 > 50ms.
CREATE TABLE comment_timeline (
    post_id       UUID,
    created_at    TIMESTAMP,
    comment_id    UUID,
    author_id     UUID,
    is_reply      BOOLEAN,
    parent_id     UUID,
    preview_text  TEXT,       -- first 200 chars
    like_count    INT,
    is_deleted    BOOLEAN,
    PRIMARY KEY ((post_id), created_at, comment_id)
) WITH CLUSTERING ORDER BY (created_at DESC, comment_id ASC);
```

### 4.2 PostgreSQL — Comments + Counters

#### Comments

```sql
CREATE TABLE comments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id        UUID NOT NULL REFERENCES posts(id),
    author_id      UUID NOT NULL REFERENCES users(id),
    parent_id      UUID REFERENCES comments(id),  -- NULL = top-level
    body           TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    like_count     INTEGER NOT NULL DEFAULT 0,
    reply_count    INTEGER NOT NULL DEFAULT 0,      -- always 0 or 1
    is_reply       BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_comments_post ON comments (post_id, created_at DESC) WHERE is_deleted = FALSE;
CREATE INDEX idx_comments_parent ON comments (parent_id, created_at ASC) WHERE parent_id IS NOT NULL AND is_deleted = FALSE;
CREATE INDEX idx_comments_author ON comments (author_id, created_at DESC) WHERE is_deleted = FALSE;
```

#### Engagement Counters (Denormalized)

```sql
CREATE TABLE post_engagement_counts (
    post_id         UUID PRIMARY KEY REFERENCES posts(id),
    like_count      INTEGER NOT NULL DEFAULT 0,
    comment_count   INTEGER NOT NULL DEFAULT 0,
    share_count     INTEGER NOT NULL DEFAULT 0,
    bookmark_count  INTEGER NOT NULL DEFAULT 0,  -- internal only, NEVER exposed in API
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Auto-create on post insert
CREATE OR REPLACE FUNCTION create_engagement_counts()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO post_engagement_counts (post_id) VALUES (NEW.id);
    RETURN NEW;
END; $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_create_engagement_counts
    AFTER INSERT ON posts
    FOR EACH ROW EXECUTE FUNCTION create_engagement_counts();
```

#### Event Processing Log (Fix #7 — Consumer Dedup)

```sql
CREATE TABLE engagement_event_log (
    event_id      TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    target_id     UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_event_log_age ON engagement_event_log (processed_at);

-- Hourly cleanup: DELETE WHERE processed_at < now() - INTERVAL '48 hours'
```

### 4.3 Redis Data Structures

```
── COUNTERS (fast reads) ──
HSET  post:eng:{post_id}  likes 234  comments 18  shares 7
  No TTL (permanent, corrected by reconciliation every 5 min)

── MEMBERSHIP CHECKS (all have 24h TTL, ScyllaDB fallback on miss) ──
SET   liked:{user_id}:{post_id}          1  EX 86400
SET   shared:{user_id}:{post_id}         1  EX 86400
SET   bookmarked:{user_id}:{post_id}     1  EX 86400    ← Fix #5: was no-TTL
SET   comment_liked:{user_id}:{comment_id}  1  EX 86400

── LIKER PREVIEW (first 3 for avatar display) ──
LPUSH  post:likers:{post_id}  {user_id}
LTRIM  post:likers:{post_id}  0  2

── HOT COMMENTS CACHE ──
ZSET   post:comments:{post_id}  score=timestamp  member=comment_json
  TTL: 1h (rebuilt from DB on miss)

── HOT POST SET (for reconciliation) ──
SADD   hot:posts  {post_id}    (on every engagement action)

── USER SEQUENCE COUNTER (for event ordering) ──
INCR   eng:seq:{user_id}       (24h TTL, auto-cleanup)

── CONSUMER DEDUP ──
SETNX  consumed:{consumer_name}:{event_id}  1  EX 86400

── NOTIFICATION DEBOUNCE BUFFER ──
ZADD   notif:like_buffer:{post_id}:{author_id}  {timestamp}  {liker_user_id}
```

---

## 5. API Endpoints

### 5.1 Post Engagement

| Endpoint | Method | Description | Rate Limit |
|----------|--------|-------------|------------|
| `/api/v1/posts/{id}/like` | POST | Toggle like on/off | 120/hour |
| `/api/v1/posts/{id}/comments` | POST | Create a comment | 10/min |
| `/api/v1/posts/{id}/comments` | GET | Get comments (paginated, cursor) | 60/min |
| `/api/v1/posts/{id}/share` | POST | Share a post (repost/quote/external) | 30/hour |
| `/api/v1/posts/{id}/bookmark` | POST | Toggle bookmark on/off | 200/hour |

### 5.2 Comment Engagement

| Endpoint | Method | Description | Rate Limit |
|----------|--------|-------------|------------|
| `/api/v1/comments/{id}` | DELETE | Soft-delete own comment | 30/min |
| `/api/v1/comments/{id}` | PATCH | Edit own comment (within 15 min) | 20/min |
| `/api/v1/comments/{id}/like` | POST | Toggle like on a comment | 120/hour |
| `/api/v1/comments/{id}/reply` | POST | Reply (**POST OWNER ONLY**) | 10/min |

### 5.3 Bookmarks

| Endpoint | Method | Description | Rate Limit |
|----------|--------|-------------|------------|
| `/api/v1/me/bookmarks` | GET | Get my bookmarks (?collection=&cursor=&limit=) | 60/min |
| `/api/v1/me/bookmark-collections` | GET | List collections with counts | 60/min |
| `/api/v1/me/bookmarks/{post_id}/collection` | PUT | Move bookmark to collection | 30/min |

---

## 6. Like System (Complete Flow)

### 6.1 Lua Toggle Script (Atomic)

```go
// POST /api/v1/posts/:post_id/like

// Keys: likeKey, engKey, likersKey, seqKey
// Args: userID, nowMicro

result := redis.Eval(ctx, `
    local likeKey   = KEYS[1]
    local engKey    = KEYS[2]
    local likersKey = KEYS[3]
    local seqKey    = KEYS[4]
    local userID    = ARGV[1]
    local nowMicro  = tonumber(ARGV[2])

    local seq = redis.call('INCR', seqKey)
    redis.call('EXPIRE', seqKey, 86400)

    local exists = redis.call('EXISTS', likeKey)
    if exists == 1 then
        redis.call('DEL', likeKey)
        redis.call('HINCRBY', engKey, 'likes', -1)
        redis.call('LREM', likersKey, 0, userID)
        local count = redis.call('HGET', engKey, 'likes')
        return {0, tonumber(count) or 0, seq, nowMicro}
    else
        redis.call('SET', likeKey, '1', 'EX', 86400)
        redis.call('HINCRBY', engKey, 'likes', 1)
        redis.call('LPUSH', likersKey, userID)
        redis.call('LTRIM', likersKey, 0, 2)
        local count = redis.call('HGET', engKey, 'likes')
        return {1, tonumber(count) or 0, seq, nowMicro}
    end
`, []string{likeKey, engKey, likersKey, seqKey}, userID.String(), time.Now().UnixMicro())
```

One Lua script = one Redis RTT = atomic toggle + counter + liker preview + sequence number + timestamp. Response returns BEFORE NATS publish.

### 6.2 Like Handler

```go
func (h *EngagementHandler) ToggleLike(c *fiber.Ctx) error {
    userID := c.Locals("user_id").(uuid.UUID)
    postID := uuid.MustParse(c.Params("post_id"))

    // Block check
    postAuthorID := h.postCache.GetAuthorID(ctx, postID)
    if h.blockChecker.IsBlocked(ctx, userID, postAuthorID) {
        return c.Status(404).JSON(Error{Code: "POST_NOT_FOUND"})
    }

    // Self-engagement check
    if userID == postAuthorID {
        return c.Status(400).JSON(Error{Code: "SELF_ENGAGEMENT"})
    }

    // Rate limit
    if !h.rateLimiter.Allow(ctx, "like:"+userID.String(), 120, time.Hour) {
        return c.Status(429).JSON(Error{Code: "RATE_LIMITED"})
    }

    // Atomic Lua toggle (returns: isLiked, count, seq, actionTS)
    // ... (Lua script from 6.1) ...

    // Build enriched event envelope
    event := EngagementEvent{
        EventID:    uuid.NewV7().String(),
        EventType:  eventType, // "engagement.post.liked" or "engagement.post.unliked"
        ActionTS:   time.UnixMicro(actionTSMicro),
        UserSeqNo:  seq,
        PostID:     postID,
        UserID:     userID,
        AuthorID:   postAuthorID,
        TargetType: "post",
        TargetID:   postID,
        Action:     "like",
        IsSet:      isLiked,
        Version:    1,
    }

    // Publish AFTER response (non-blocking goroutine)
    go h.nats.Publish(event.EventType, event)

    return c.JSON(LikeResponse{Liked: isLiked, Count: count})
}
```

### 6.3 ScyllaDB Like Consumer (Fix #1: No BATCH, Fix #2: USING TIMESTAMP)

```go
func (c *ScyllaLikeConsumer) HandleLikeEvent(event EngagementEvent) error {
    // Dedup
    if c.isDuplicate(event.EventID) { return nil }

    ts := event.ActionTS.UnixMicro()
    shard := likeShard(event.UserID)

    if event.IsSet {
        // LIKE: two independent idempotent writes (NOT a BATCH)

        // Write 1: post_likes (sharded partition)
        err1 := c.scylla.Query(`
            INSERT INTO post_likes (post_id, shard, user_id, created_at)
            VALUES (?, ?, ?, ?) USING TIMESTAMP ?
        `, event.PostID, shard, event.UserID, event.ActionTS, ts).Exec()

        // Write 2: user_likes
        err2 := c.scylla.Query(`
            INSERT INTO user_likes (user_id, post_id, created_at)
            VALUES (?, ?, ?) USING TIMESTAMP ?
        `, event.UserID, event.PostID, event.ActionTS, ts).Exec()

        if err1 != nil { return err1 }
        if err2 != nil { return err2 }

    } else {
        // UNLIKE: two independent deletes with USING TIMESTAMP (LWW)
        c.scylla.Query(`DELETE FROM post_likes USING TIMESTAMP ? WHERE post_id = ? AND shard = ? AND user_id = ?`,
            ts, event.PostID, shard, event.UserID).Exec()
        c.scylla.Query(`DELETE FROM user_likes USING TIMESTAMP ? WHERE user_id = ? AND post_id = ?`,
            ts, event.UserID, event.PostID).Exec()
    }

    // Update engagement_counters via LWT (Fix #3)
    delta := 1
    if !event.IsSet { delta = -1 }
    c.incrementCounter("post", event.PostID.String(), "likes", delta)

    c.markProcessed(event.EventID)
    return nil
}
```

**USING TIMESTAMP** gives cell-level last-write-wins. If events arrive out-of-order (e.g., unlike at T=200 arrives after like at T=300), the newer timestamp wins and the row state is correct.

### 6.4 PostgreSQL Counter Consumer (Fix #7: Dedup)

```go
func (c *PGCounterConsumer) HandleLikeEvent(event EngagementEvent) error {
    return c.db.Transaction(func(tx *gorm.DB) error {
        // Dedup: insert event_id, ON CONFLICT DO NOTHING
        result := tx.Exec(`
            INSERT INTO engagement_event_log (event_id, event_type, target_id)
            VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING
        `, event.EventID, event.EventType, event.TargetID)

        if result.RowsAffected == 0 {
            return nil // duplicate, skip
        }

        delta := 1
        if !event.IsSet { delta = -1 }

        tx.Exec(`
            UPDATE post_engagement_counts
            SET like_count = GREATEST(like_count + $2, 0), updated_at = now()
            WHERE post_id = $1
        `, event.TargetID, delta)

        return nil
    })
}
```

Single PG transaction: dedup check + counter update. Duplicates are zero-cost (insert is no-op, counter untouched).

### 6.5 Notification Debouncing

```
# Buffer: Redis sorted set per post per author
ZADD notif:like_buffer:{post_id}:{author_id} {timestamp} {liker_user_id}

# Worker runs every 30 seconds:
# For each buffer:
#   count = ZCARD
#   names = ZRANGE (last 2)
#   Send ONE notification:
#     1 liker:  "{Priya} liked your post"
#     2 likers: "{Priya} and {Arjun} liked your post"
#     3+ likers: "{Priya}, {Arjun} and 87 others liked your post"
#   DEL buffer key

# Result: max 1 notification per post per 30 seconds.
# Reduces notification volume ~95% on viral posts.
```

---

## 7. Comment System

### 7.1 Create Comment

```go
// POST /api/v1/posts/:post_id/comments

func (h *CommentHandler) Create(c *fiber.Ctx) error {
    userID := c.Locals("user_id").(uuid.UUID)
    postID := uuid.MustParse(c.Params("post_id"))

    // Block check, rate limit (10/min), body validation (1-2000 chars)
    // ...

    var req CreateCommentRequest
    c.BodyParser(&req)

    comment, err := h.db.Transaction(func(tx *gorm.DB) *Comment {
        cm := &Comment{
            PostID: postID, AuthorID: userID,
            Body: req.Body, IsReply: false,
        }
        tx.Create(cm)

        // Counter update (with dedup via event_log in consumer, or inline here)
        tx.Exec(`UPDATE post_engagement_counts
            SET comment_count = comment_count + 1, updated_at = now()
            WHERE post_id = $1`, postID)

        // Outbox event
        tx.Create(&OutboxEvent{
            EventType: "engagement.comment.created",
            Payload: EngagementEvent{...},
        })
        return cm
    })

    // Redis: update hot cache
    redis.HIncrBy(ctx, "post:eng:"+postID.String(), "comments", 1)
    redis.ZAdd(ctx, "post:comments:"+postID.String(), commentJSON, score)

    return c.Status(201).JSON(comment)
}
```

### 7.2 Create Reply (Post Owner Only)

```go
// POST /api/v1/comments/:comment_id/reply

func (h *CommentHandler) CreateReply(c *fiber.Ctx) error {
    userID := c.Locals("user_id").(uuid.UUID)
    commentID := uuid.MustParse(c.Params("comment_id"))

    parentComment := h.commentRepo.GetByID(ctx, commentID)
    post := h.postRepo.GetByID(ctx, parentComment.PostID)

    // === CRITICAL: Only post owner can reply ===
    if userID != post.AuthorID {
        return c.Status(403).JSON(Error{Code: "REPLY_OWNER_ONLY"})
    }

    // Max 1 reply per comment
    if h.commentRepo.GetReplyByParent(ctx, commentID) != nil {
        return c.Status(409).JSON(Error{Code: "REPLY_EXISTS"})
    }

    // Cannot reply to a reply (max depth = 2)
    if parentComment.IsReply {
        return c.Status(400).JSON(Error{Code: "CANNOT_REPLY_TO_REPLY"})
    }

    // Create reply in transaction
    // Set: ParentID = commentID, IsReply = true
    // Update parent: reply_count = 1
    // Notification to commenter (parentComment.AuthorID)
}
```

### 7.3 Comment Like

Same Lua toggle pattern as post likes. Different keys:
- `comment_liked:{user_id}:{comment_id}` (membership, 24h TTL)
- `HSET comment:eng:{comment_id} likes {count}` (counter)
- ScyllaDB: `comment_likes` table
- Notification to commenter (debounced)

Comment like counts are shown but the liker LIST is NOT shown (product decision).

### 7.4 Real-Time Comment Delivery

```
# When user opens post detail page:
ws.send({ type: "join_room", room: "post:{post_id}" })
Server: SADD ws:room:post:{post_id} {connection_id}

# On comment.created event:
members = SMEMBERS ws:room:post:{post_id}
for each member: send { type: "comment.new", data: commentPayload }

# Client handles:
#   comment.new with ParentID=nil → append to comment list
#   comment.new with ParentID set → append under parent
#   Counter update in post header
```

---

## 8. Share System

### 8.1 Share Types

| Type | Behavior | Creates New Post? |
|------|----------|------------------|
| **Repost** | Share original as-is. "Reposted by {name}" in feed. | No (share record + feed entry) |
| **Quote** | Share with commentary wrapping original. | Yes (new post with type='quote') |
| **External** | Copy link for sharing outside Postbook. | No (count + analytics only) |

### 8.2 Circle Share Rule (HARD)

**Circle-only posts CANNOT be reposted or quoted.** This would break the privacy contract. External link sharing is allowed (viewers without circle access can't see the content anyway).

```go
if post.Visibility == "circle" && req.ShareType != "external" {
    return c.Status(403).JSON(Error{Code: "CIRCLE_SHARE_RESTRICTED"})
}
```

### 8.3 Repost Idempotency

A user can only repost a post once. Check `shared:{uid}:{pid}` before allowing. Return 409 `ALREADY_SHARED` on duplicate.

---

## 9. Bookmark System

### 9.1 Privacy

Bookmarks are **completely private**. No one sees what you bookmark. Bookmark count exists in `post_engagement_counts` for internal analytics only — NEVER exposed in API responses. No notification, no WebSocket event.

### 9.2 Toggle with TTL (Fix #5)

```go
func (h *EngagementHandler) ToggleBookmark(c *fiber.Ctx) error {
    userID := c.Locals("user_id").(uuid.UUID)
    postID := uuid.MustParse(c.Params("post_id"))

    bmKey := fmt.Sprintf("bookmarked:%s:%s", userID, postID)

    if redis.Exists(ctx, bmKey).Val() == 1 {
        redis.Del(ctx, bmKey)
        // NATS: engagement.post.bookmarked (IsSet: false)
        return c.JSON(BookmarkResponse{Bookmarked: false})
    }

    redis.Set(ctx, bmKey, "1", 24*time.Hour) // 24h TTL, ScyllaDB fallback on miss
    // NATS: engagement.post.bookmarked (IsSet: true)
    return c.JSON(BookmarkResponse{Bookmarked: true})
}
```

### 9.3 Membership Check with Fallback

```go
func isBookmarked(ctx context.Context, userID, postID uuid.UUID) bool {
    bmKey := fmt.Sprintf("bookmarked:%s:%s", userID, postID)

    val, err := redis.Get(ctx, bmKey).Result()
    if err == nil { return val == "1" }

    // Cache miss → ScyllaDB
    var exists bool
    scylla.Query(`SELECT user_id FROM bookmark_check WHERE user_id = ? AND post_id = ?`,
        userID, postID).Scan(&exists)

    if exists {
        redis.Set(ctx, bmKey, "1", 24*time.Hour)
        return true
    }

    redis.Set(ctx, bmKey, "0", 1*time.Hour) // negative cache
    return false
}
```

### 9.4 Collections

- Max 20 collections per user
- Names: 1-50 chars, alphanumeric + hyphens
- Default collection: `default` (cannot be deleted)
- Move between collections: DELETE from old partition + INSERT into new partition + UPDATE bookmark_check

---

## 10. Feed Hydration (Batch Pipeline)

When loading a feed of 20 posts, hydrate engagement for ALL posts in a single Redis Pipeline (one network RTT):

```go
func HydrateEngagement(posts []Post, viewerID uuid.UUID) []PostResponse {
    pipe := redis.Pipeline()

    // Queue 5 commands × 20 posts = 100 commands
    engCmds := make([]*redis.MapStringStringCmd, len(posts))
    likedCmds := make([]*redis.IntCmd, len(posts))
    sharedCmds := make([]*redis.IntCmd, len(posts))
    bookmarkCmds := make([]*redis.IntCmd, len(posts))
    likerCmds := make([]*redis.StringSliceCmd, len(posts))

    for i, post := range posts {
        pid := post.ID.String()
        uid := viewerID.String()
        engCmds[i] = pipe.HGetAll(ctx, "post:eng:"+pid)
        likedCmds[i] = pipe.Exists(ctx, fmt.Sprintf("liked:%s:%s", uid, pid))
        sharedCmds[i] = pipe.Exists(ctx, fmt.Sprintf("shared:%s:%s", uid, pid))
        bookmarkCmds[i] = pipe.Exists(ctx, fmt.Sprintf("bookmarked:%s:%s", uid, pid))
        likerCmds[i] = pipe.LRange(ctx, "post:likers:"+pid, 0, 2)
    }

    pipe.Exec(ctx) // ONE round-trip for all 100 commands

    // Assemble responses with engagement + viewer_state + recent_likers
    // For any membership cache miss, fall back to ScyllaDB point read
}
```

### Post Response Shape

```go
type PostResponse struct {
    ID         uuid.UUID `json:"id"`
    // ... post fields ...

    Engagement struct {
        Likes    int64 `json:"likes"`
        Comments int64 `json:"comments"`
        Shares   int64 `json:"shares"`
        // NO bookmark count (private)
    } `json:"engagement"`

    ViewerState struct {
        Liked      bool `json:"liked"`
        Shared     bool `json:"shared"`
        Bookmarked bool `json:"bookmarked"`
    } `json:"viewer_state"`

    RecentLikers []MiniProfile `json:"recent_likers"` // max 3
}
```

---

## 11. Counter Reconciliation

### 11.1 Schedule

Every 5 minutes for "hot" posts (posts in the `hot:posts` Redis set — any post with engagement in the last hour).

### 11.2 Authority

| Engagement Type | Authority Source | Method |
|----------------|-----------------|--------|
| Likes | ScyllaDB `engagement_counters` | Direct read |
| Shares | ScyllaDB `engagement_counters` | Direct read |
| Bookmarks | ScyllaDB `engagement_counters` | Direct read |
| Comments | PostgreSQL `COUNT(*)` | Query with `is_deleted = FALSE` |

### 11.3 Worker Logic

```go
func (w *ReconciliationWorker) Run(ctx context.Context) {
    hotPosts := redis.SMembers(ctx, "hot:posts").Val()

    for _, postID := range hotPosts {
        // Read Scylla truth for likes/shares
        scyllaLikes := scylla.Query(`SELECT count FROM engagement_counters
            WHERE target_type = 'post' AND target_id = ? AND counter_type = 'likes'`, postID).Scan()
        scyllaShares := scylla.Query(`... counter_type = 'shares'`).Scan()

        // Read PG truth for comments
        pgComments := pg.QueryRow(`SELECT COUNT(*) FROM comments
            WHERE post_id = $1 AND is_deleted = FALSE`, postID).Scan()

        // Correct Redis
        redisEng := redis.HGetAll(ctx, "post:eng:"+postID).Val()
        if drift detected {
            redis.HSet(ctx, "post:eng:"+postID, corrected values)
            metrics.CounterDrift.Observe(drift)
        }

        // Sync PG analytics counters from authoritative sources
        pg.Exec(`UPDATE post_engagement_counts SET
            like_count = $2, share_count = $3, comment_count = $4
            WHERE post_id = $1`, postID, scyllaLikes, scyllaShares, pgComments)
    }
}
```

### 11.4 ScyllaDB Counter LWT Update

```go
func incrementCounter(targetType, targetID, counterType string, delta int) error {
    for retries := 0; retries < 3; retries++ {
        var current int64
        scylla.Query(`SELECT count FROM engagement_counters
            WHERE target_type = ? AND target_id = ? AND counter_type = ?`,
            targetType, targetID, counterType).Scan(&current)

        newCount := max(current + int64(delta), 0)

        applied := scylla.Query(`
            UPDATE engagement_counters
            SET count = ?, updated_at = toTimestamp(now())
            WHERE target_type = ? AND target_id = ? AND counter_type = ?
            IF count = ?
        `, newCount, targetType, targetID, counterType, current).ScanCAS()

        if applied { return nil }
        // CAS failed (concurrent update), retry with fresh read
    }
    return fmt.Errorf("counter update failed after 3 retries")
}
```

---

## 12. Real-Time WebSocket Delivery

| Context | Strategy | Latency | Reason |
|---------|----------|---------|--------|
| **Post detail page** | WebSocket room, instant push per event | < 200ms | User actively watching; live comments critical |
| **Feed scrolling** | Batched WebSocket every 10 seconds | < 10s | Counter precision not critical while scrolling |
| **Background tab** | No updates | N/A | Don't waste resources |

### Feed-Level Batching

```
// Client subscribes to feed updates:
ws.send({ type: "subscribe_feed" })

// Server batches counter changes every 10 seconds:
{ type: "feed.engagement_batch",
  updates: [
    { post_id: "abc", likes: 235, comments: 19 },
    { post_id: "def", likes: 90, comments: 43 },
  ]
}
```

---

## 13. Event Catalog

| Event | NATS Subject | Consumers | Notification? | WebSocket? |
|-------|-------------|-----------|--------------|------------|
| `engagement.post.liked` | `eng.post.liked` | ScyllaDB, PG, Notification, WS, Feed Ranker | Yes (debounced 30s) | Yes (count) |
| `engagement.post.unliked` | `eng.post.unliked` | ScyllaDB, PG, WS | No | Yes (count) |
| `engagement.comment.created` | `eng.comment.created` | PG Counter, Notification, WS, Mention Parser | Yes (immediate) | Yes (full comment) |
| `engagement.comment.deleted` | `eng.comment.deleted` | PG Counter, WS | No | Yes (remove) |
| `engagement.comment.liked` | `eng.comment.liked` | ScyllaDB, PG, Notification | Yes (debounced) | No |
| `engagement.reply.created` | `eng.reply.created` | Notification, WS | Yes (to commenter) | Yes (append reply) |
| `engagement.post.shared` | `eng.post.shared` | ScyllaDB, PG, Notification, Feed (repost) | Yes | Yes (count) |
| `engagement.post.bookmarked` | `eng.post.bookmarked` | ScyllaDB only | **NEVER** | **NEVER** |

---

## 14. Anti-Abuse & Rate Limiting

### 14.1 Rate Limits

| Action | Limit | Window |
|--------|-------|--------|
| Like toggle | 120/hour | Sliding window |
| Comment create | 10/minute | Sliding window |
| Share | 30/hour | Sliding window |
| Bookmark toggle | 200/hour | Sliding window |
| Reply create | 20/hour | Sliding window |
| Comment like | 120/hour | Sliding window |

### 14.2 Abuse Rules

- **Self-engagement**: Cannot like/share own posts. Return 400 `SELF_ENGAGEMENT`.
- **Rapid-fire likes**: > 20 posts in 60 seconds → flag for review.
- **Duplicate comments**: Same text on 3+ posts in 1 hour → auto-flag as spam.
- **Ghost engagement**: Engagement on deleted/hidden posts → silent no-op (200 OK).
- **Block enforcement**: All endpoints check `BlockChecker` first. Blocked users get 404 (not 403).

### 14.3 Rate Limiter (Redis Sorted Set)

```go
func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) bool {
    now := time.Now().UnixMilli()
    windowStart := now - window.Milliseconds()

    result := rl.redis.Eval(ctx, `
        local key = KEYS[1]
        redis.call('ZREMRANGEBYSCORE', key, '-inf', ARGV[2])
        local count = redis.call('ZCARD', key)
        if count < tonumber(ARGV[3]) then
            redis.call('ZADD', key, ARGV[1], ARGV[1] .. ':' .. math.random(100000))
            redis.call('EXPIRE', key, math.ceil(tonumber(ARGV[4]) / 1000))
            return 1
        end
        return 0
    `, []string{key}, now, windowStart, limit, window.Milliseconds())

    return result.(int64) == 1
}
```

---

## 15. Frontend Integration

### 15.1 Optimistic Like Toggle (TanStack Query)

```tsx
function useToggleLike(postId: string) {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: () => api.post(`/posts/${postId}/like`),

        onMutate: async () => {
            await queryClient.cancelQueries({ queryKey: ['feed'] });
            const prev = queryClient.getQueryData(['feed']);

            queryClient.setQueryData(['feed'], (old) =>
                old.map(post =>
                    post.id === postId ? {
                        ...post,
                        engagement: {
                            ...post.engagement,
                            likes: post.viewer_state.liked
                                ? post.engagement.likes - 1
                                : post.engagement.likes + 1,
                        },
                        viewer_state: { ...post.viewer_state, liked: !post.viewer_state.liked },
                    } : post
                )
            );
            return { prev };
        },

        onError: (err, _, context) => {
            queryClient.setQueryData(['feed'], context.prev); // rollback
        },

        onSettled: (data) => {
            // Server truth: { liked: bool, count: number }
            queryClient.setQueryData(['feed'], (old) =>
                old.map(post =>
                    post.id === postId ? {
                        ...post,
                        engagement: { ...post.engagement, likes: data.count },
                        viewer_state: { ...post.viewer_state, liked: data.liked },
                    } : post
                )
            );
        },
    });
}
```

### 15.2 WebSocket Live Counters (Zustand)

```tsx
const useEngagementStore = create((set, get) => ({
    liveCounters: {}, // { [postId]: { likes, comments, shares } }

    handleWSEvent: (event) => {
        if (event.type === 'engagement.count') {
            set(state => ({
                liveCounters: { ...state.liveCounters, [event.post_id]: event.counts }
            }));
        }
        if (event.type === 'feed.engagement_batch') {
            set(state => ({
                liveCounters: {
                    ...state.liveCounters,
                    ...Object.fromEntries(event.updates.map(u => [u.post_id, u]))
                }
            }));
        }
    },

    getCount: (postId, field) => {
        const live = get().liveCounters[postId];
        return live ? live[field] : null; // null = use server value
    },
}));

// In PostCard: const likeCount = liveCount ?? post.engagement.likes;
```

---

## 16. SLOs & Observability

### 16.1 SLO Targets

| Metric | Target | Alert Threshold |
|--------|--------|----------------|
| Like toggle p99 | < 20ms | > 50ms |
| Comment create p99 | < 100ms | > 250ms |
| Feed hydration p99 (20 posts) | < 5ms | > 15ms |
| Counter reconciliation drift | < 0.5% | > 2% |
| WebSocket delivery p99 | < 200ms | > 500ms |
| NATS consumer lag | < 1000 msgs | > 5000 msgs |
| Like dedup accuracy | > 99.9% | < 99.5% |
| Comment notification delivery | > 99.5% | < 99% |

### 16.2 Tracked Metrics

- `eng_action_total{type, action}` — Counter per engagement type
- `eng_latency_seconds{type, path}` — Histogram: Redis vs full API
- `eng_counter_drift{type}` — Gauge: Redis vs authority
- `eng_nats_consumer_lag{consumer}` — Gauge per consumer
- `eng_reconciliation_corrections_total` — Counter per reconciliation run
- `eng_rate_limit_hits_total{action}` — Rate limit rejections
- `eng_scylla_lwt_retries_total` — Counter LWT contention

---

## 17. Implementation Checklist

### Phase 0: Schemas

| # | Task |
|---|------|
| 0.1 | Create ScyllaDB tables: post_likes (sharded), user_likes, comment_likes, post_shares (sharded), user_shares, user_bookmarks (collection PK), bookmark_check |
| 0.2 | Create ScyllaDB engagement_counters table |
| 0.3 | Create ScyllaDB comment_timeline table (optional at launch) |
| 0.4 | Create PostgreSQL comments table with indexes |
| 0.5 | Create PostgreSQL post_engagement_counts with auto-create trigger |
| 0.6 | Create PostgreSQL engagement_event_log for consumer dedup |
| 0.7 | Configure NATS JetStream stream: ENGAGEMENT with subjects `eng.*` |

### Phase 1: Event Infrastructure

| # | Task |
|---|------|
| 1.1 | Define EngagementEvent struct with event_id, action_ts, user_seq_no, is_set |
| 1.2 | Implement Lua toggle script (like) with seq + timestamp |
| 1.3 | Implement BaseConsumer with isDuplicate (Redis SETNX) |
| 1.4 | Implement Redis sliding-window rate limiter |

### Phase 2: Like System

| # | Task |
|---|------|
| 2.1 | Implement POST /api/v1/posts/{id}/like handler |
| 2.2 | Implement ScyllaDB Writer consumer (independent writes, USING TIMESTAMP, sharded) |
| 2.3 | Implement PG Counter consumer (transaction: event_log dedup + counter update) |
| 2.4 | Implement engagement_counters LWT increment |
| 2.5 | Implement notification debouncing (Redis ZSET buffer, 30s batch) |
| 2.6 | Implement WebSocket count broadcast |

### Phase 3: Comment System

| # | Task |
|---|------|
| 3.1 | Implement POST /api/v1/posts/{id}/comments (create) |
| 3.2 | Implement POST /api/v1/comments/{id}/reply (post owner check) |
| 3.3 | Implement GET /api/v1/posts/{id}/comments (paginated with threading) |
| 3.4 | Implement DELETE /api/v1/comments/{id} (soft delete) |
| 3.5 | Implement PATCH /api/v1/comments/{id} (edit within 15 min) |
| 3.6 | Implement POST /api/v1/comments/{id}/like (toggle) |
| 3.7 | Implement WebSocket room for real-time comment push |

### Phase 4: Share System

| # | Task |
|---|------|
| 4.1 | Implement POST /api/v1/posts/{id}/share (3 types) |
| 4.2 | Enforce circle-only share restriction |
| 4.3 | Implement repost idempotency check |

### Phase 5: Bookmark System

| # | Task |
|---|------|
| 5.1 | Implement POST /api/v1/posts/{id}/bookmark (toggle with 24h TTL) |
| 5.2 | Implement ScyllaDB fallback on cache miss |
| 5.3 | Implement GET /api/v1/me/bookmarks (collection filtering) |
| 5.4 | Implement collection management (list, move, create) |

### Phase 6: Feed Hydration

| # | Task |
|---|------|
| 6.1 | Implement batch pipeline hydration (5 commands × N posts, 1 RTT) |
| 6.2 | Implement PostResponse with engagement + viewer_state + recent_likers |
| 6.3 | Implement ScyllaDB fallback for membership cache misses in pipeline |

### Phase 7: Reconciliation & Observability

| # | Task |
|---|------|
| 7.1 | Implement reconciliation worker (Scylla authority for likes/shares, PG for comments) |
| 7.2 | Implement hot post set tracking (SADD on every engagement) |
| 7.3 | Implement hourly cleanup for engagement_event_log > 48h |
| 7.4 | Implement all SLO metrics |

### Phase 8: Frontend

| # | Task |
|---|------|
| 8.1 | Implement optimistic like/bookmark/share toggles (TanStack Query) |
| 8.2 | Implement Zustand store for WebSocket live counter merging |
| 8.3 | Implement WebSocket room join/leave for post detail pages |
| 8.4 | Implement comment thread UI with real-time insertion |
| 8.5 | Implement reply UI (only visible to post owner as action) |

---

## 18. Anti-Patterns (DO NOT)

1. **DO NOT use cross-partition BATCH in ScyllaDB.** Two independent idempotent writes with USING TIMESTAMP.
2. **DO NOT increment counters without dedup.** Every consumer uses event_log (PG) or SETNX (Redis).
3. **DO NOT use COUNT(*) for large Scylla partitions.** Use the engagement_counters table.
4. **DO NOT use ALLOW FILTERING in ScyllaDB.** Put filter columns in the PRIMARY KEY.
5. **DO NOT store unbounded data in Redis without TTL.** All membership keys get 24h TTL with Scylla fallback.
6. **DO NOT use Scylla native counters.** Regular tables with LWT for correctness.
7. **DO NOT assume NATS event ordering.** All consumers must be idempotent and order-tolerant. Use USING TIMESTAMP in Scylla, event_log in PG.
8. **DO NOT publish NATS event BEFORE Lua script completes.** ActionTS comes from the Lua script. Lua first, then publish.
9. **DO NOT send notifications on bookmark.** Bookmarks are completely private. No notification, no WebSocket, no public count.
10. **DO NOT allow non-post-owners to reply.** Hard 403 check. Only the post author can reply to comments.
11. **DO NOT allow repost/quote of circle-only posts.** Hard 403. Privacy contract.
12. **DO NOT allow self-engagement.** Cannot like/share own posts. 400 SELF_ENGAGEMENT.
