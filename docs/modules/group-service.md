# Module: group-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:groupId
DELETE /:groupId/channels/:channelId
DELETE /:groupId/events/:eventId
DELETE /:groupId/members/:userId
DELETE /:groupId/members/:userId/ban
DELETE /:groupId/posts/:postId
DELETE /:groupId/posts/:postId/pin
DELETE /:groupId/posts/v2/:postId
DELETE /:groupId/posts/v2/:postId/comments/:commentId
DELETE /:groupId/posts/v2/:postId/echo
DELETE /:groupId/posts/v2/:postId/spark
DELETE /:groupId/posts/v2/:postId/stash
DELETE /:groupId/wiki/:pageId
DELETE /:groupId/word-blocklist/:word
GET /by-handle/:handle
GET /discover
GET /feed
GET /:groupId
GET /:groupId/approval-queue
GET /:groupId/channels
GET /:groupId/events
GET /:groupId/events/:eventId
GET /:groupId/feed
GET /:groupId/feed/v2
GET /:groupId/invites
GET /:groupId/join-requests
GET /:groupId/media
GET /:groupId/members
GET /:groupId/members/banned
GET /:groupId/members/:userId/stats
GET /:groupId/posts/v2/:postId
GET /:groupId/posts/v2/:postId/comments
GET /:groupId/rules
GET /:groupId/stats/contributors
GET /:groupId/wiki
GET /:groupId/word-blocklist
GET /invites/my
GET /my
GET /search
POST /:groupId/approval-queue/:itemId/approve
POST /:groupId/approval-queue/:itemId/reject
POST /:groupId/archive
POST /:groupId/channels
POST /:groupId/events
POST /:groupId/events/:eventId/rsvp
POST /:groupId/invite
POST /:groupId/join
POST /:groupId/join-requests
POST /:groupId/join-requests/:requestId/approve
POST /:groupId/join-requests/:requestId/reject
POST /:groupId/leave
POST /:groupId/members/:userId/ban
POST /:groupId/posts
POST /:groupId/posts/v2
POST /:groupId/posts/v2/:postId/comments
POST /:groupId/posts/v2/:postId/echo
POST /:groupId/posts/v2/:postId/spark
POST /:groupId/posts/v2/:postId/stash
POST /:groupId/posts/v2/:postId/view
POST /:groupId/wiki
POST /:groupId/word-blocklist
POST /handle/check
POST /invites/:inviteId/accept
POST /invites/:inviteId/reject
PUT /:groupId
PUT /:groupId/members/:userId/role
PUT /:groupId/posts/:postId/pin
PUT /:groupId/rules
PUT /:groupId/wiki/:pageId
GROUP /v1/groups
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    cover_media_id UUID,
    creator_id UUID NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    chat_conversation_id UUID,
    member_count BIGINT DEFAULT 0,
    post_count BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'moderator', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    inviter_id UUID NOT NULL,
    invitee_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(group_id, invitee_id)
);

CREATE TABLE IF NOT EXISTS group_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    post_id UUID,
    channel_id UUID,
    author_id TEXT NOT NULL,
    content_type VARCHAR(20) DEFAULT 'text',
    title VARCHAR(200),
    body TEXT,
    body_html TEXT,
    type_payload JSONB DEFAULT '{}',
    attachments JSONB DEFAULT '[]',
    needs_approval BOOLEAN DEFAULT FALSE,
    approved_by TEXT,
    approved_at TIMESTAMPTZ,
    is_pinned BOOLEAN DEFAULT FALSE,
    is_announcement BOOLEAN DEFAULT FALSE,
    status VARCHAR(20) DEFAULT 'published',
    spark_count INTEGER DEFAULT 0,
    comment_count INTEGER DEFAULT 0,
    echo_count INTEGER DEFAULT 0,
    view_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_post_sparks (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, is_supernova BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL, user_id TEXT NOT NULL, body TEXT NOT NULL,
    parent_id UUID, is_pinned BOOLEAN DEFAULT FALSE, spark_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS group_post_stashes (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_views (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, viewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_echoes (
    id UUID PRIMARY KEY,
    post_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    echo_type VARCHAR(20) NOT NULL DEFAULT 'share',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, post_id UUID, creator_id TEXT NOT NULL,
    title VARCHAR(200) NOT NULL, description TEXT, cover_media_id UUID,
    start_at TIMESTAMPTZ NOT NULL, end_at TIMESTAMPTZ,
    timezone VARCHAR(50) DEFAULT 'UTC', is_all_day BOOLEAN DEFAULT FALSE,
    location_type VARCHAR(20) DEFAULT 'online', address TEXT, online_link TEXT,
    rsvp_enabled BOOLEAN DEFAULT TRUE, max_attendees INTEGER DEFAULT 0,
    going_count INTEGER DEFAULT 0, maybe_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'upcoming', created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_event_rsvps (
    event_id UUID NOT NULL, user_id TEXT NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('going', 'maybe', 'not_going')),
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (event_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, name VARCHAR(100) NOT NULL,
    type VARCHAR(20) DEFAULT 'discussion', description TEXT DEFAULT '',
    who_can_post VARCHAR(20) DEFAULT 'all_members',
    is_default BOOLEAN DEFAULT FALSE, is_archived BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0, post_count BIGINT DEFAULT 0,
    created_by TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_wiki_pages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, title VARCHAR(200) NOT NULL, content TEXT NOT NULL,
    created_by TEXT NOT NULL, updated_by TEXT,
    version INTEGER DEFAULT 1, is_pinned BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_bans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, user_id TEXT NOT NULL, banned_by TEXT NOT NULL,
    reason TEXT, expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(), UNIQUE(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS post_approval_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, post_id UUID NOT NULL, author_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', reviewed_by TEXT, reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS member_stats (
    group_id UUID NOT NULL, user_id UUID NOT NULL,
    post_count INTEGER DEFAULT 0, sparks_received INTEGER DEFAULT 0,
    updated_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by_user_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS group_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    rule_order INT NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_member_stats (
    group_id        UUID NOT NULL,
    user_id         UUID NOT NULL,
    post_count      INT DEFAULT 0,
    sparks_received INT DEFAULT 0,
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_word_blocklist (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    word     TEXT NOT NULL,
    added_by UUID NOT NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, word)
);

CREATE TABLE IF NOT EXISTS post_approval_queue (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL,
    author_id   UUID NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_channels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('discussion','announcements','qa','resources','voice')),
    description TEXT NOT NULL DEFAULT '',
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order  INT NOT NULL DEFAULT 0,
    post_count  BIGINT NOT NULL DEFAULT 0,
    created_by  UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_channel_posts (
    channel_id UUID NOT NULL REFERENCES group_channels(id) ON DELETE CASCADE,
    post_id    UUID NOT NULL,
    author_id  UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, post_id)
);

CREATE TABLE IF NOT EXISTS group_wiki_pages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_by UUID NOT NULL,
    updated_by UUID,
    version    INT NOT NULL DEFAULT 1,
    is_pinned  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_resources (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('link','file','document')),
    url        TEXT NOT NULL,
    media_id   UUID,
    added_by   UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    channel_id UUID,
    author_id TEXT NOT NULL,
    content_type VARCHAR(20) DEFAULT 'text',
    title VARCHAR(200),
    body TEXT,
    body_html TEXT,
    type_payload JSONB DEFAULT '{}',
    attachments JSONB DEFAULT '[]',
    needs_approval BOOLEAN DEFAULT FALSE,
    approved_by TEXT,
    approved_at TIMESTAMPTZ,
    is_pinned BOOLEAN DEFAULT FALSE,
    is_announcement BOOLEAN DEFAULT FALSE,
    status VARCHAR(20) DEFAULT 'published',
    spark_count INTEGER DEFAULT 0,
    comment_count INTEGER DEFAULT 0,
    echo_count INTEGER DEFAULT 0,
    view_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) DEFAULT 'discussion',
    description TEXT DEFAULT '',
    who_can_post VARCHAR(20) DEFAULT 'all_members',
    is_default BOOLEAN DEFAULT FALSE,
    is_archived BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0,
    post_count BIGINT DEFAULT 0,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    post_id UUID,
    creator_id TEXT NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT,
    cover_media_id UUID,
    start_at TIMESTAMPTZ NOT NULL,
    end_at TIMESTAMPTZ,
    timezone VARCHAR(50) DEFAULT 'UTC',
    is_all_day BOOLEAN DEFAULT FALSE,
    location_type VARCHAR(20) DEFAULT 'online',
    address TEXT,
    online_link TEXT,
    rsvp_enabled BOOLEAN DEFAULT TRUE,
    max_attendees INTEGER DEFAULT 0,
    going_count INTEGER DEFAULT 0,
    maybe_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'upcoming',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_bans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    banned_by TEXT NOT NULL,
    reason TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    reporter_id TEXT NOT NULL,
    target_type VARCHAR(20) NOT NULL,
    target_id UUID NOT NULL,
    reason VARCHAR(50) NOT NULL,
    description TEXT,
    status VARCHAR(20) DEFAULT 'pending',
    reviewed_by TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    inviter_id TEXT NOT NULL,
    invitee_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    message VARCHAR(200),
    expires_at TIMESTAMPTZ DEFAULT NOW() + INTERVAL '7 days',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(group_id, invitee_id)
);

CREATE TABLE IF NOT EXISTS group_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    answers JSONB DEFAULT '[]',
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_sparks (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, is_supernova BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL, user_id TEXT NOT NULL, body TEXT NOT NULL,
    parent_id UUID, is_pinned BOOLEAN DEFAULT FALSE, spark_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS group_post_stashes (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_views (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, viewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_event_rsvps (
    event_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('going', 'maybe', 'not_going')),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (event_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_sparks (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, is_supernova BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL, user_id TEXT NOT NULL, body TEXT NOT NULL,
    parent_id UUID, is_pinned BOOLEAN DEFAULT FALSE, spark_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS group_post_stashes (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_views (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, viewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, post_id UUID, creator_id TEXT NOT NULL,
    title VARCHAR(200) NOT NULL, description TEXT, cover_media_id UUID,
    start_at TIMESTAMPTZ NOT NULL, end_at TIMESTAMPTZ,
    timezone VARCHAR(50) DEFAULT 'UTC', is_all_day BOOLEAN DEFAULT FALSE,
    location_type VARCHAR(20) DEFAULT 'online', address TEXT, online_link TEXT,
    rsvp_enabled BOOLEAN DEFAULT TRUE, max_attendees INTEGER DEFAULT 0,
    going_count INTEGER DEFAULT 0, maybe_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'upcoming', created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_event_rsvps (
    event_id UUID NOT NULL, user_id TEXT NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('going', 'maybe', 'not_going')),
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (event_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, name VARCHAR(100) NOT NULL,
    type VARCHAR(20) DEFAULT 'discussion', description TEXT DEFAULT '',
    who_can_post VARCHAR(20) DEFAULT 'all_members',
    is_default BOOLEAN DEFAULT FALSE, is_archived BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0, post_count BIGINT DEFAULT 0,
    created_by TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_wiki_pages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, title VARCHAR(200) NOT NULL, content TEXT NOT NULL,
    created_by TEXT NOT NULL, updated_by TEXT,
    version INTEGER DEFAULT 1, is_pinned BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_bans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, user_id TEXT NOT NULL, banned_by TEXT NOT NULL,
    reason TEXT, expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(), UNIQUE(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS post_approval_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, post_id UUID NOT NULL, author_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', reviewed_by TEXT, reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS member_stats (
    group_id UUID NOT NULL, user_id UUID NOT NULL,
    post_count INTEGER DEFAULT 0, sparks_received INTEGER DEFAULT 0,
    updated_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (group_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_post_echoes (
    id UUID PRIMARY KEY,
    post_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    echo_type VARCHAR(20) NOT NULL DEFAULT 'share',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(post_id, user_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type CreateGroupRequest struct {
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description"`
	Visibility     string `json:"visibility"`
	Handle         string `json:"handle"`
	Category       string `json:"category"`
	PrivacyLevel   string `json:"privacy_level"`
	JoinMode       string `json:"join_mode"`
	WhoCanPost     string `json:"who_can_post"`
	WhoCanInvite   string `json:"who_can_invite"`
	Location       string `json:"location"`
	Language       string `json:"language"`
	IdempotencyKey string `json:"idempotency_key"`
	// GCC Phase 1 fields
	GroupType         string          `json:"group_type"`
	MaxMembers        int             `json:"max_members"`
	JoinQuestions     json.RawMessage `json:"join_questions"`
	TopicTags         []string        `json:"topic_tags"`
	CommentPermission string          `json:"comment_permission"`
	MemberListVisible *bool           `json:"member_list_visible"`
	LinkSharing       *bool           `json:"link_sharing"`
	IsMature          bool            `json:"is_mature"`
}

type UpdateGroupRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Visibility    *string `json:"visibility"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	// GCC Phase 1 fields
	GroupType         *string          `json:"group_type"`
	MaxMembers        *int             `json:"max_members"`
	JoinQuestions     json.RawMessage  `json:"join_questions"`
	TopicTags         []string         `json:"topic_tags"`
	CommentPermission *string          `json:"comment_permission"`
	MemberListVisible *bool            `json:"member_list_visible"`
	LinkSharing       *bool            `json:"link_sharing"`
}

type InviteRequest struct {
	UserID  string   `json:"user_id"`
	UserIDs []string `json:"user_ids"`
}

type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type CheckHandleRequest struct {
	Handle string `json:"handle" binding:"required"`
}

type BanRequest struct {
	Reason string `json:"reason"`
}

type UpdateRulesRequest struct {
	Rules []RuleItem `json:"rules" binding:"required"`
}

type RuleItem struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
}
```
