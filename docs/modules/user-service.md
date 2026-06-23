# Module: user-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:id
DELETE /:id/follow
DELETE /me/about/:section/:itemId
DELETE /me/pins/:contentType/:contentId
DELETE /me/portfolio/:id
DELETE /subscribe
GET /by-username/:username
GET /dlq
GET /:id
GET /:id/documents
GET /:id/reviews
GET /me
GET /me/links/analytics
GET /me/qr
GET /me/screen-time
GET /me/settings
GET /me/wellbeing
GET /projection/health
GET /subscribers
GET /subscription
GET /:userId
GET /:userId/about
GET /:userId/about/:section
GET /:userId/channels
GET /:userId/compatibility
GET /:userId/endorsements
GET /:userId/links
GET /:userId/online
GET /:userId/pins
GET /:userId/portfolio
GET /:userId/reputation
GET /:userId/subscriptions
PATCH /:id
PATCH /me/portfolio/:id
PATCH /me/status
POST /dlq/:id/replay
POST /:id/approve
POST /:id/disable
POST /:id/documents
POST /:id/documents/:docId/:action
POST /:id/follow
POST /:id/reject
POST /:id/reviews
POST /:id/submit-review
POST /:id/suspend
POST /links/:platform/click
POST /me/heartbeat
POST /me/pins
POST /me/portfolio
POST /me/screen-time
POST /online/batch
POST /subscribe
POST /:userId/endorse
POST /:userId/qr/scan
POST /users/:userId/ensure
POST /v1/onboarding/ensure-publisher
PUT /me
PUT /me/about/:section
PUT /me/links
PUT /me/settings
PUT /me/wellbeing
GROUP /internal
GROUP /v1/channels/:id
GROUP /v1/pages
GROUP /v1/users
GROUP /v1/users/me/channels
GROUP /v1/users/me/pages
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username TEXT UNIQUE,
    display_name TEXT NOT NULL,
    first_name TEXT,
    last_name TEXT,
    bio TEXT DEFAULT '',
    dob DATE,
    gender TEXT,
    avatar_media_id UUID,
    cover_media_id UUID,
    category TEXT DEFAULT 'personal',
    profession TEXT DEFAULT '',
    website TEXT DEFAULT '',
    location TEXT DEFAULT '',
    badge_flags INT DEFAULT 0,
    is_verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS user_links (
    user_id UUID NOT NULL REFERENCES users(id),
    platform TEXT NOT NULL,
    url TEXT NOT NULL,
    display_label TEXT DEFAULT '',
    sort_order INT DEFAULT 0,
    PRIMARY KEY (user_id, platform)
);

CREATE TABLE IF NOT EXISTS user_about (
    user_id UUID NOT NULL REFERENCES users(id),
    section TEXT NOT NULL,
    item_id UUID NOT NULL DEFAULT gen_random_uuid(),
    data JSONB NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public',
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section, item_id)
);

CREATE TABLE IF NOT EXISTS user_settings (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    account_visibility TEXT DEFAULT 'public',
    allow_messages_from TEXT DEFAULT 'everyone',
    allow_comments_from TEXT DEFAULT 'everyone',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS business_pages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id),
    page_handle    TEXT NOT NULL UNIQUE,
    page_name      TEXT NOT NULL,
    category       TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    address        TEXT NOT NULL DEFAULT '',
    lat            DOUBLE PRECISION,
    lng            DOUBLE PRECISION,
    business_hours JSONB,
    phone          TEXT NOT NULL DEFAULT '',
    whatsapp       TEXT NOT NULL DEFAULT '',
    business_email TEXT NOT NULL DEFAULT '',
    services       JSONB,
    price_range    TEXT NOT NULL DEFAULT '',
    booking_url    TEXT NOT NULL DEFAULT '',
    menu_urls      JSONB,
    is_verified    BOOLEAN NOT NULL DEFAULT FALSE,
    avg_rating     DOUBLE PRECISION NOT NULL DEFAULT 0,
    review_count   INTEGER NOT NULL DEFAULT 0,
    faq            JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS business_reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL,
    rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review_text TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(page_id, reviewer_id)
);

CREATE TABLE IF NOT EXISTS page_followers (
    page_id    UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, user_id)
);

CREATE TABLE IF NOT EXISTS page_roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id    UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    role       VARCHAR(40) NOT NULL CHECK (role IN ('owner','admin','editor','viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS page_verification_documents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id             UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    document_type       VARCHAR(80) NOT NULL,
    document_url        TEXT NOT NULL,
    status              VARCHAR(40) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','approved','rejected')),
    reviewed_by_user_id UUID,
    reviewed_at         TIMESTAMPTZ,
    rejection_reason    TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id),
    handle           TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    icon_url         TEXT NOT NULL DEFAULT '',
    banner_url       TEXT NOT NULL DEFAULT '',
    category         TEXT NOT NULL DEFAULT '',
    country          TEXT NOT NULL DEFAULT '',
    language         TEXT NOT NULL DEFAULT '',
    contact_email    TEXT NOT NULL DEFAULT '',
    collab_status    TEXT NOT NULL DEFAULT 'closed',
    content_schedule TEXT NOT NULL DEFAULT '',
    subscriber_count INTEGER NOT NULL DEFAULT 0,
    is_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS channel_links (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    url        TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS channel_milestones (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id     UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    milestone_type TEXT NOT NULL,
    title          TEXT NOT NULL,
    achieved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_public      BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS business_pages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id),
    page_handle    TEXT NOT NULL UNIQUE,
    page_name      TEXT NOT NULL,
    category       TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    address        TEXT NOT NULL DEFAULT '',
    lat            DOUBLE PRECISION,
    lng            DOUBLE PRECISION,
    business_hours JSONB,
    phone          TEXT NOT NULL DEFAULT '',
    whatsapp       TEXT NOT NULL DEFAULT '',
    business_email TEXT NOT NULL DEFAULT '',
    services       JSONB,
    price_range    TEXT NOT NULL DEFAULT '',
    booking_url    TEXT NOT NULL DEFAULT '',
    menu_urls      JSONB,
    is_verified    BOOLEAN NOT NULL DEFAULT FALSE,
    avg_rating     DOUBLE PRECISION NOT NULL DEFAULT 0,
    review_count   INTEGER NOT NULL DEFAULT 0,
    faq            JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS business_reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL,
    rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review_text TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(page_id, reviewer_id)
);

CREATE TABLE IF NOT EXISTS user_reputation (
    user_id              UUID PRIMARY KEY REFERENCES users(id),
    trust_score          DECIMAL(3,2) NOT NULL DEFAULT 0.50,
    endorsement_count    INTEGER NOT NULL DEFAULT 0,
    cross_platform_proofs JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS endorsements (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user_id UUID NOT NULL REFERENCES users(id),
    to_user_id   UUID NOT NULL REFERENCES users(id),
    skill_tag    TEXT NOT NULL,
    message      TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(from_user_id, to_user_id, skill_tag)
);

CREATE TABLE IF NOT EXISTS channel_members (
    channel_id  UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member',
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE TABLE IF NOT EXISTS referrals (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id    UUID NOT NULL REFERENCES users(id),
    referee_id     UUID REFERENCES users(id),
    invite_code    TEXT NOT NULL UNIQUE,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','signed_up','qualified','rewarded')),
    clicked_at     TIMESTAMPTZ,
    signed_up_at   TIMESTAMPTZ,
    qualified_at   TIMESTAMPTZ,
    reward_issued  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS channel_subscriptions (
    channel_id    UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notify_on     TEXT NOT NULL DEFAULT 'all' CHECK (notify_on IN ('all','highlights','none')),
    subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE TABLE IF NOT EXISTS profile_pins (
    user_id         UUID NOT NULL,
    content_type    TEXT NOT NULL CHECK (content_type IN ('post','reel','video','product')),
    content_id      UUID NOT NULL,
    pin_order       INT NOT NULL DEFAULT 0,
    pinned_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, content_type, content_id)
);

CREATE TABLE IF NOT EXISTS portfolio_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    type            TEXT NOT NULL CHECK (type IN ('project','article','video','design','achievement')),
    url             TEXT,
    media_id        UUID,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS profile_qr_codes (
    user_id     UUID PRIMARY KEY,
    qr_url      TEXT NOT NULL,
    short_link  TEXT NOT NULL,
    scan_count  BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS digital_wellbeing (
    user_id             UUID PRIMARY KEY,
    daily_limit_mins    INT,
    focus_mode_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    focus_mode_until    TIMESTAMPTZ,
    bedtime_start       TIME,
    bedtime_end         TIME,
    nudge_interval_mins INT NOT NULL DEFAULT 30,
    hide_like_counts    BOOLEAN NOT NULL DEFAULT FALSE,
    detox_mode_until    TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS screen_time_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    date          DATE NOT NULL,
    minutes       INT NOT NULL DEFAULT 0,
    session_count INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, date)
);

CREATE TABLE IF NOT EXISTS page_followers (
    page_id    UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, user_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type CreatePageRequest struct {
	PageHandle    string `json:"page_handle" binding:"required"`
	PageName      string `json:"page_name" binding:"required"`
	PageType      string `json:"page_type" binding:"required"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Address       string `json:"address"`
	Phone         string `json:"phone"`
	Whatsapp      string `json:"whatsapp"`
	BusinessEmail string `json:"business_email"`
	Website       string `json:"website"`
	PriceRange    string `json:"price_range"`
	BookingURL    string `json:"booking_url"`
	CoverMediaID  string `json:"cover_media_id"`
	AvatarMediaID string `json:"avatar_media_id"`
}

type UpdatePageRequest struct {
	PageName      string  `json:"page_name"`
	Category      string  `json:"category"`
	Description   string  `json:"description"`
	Address       string  `json:"address"`
	Lat           *float64 `json:"lat"`
	Lng           *float64 `json:"lng"`
	Phone         string  `json:"phone"`
	Whatsapp      string  `json:"whatsapp"`
	BusinessEmail string  `json:"business_email"`
	Website       string  `json:"website"`
	PriceRange    string  `json:"price_range"`
	BookingURL    string  `json:"booking_url"`
	CoverMediaID  string  `json:"cover_media_id"`
	AvatarMediaID string  `json:"avatar_media_id"`
}

type PageActions struct {
	CanFollow         bool `json:"canFollow"`
	CanUnfollow       bool `json:"canUnfollow"`
	CanManage         bool `json:"canManage"`
	CanMessage        bool `json:"canMessage"`
	CanAddFriend      bool `json:"canAddFriend"` // ALWAYS false on a page
	CanEdit           bool `json:"canEdit"`
	CanUploadDocument bool `json:"canUploadDocument"`
	CanSubmitForReview bool `json:"canSubmitForReview"`
}

type PageActionButton struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Primary bool   `json:"primary,omitempty"`
	Gated   bool   `json:"gated"`
}

type PageResponse struct {
	*store.BusinessPage
	DisplayType   string             `json:"displayType"`
	ViewerRole    string             `json:"viewerRole"`
	IsOwner       bool               `json:"isOwner"`
	BannerMessage string             `json:"bannerMessage,omitempty"`
	Actions       PageActions        `json:"actions"`
	ActionButtons []PageActionButton `json:"actionButtons"`
}

type SubmitReviewRequest struct {
	Rating     int    `json:"rating" binding:"required,min=1,max=5"`
	ReviewText string `json:"review_text"`
}

type AddDocumentRequest struct {
	DocumentType string `json:"document_type" binding:"required"`
	DocumentURL  string `json:"document_url" binding:"required"`
}

type SubscribeRequest struct {
	NotifyOn string `json:"notify_on"`
}

type UpdateProfileRequest struct {
	DisplayName   string     `json:"display_name"`
	Bio           string     `json:"bio"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id"`
	FirstName     *string    `json:"first_name"`
	LastName      *string    `json:"last_name"`
	Gender        *string    `json:"gender"`
	DoB           *time.Time `json:"dob"`
	Username      *string    `json:"username"`
	Category      *string    `json:"category"`
	Profession    *string    `json:"profession"`
	Website       *string    `json:"website"`
	Location      *string    `json:"location"`
}

type UpdateLinksRequest struct {
	Links []LinkItem `json:"links"`
}

type LinkItem struct {
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	DisplayLabel string `json:"display_label"`
	SortOrder    int    `json:"sort_order"`
}

type UpsertAboutRequest struct {
	ItemID     *string         `json:"item_id"`
	Data       json.RawMessage `json:"data" binding:"required"`
	Visibility string          `json:"visibility"`
	SortOrder  int             `json:"sort_order"`
}

type UpdateStatusRequest struct {
	StatusText  string     `json:"status_text"`
	StatusEmoji string     `json:"status_emoji"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type EndorseRequest struct {
	SkillTag string `json:"skill_tag" binding:"required"`
	Message  string `json:"message"`
}

type CreateChannelRequest struct {
	Handle          string `json:"handle" binding:"required"`
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	Country         string `json:"country"`
	Language        string `json:"language"`
	ContactEmail    string `json:"contact_email"`
	CollabStatus    string `json:"collab_status"`
	ContentSchedule string `json:"content_schedule"`
}

type UpdateChannelRequest struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	AvatarMediaID   *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID `json:"banner_media_id"`
	Category        *string    `json:"category"`
	Country         *string    `json:"country"`
	Language        *string    `json:"language"`
	ContactEmail    *string    `json:"contact_email"`
	CollabStatus    *string    `json:"collab_status"`
	ContentSchedule *string    `json:"content_schedule"`
}

type PinContentRequest struct {
	ContentType string `json:"content_type" binding:"required"`
	ContentID   string `json:"content_id" binding:"required"`
	PinOrder    int    `json:"pin_order"`
}

type AddPortfolioItemRequest struct {
	Title       string     `json:"title" binding:"required"`
	Description string     `json:"description"`
	Type        string     `json:"type" binding:"required"`
	URL         string     `json:"url"`
	MediaID     *uuid.UUID `json:"media_id"`
	SortOrder   int        `json:"sort_order"`
}

type UpdatePortfolioItemRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	URL         string `json:"url"`
	SortOrder   int    `json:"sort_order"`
}

type LogScreenTimeRequest struct {
	Minutes  int `json:"minutes" binding:"required,min=1"`
	Sessions int `json:"sessions"`
}
```
