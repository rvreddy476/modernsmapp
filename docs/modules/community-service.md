# Module: community-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:communityId
DELETE /:communityId/members/:userId/ban
DELETE /:communityId/posts/:postId
DELETE /:communityId/spaces/:spaceId
GET /:communityId
GET /:communityId/events
GET /:communityId/featured
GET /:communityId/join-requests
GET /:communityId/members
GET /:communityId/modlog
GET /:communityId/posts
GET /:communityId/posts/:postId
GET /:communityId/spaces
GET /:communityId/wiki
GET /:communityId/wiki/:slug
GET /discover
GET /my
POST /:communityId/events
POST /:communityId/join
POST /:communityId/join-requests/:requestId/approve
POST /:communityId/join-requests/:requestId/reject
POST /:communityId/leave
POST /:communityId/members/:userId/ban
POST /:communityId/posts/:postId/accept-answer
POST /:communityId/posts/:postId/feature
POST /:communityId/posts/:postId/pin
POST /:communityId/posts/:postId/spark
POST /:communityId/posts/:postId/stash
POST /:communityId/posts/:postId/view
POST /:communityId/spaces
POST /:communityId/spaces/:spaceId/posts
POST /:communityId/spaces/:spaceId/quarantine
POST /:communityId/wiki
PUT /:communityId
PUT /:communityId/members/:userId/role
PUT /:communityId/spaces/:spaceId
PUT /:communityId/wiki/:slug
GROUP /v1/communities
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS communities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    handle TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    banner_media_id UUID,
    community_type TEXT NOT NULL DEFAULT 'public' CHECK (community_type IN ('public','private','invite','education','local','professional','fan','brand')),
    category TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    join_mode TEXT NOT NULL DEFAULT 'open' CHECK (join_mode IN ('open','request','invite_only','email_domain')),
    email_domain_gate TEXT,
    join_questions JSONB DEFAULT '[]',
    member_directory BOOLEAN NOT NULL DEFAULT true,
    cross_space_bans BOOLEAN NOT NULL DEFAULT true,
    max_sub_spaces INT NOT NULL DEFAULT 50,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    location_name TEXT NOT NULL DEFAULT '',
    rules TEXT[] NOT NULL DEFAULT '{}',
    topic_tags TEXT[] NOT NULL DEFAULT '{}',
    member_count BIGINT NOT NULL DEFAULT 0,
    space_count INT NOT NULL DEFAULT 0,
    is_verified BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','archived','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS community_members (
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','moderator','space_manager','expert','member','pending','suspended','banned')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    banned_at TIMESTAMPTZ,
    banned_by UUID,
    ban_reason TEXT,
    PRIMARY KEY (community_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_spaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    space_type TEXT NOT NULL CHECK (space_type IN ('group','channel','discussion','events','resources')),
    linked_group_id UUID,
    linked_channel_id UUID,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    sort_order INT NOT NULL DEFAULT 0,
    is_quarantined BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS community_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    answers JSONB,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(community_id, user_id, status)
);

CREATE TABLE IF NOT EXISTS community_modlog (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id UUID NOT NULL,
    reason TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS community_announcements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS community_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    space_id UUID REFERENCES community_spaces(id) ON DELETE SET NULL,
    creator_id UUID NOT NULL,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    location TEXT,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    max_attendees INT DEFAULT 0,
    rsvp_count INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS community_posts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    space_id        UUID NOT NULL,
    author_id       TEXT NOT NULL,
    content_type    VARCHAR(20) NOT NULL DEFAULT 'text',
    title           VARCHAR(300),
    body            TEXT,
    body_html       TEXT,
    type_payload    JSONB NOT NULL DEFAULT '{}',
    attachments     JSONB NOT NULL DEFAULT '[]',
    tags            TEXT[] DEFAULT '{}',
    parent_post_id  UUID,
    thread_depth    INTEGER NOT NULL DEFAULT 0,
    reply_count     INTEGER NOT NULL DEFAULT 0,
    needs_approval  BOOLEAN NOT NULL DEFAULT FALSE,
    approved_by     TEXT,
    is_pinned       BOOLEAN NOT NULL DEFAULT FALSE,
    is_announcement BOOLEAN NOT NULL DEFAULT FALSE,
    is_featured     BOOLEAN NOT NULL DEFAULT FALSE,
    is_answered     BOOLEAN NOT NULL DEFAULT FALSE,
    accepted_answer_id UUID,
    is_expert_answer BOOLEAN NOT NULL DEFAULT FALSE,
    status          VARCHAR(20) NOT NULL DEFAULT 'published',
    spark_count     INTEGER NOT NULL DEFAULT 0,
    comment_count   INTEGER NOT NULL DEFAULT 0,
    echo_count      INTEGER NOT NULL DEFAULT 0,
    view_count      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS community_wiki_pages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    title           TEXT NOT NULL,
    slug            VARCHAR(200) NOT NULL,
    content         TEXT NOT NULL,
    content_html    TEXT,
    category        VARCHAR(50),
    is_pinned       BOOLEAN NOT NULL DEFAULT FALSE,
    created_by      TEXT NOT NULL,
    updated_by      TEXT,
    version         INTEGER NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(community_id, slug)
);

CREATE TABLE IF NOT EXISTS community_bans (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    user_id         TEXT NOT NULL,
    banned_by       TEXT NOT NULL,
    reason          TEXT,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(community_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_reports (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    reporter_id     TEXT NOT NULL,
    target_type     VARCHAR(20) NOT NULL,
    target_id       UUID NOT NULL,
    reason          VARCHAR(50) NOT NULL,
    description     TEXT,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    reviewed_by     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS community_post_sparks (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, is_supernova BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_post_stashes (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_post_views (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, viewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_space_follows (
    community_id    UUID NOT NULL,
    space_id        UUID NOT NULL,
    user_id         TEXT NOT NULL,
    followed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (community_id, space_id, user_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type CreateEventRequest struct {
	Title        string  `json:"title" binding:"required"`
	Description  string  `json:"description"`
	Location     *string `json:"location"`
	StartsAt     string  `json:"starts_at" binding:"required"`
	EndsAt       *string `json:"ends_at"`
	IsOnline     bool    `json:"is_online"`
	MaxAttendees int     `json:"max_attendees"`
}

type CreateCommunityRequest struct {
	Handle          string          `json:"handle" binding:"required"`
	Name            string          `json:"name" binding:"required"`
	Description     string          `json:"description"`
	AvatarMediaID   *uuid.UUID      `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID      `json:"banner_media_id"`
	CommunityType   string          `json:"community_type"`
	Category        string          `json:"category"`
	Language        string          `json:"language"`
	JoinMode        string          `json:"join_mode"`
	EmailDomainGate *string         `json:"email_domain_gate"`
	JoinQuestions   json.RawMessage `json:"join_questions"`
	MemberDirectory *bool           `json:"member_directory"`
	CrossSpaceBans  *bool           `json:"cross_space_bans"`
	MaxSubSpaces    *int            `json:"max_sub_spaces"`
	Latitude        *float64        `json:"latitude"`
	Longitude       *float64        `json:"longitude"`
	LocationName    string          `json:"location_name"`
	Rules           []string        `json:"rules"`
	TopicTags       []string        `json:"topic_tags"`
}

type UpdateCommunityRequest struct {
	Name            *string          `json:"name"`
	Description     *string          `json:"description"`
	AvatarMediaID   *uuid.UUID       `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID       `json:"banner_media_id"`
	CommunityType   *string          `json:"community_type"`
	Category        *string          `json:"category"`
	Language        *string          `json:"language"`
	JoinMode        *string          `json:"join_mode"`
	EmailDomainGate *string          `json:"email_domain_gate"`
	JoinQuestions   json.RawMessage  `json:"join_questions"`
	MemberDirectory *bool            `json:"member_directory"`
	CrossSpaceBans  *bool            `json:"cross_space_bans"`
	MaxSubSpaces    *int             `json:"max_sub_spaces"`
	Latitude        *float64         `json:"latitude"`
	Longitude       *float64         `json:"longitude"`
	LocationName    *string          `json:"location_name"`
	Rules           []string         `json:"rules"`
	TopicTags       []string         `json:"topic_tags"`
}

type JoinCommunityRequest struct {
	Answers json.RawMessage `json:"answers"`
}

type CreateSpaceRequest struct {
	SpaceType       string     `json:"space_type"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
	Name            string     `json:"name" binding:"required"`
	Description     string     `json:"description"`
	SortOrder       int        `json:"sort_order"`
}

type UpdateSpaceRequest struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	SortOrder       *int       `json:"sort_order"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
}

type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type BanMemberRequest struct {
	Reason string `json:"reason"`
}

type QuarantineSpaceRequest struct {
	Reason string `json:"reason"`
}
```
