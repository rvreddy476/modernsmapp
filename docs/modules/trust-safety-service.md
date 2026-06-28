# Module: trust-safety-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /:id
GET /:mediaId
GET /:userId
PATCH /:id
GROUP /v1/appeals
GROUP /v1/grievances
GROUP /v1/keyword-filters
GROUP /v1/media-labels
GROUP /v1/reports
GROUP /v1/strikes
GROUP /v1/teen-accounts
GROUP /v1/verification-requests
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS trust.reports (
    id UUID PRIMARY KEY,
    reporter_id UUID NOT NULL,
    entity_type TEXT NOT NULL, -- 'user', 'post', 'comment'
    entity_id UUID NOT NULL,
    reason TEXT NOT NULL,
    details TEXT,
    status TEXT NOT NULL DEFAULT 'open', -- 'open', 'reviewing', 'closed'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.reports (
    id UUID PRIMARY KEY,
    reporter_id UUID NOT NULL,
    entity_type TEXT NOT NULL, -- 'user', 'post', 'comment'
    entity_id UUID NOT NULL,
    reason TEXT NOT NULL,
    details TEXT,
    status TEXT NOT NULL DEFAULT 'open', -- 'open', 'reviewing', 'closed'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.content_appeals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    content_type    TEXT NOT NULL,
    content_id      UUID NOT NULL,
    action_taken    TEXT NOT NULL,
    appeal_reason   TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','under_review','upheld','overturned','expired')),
    reviewed_by     UUID,
    resolution_note TEXT,
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS trust.keyword_filters (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope       TEXT NOT NULL CHECK (scope IN ('platform','group','channel','user')),
    scope_id    UUID,
    keyword     TEXT NOT NULL,
    action      TEXT NOT NULL DEFAULT 'hide' CHECK (action IN ('hide','flag','block')),
    added_by    UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.teen_accounts (
    user_id             UUID PRIMARY KEY,
    guardian_id         UUID,
    guardian_approved   BOOLEAN NOT NULL DEFAULT FALSE,
    daily_limit_mins    INT NOT NULL DEFAULT 60,
    content_filter      TEXT NOT NULL DEFAULT 'strict' CHECK (content_filter IN ('strict','moderate','off')),
    dm_restricted       BOOLEAN NOT NULL DEFAULT TRUE,
    follower_approval   BOOLEAN NOT NULL DEFAULT TRUE,
    location_hidden     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.media_labels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_asset_id  UUID NOT NULL,
    label_type      TEXT NOT NULL CHECK (label_type IN ('ai_generated','deepfake','edited','satire','synthetic_audio')),
    confidence      REAL NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('auto_detected','user_reported','admin_labelled')),
    labeled_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.user_strikes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    reason          TEXT NOT NULL,
    content_type    TEXT,
    content_id      UUID,
    severity        TEXT NOT NULL CHECK (severity IN ('warning','strike','severe_strike')),
    expires_at      TIMESTAMPTZ,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.verification_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('creator','business','organization','government')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','more_info_needed')),
    submitted_docs  JSONB,
    rejection_reason TEXT,
    reviewed_by     UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trust.user_trust_state (
    user_id                 UUID PRIMARY KEY,
    trust_score             SMALLINT NOT NULL DEFAULT 50
        CHECK (trust_score BETWEEN 0 AND 100),
    trust_tier              VARCHAR(16) NOT NULL DEFAULT 'new'
        CHECK (trust_tier IN ('new', 'low', 'standard', 'trusted', 'verified')),
    account_age_days        INTEGER NOT NULL DEFAULT 0,
    reports_received        INTEGER NOT NULL DEFAULT 0,
    blocks_received         INTEGER NOT NULL DEFAULT 0,
    connection_accept_ratio NUMERIC(4,3),
    last_recomputed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    shadowbanned            BOOLEAN NOT NULL DEFAULT FALSE,
    suspended_until         TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS trust.grievances (
    id                UUID PRIMARY KEY,
    complainant_id    UUID NOT NULL,
    subject           TEXT NOT NULL CHECK (subject IN
                          ('content_complaint', 'privacy', 'account',
                           'intellectual_property', 'other')),
    about_entity_type TEXT,
    about_entity_id   UUID,
    description       TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'open' CHECK (status IN
                          ('open', 'acknowledged', 'resolved', 'rejected')),
    assigned_to       UUID,
    resolution_notes  TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acknowledged_at   TIMESTAMPTZ,
    resolved_at       TIMESTAMPTZ,
    due_at            TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```

## API types (request/response Go structs with JSON tags)
```go
type fileGrievanceRequest struct {
	Subject         string `json:"subject" binding:"required"`
	AboutEntityType string `json:"about_entity_type"`
	AboutEntityID   string `json:"about_entity_id"`
	Description     string `json:"description" binding:"required"`
}

type updateGrievanceRequest struct {
	Status          string `json:"status" binding:"required,oneof=acknowledged resolved rejected"`
	ResolutionNotes string `json:"resolution_notes"`
}

type FileReportRequest struct {
	EntityType string `json:"entity_type" binding:"required,oneof=user post comment"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
	Details    string `json:"details"`
}

type UpdateReportRequest struct {
	Status          string  `json:"status" binding:"required,oneof=reviewing resolved dismissed"`
	AssignedTo      *string `json:"assigned_to,omitempty"`
	ResolutionNotes string  `json:"resolution_notes"`
}

type submitAppealRequest struct {
	ContentType string `json:"content_type" binding:"required"`
	ContentID   string `json:"content_id" binding:"required"`
	ActionTaken string `json:"action_taken" binding:"required"`
	AppealReason string `json:"appeal_reason" binding:"required"`
}

type reviewAppealRequest struct {
	Status string `json:"status" binding:"required"`
	Note   string `json:"note"`
}

type addKeywordFilterRequest struct {
	Scope   string  `json:"scope" binding:"required"`
	ScopeID *string `json:"scope_id,omitempty"`
	Keyword string  `json:"keyword" binding:"required"`
	Action  string  `json:"action"`
}

type upsertTeenAccountRequest struct {
	GuardianID     *string `json:"guardian_id,omitempty"`
	DailyLimitMins int     `json:"daily_limit_mins"`
	ContentFilter  string  `json:"content_filter"`
	DMRestricted   bool    `json:"dm_restricted"`
	FollowerApproval bool  `json:"follower_approval"`
	LocationHidden bool    `json:"location_hidden"`
}

type addMediaLabelRequest struct {
	MediaAssetID string  `json:"media_asset_id" binding:"required"`
	LabelType    string  `json:"label_type" binding:"required"`
	Confidence   float32 `json:"confidence" binding:"required"`
	Source       string  `json:"source" binding:"required"`
}

type issueStrikeRequest struct {
	UserID      string  `json:"user_id" binding:"required"`
	Reason      string  `json:"reason" binding:"required"`
	ContentType string  `json:"content_type"`
	ContentID   *string `json:"content_id,omitempty"`
	Severity    string  `json:"severity" binding:"required"`
}

type submitVerificationRequest struct {
	Type string            `json:"type" binding:"required"`
	Docs map[string]string `json:"docs"`
}

type reviewVerificationRequest struct {
	Status          string `json:"status" binding:"required"`
	RejectionReason string `json:"rejection_reason"`
}
```
