# Module: dating-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /photos/:id
DELETE /profile
DELETE /prompts/:promptId
DELETE /sparks/:id
DELETE /stash/:candidateId
DELETE /vouches/:id
GET /admin/audit
GET /admin/photos/pending
GET /admin/reports
GET /admin/risk
GET /admin/safety/panic
GET /data-export/me
GET /matches
GET /matches/:id
GET /photos
GET /photos/me
GET /preferences
GET /premium/me
GET /premium/plans
GET /profile
GET /profile/privacy
GET /profile/:userId/preview
GET /prompts
GET /prompts/catalog
GET /pulse/nebula
GET /pulse/:targetUserId/explain
GET /pulse/today
GET /risk/:userId
GET /sparks/incoming
GET /stash
GET /tune
GET /vouches/for/:userId
GET /vouches/sent
PATCH /photos/:id
PATCH /profile/intent
PATCH /profile/privacy
POST /admin/reports/:id/action
POST /admin/safety/panic/:id/ack
POST /data-export
POST /matches/:id/close
POST /matches/:id/extend
POST /matches/:id/first-message
POST /moderation/scan
POST /photos
POST /photos/:id/moderation
POST /premium/cancel
POST /premium/checkout
POST /profile
POST /profile/pause
POST /pulse/boost
POST /safety/block
POST /safety/meet
POST /safety/meet/:id/check-in
POST /safety/panic
POST /safety/report
POST /safety/share-location
POST /sparks
POST /stash
POST /v1/dating/premium/webhook
POST /verification/aadhaar/callback
POST /verification/aadhaar/start
POST /verification/selfie
POST /vouches
POST /vouches/:id/accept
POST /vouches/:id/decline
PUT /preferences
PUT /prompts/:promptId
PUT /tune
GROUP /v1/dating
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS dating_profiles (
    user_id           UUID PRIMARY KEY,
    intent            TEXT NOT NULL DEFAULT 'casual'
        CHECK (intent IN ('casual','serious','marriage')),
    bio               TEXT NOT NULL DEFAULT '',
    gender            TEXT,
    birth_date        DATE,
    city              TEXT,
    state             TEXT,
    country           TEXT,
    latitude          DOUBLE PRECISION,
    longitude         DOUBLE PRECISION,
    location_geohash  TEXT,
    height_cm         INT,
    religion          TEXT,
    community         TEXT,
    occupation        TEXT,
    education         TEXT,
    drinking          TEXT,
    smoking           TEXT,
    exercise          TEXT,
    diet              TEXT,
    wants_children    TEXT,
    family_plans      TEXT,
    blur_mode         BOOLEAN NOT NULL DEFAULT false,
    visible_to_public BOOLEAN NOT NULL DEFAULT true,
    paused            BOOLEAN NOT NULL DEFAULT false,
    language_prefs    TEXT[] NOT NULL DEFAULT '{}',
    trust_tier        TEXT   NOT NULL DEFAULT 'phone'
        CHECK (trust_tier IN ('phone','selfie','aadhaar')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS dating_tunes (
    user_id            UUID PRIMARY KEY,
    lifestyle_rhythm   SMALLINT,                          -- 1..5 Quiet..Vibrant
    conversation_style TEXT
        CHECK (conversation_style IS NULL OR conversation_style IN ('witty','deep','playful','direct','reflective')),
    faith_weight       SMALLINT,                          -- 1..5
    family_weight      SMALLINT,                          -- 1..5
    region_weight      SMALLINT,                          -- 1..5
    family_plans_axis  SMALLINT,                          -- marriage-only
    education_axis     SMALLINT,                          -- marriage-only
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_photos (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL,
    media_id          UUID NOT NULL,
    sort_order        SMALLINT NOT NULL DEFAULT 0,
    is_primary        BOOLEAN  NOT NULL DEFAULT false,
    visibility        TEXT     NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public','match_only','sparked_only')),
    moderation_status TEXT     NOT NULL DEFAULT 'pending'
        CHECK (moderation_status IN ('pending','approved','rejected')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_prompts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    prompt_id  INT  NOT NULL,
    answer     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, prompt_id)
);

CREATE TABLE IF NOT EXISTS dating_preferences (
    user_id              UUID PRIMARY KEY,
    min_age              INT,
    max_age              INT,
    distance_km          INT     NOT NULL DEFAULT 25,
    interested_in_gender TEXT,
    intent_filter        TEXT[]  NOT NULL DEFAULT '{}',
    blur_mode_pref       BOOLEAN NOT NULL DEFAULT false,
    language_filter      TEXT[]  NOT NULL DEFAULT '{}',
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_sparks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user_id UUID NOT NULL,
    to_user_id   UUID NOT NULL,
    target_kind  TEXT NOT NULL
        CHECK (target_kind IN ('photo','prompt','tune_axis','echo')),
    target_ref   TEXT NOT NULL,
    note         TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (from_user_id, to_user_id, target_kind, target_ref)
);

CREATE TABLE IF NOT EXISTS dating_stashes (
    user_id             UUID NOT NULL,
    candidate_id        UUID NOT NULL,
    stashed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    reactivation_signal TEXT,
    PRIMARY KEY (user_id, candidate_id)
);

CREATE TABLE IF NOT EXISTS dating_passes (
    user_id      UUID NOT NULL,
    candidate_id UUID NOT NULL,
    passed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason       TEXT,
    PRIMARY KEY (user_id, candidate_id)
);

CREATE TABLE IF NOT EXISTS dating_blocks (
    user_id      UUID NOT NULL,
    blocked_id   UUID NOT NULL,
    reason       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, blocked_id)
);

CREATE TABLE IF NOT EXISTS dating_matches (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_a           UUID NOT NULL,
    user_b           UUID NOT NULL,
    status           TEXT NOT NULL DEFAULT 'matched'
        CHECK (status IN ('matched','conversing','quiet','expired','closed')),
    conversation_id  UUID,
    spark_target     JSONB,
    matched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_message_at TIMESTAMPTZ,
    last_message_at  TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ,
    closed_by        UUID,
    CHECK (user_a < user_b)
);

CREATE TABLE IF NOT EXISTS dating_vouches (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    voucher_id   UUID NOT NULL,
    vouchee_id   UUID NOT NULL,
    relationship TEXT,
    community_id UUID,
    note         TEXT,
    status       TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','accepted','declined','revoked')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ,
    UNIQUE (voucher_id, vouchee_id)
);

CREATE TABLE IF NOT EXISTS dating_verifications (
    user_id        UUID PRIMARY KEY,
    selfie_status  TEXT,                         -- pending | passed | failed
    selfie_score   DOUBLE PRECISION,
    selfie_at      TIMESTAMPTZ,
    aadhaar_status TEXT,                         -- pending | verified | failed
    aadhaar_at     TIMESTAMPTZ,
    digilocker_ref TEXT
);

CREATE TABLE IF NOT EXISTS dating_safety_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    kind       TEXT NOT NULL,
    details    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_premium_subscriptions (
    user_id    UUID PRIMARY KEY,
    plan       TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    source     TEXT
);

CREATE TABLE IF NOT EXISTS dating_echo_cache (
    user_id      UUID PRIMARY KEY,
    reels        JSONB NOT NULL DEFAULT '[]'::jsonb,
    qa_answers   JSONB NOT NULL DEFAULT '[]'::jsonb,
    communities  JSONB NOT NULL DEFAULT '[]'::jsonb,
    posts        JSONB NOT NULL DEFAULT '[]'::jsonb,
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_meets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    with_user_id    UUID NOT NULL,
    scheduled_at    TIMESTAMPTZ NOT NULL,
    venue           TEXT,
    latitude        DOUBLE PRECISION,
    longitude       DOUBLE PRECISION,
    check_in_status TEXT,
    checked_in_at   TIMESTAMPTZ,
    no_show_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_reports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id UUID NOT NULL,
    target_id   UUID NOT NULL,
    category    TEXT NOT NULL,
    details     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_moderation_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id      UUID NOT NULL,
    conversation_id UUID NOT NULL,
    layer           SMALLINT NOT NULL,
    confidence      FLOAT NOT NULL,
    patterns        TEXT[] NOT NULL DEFAULT '{}',
    action_taken    TEXT NOT NULL DEFAULT 'shadow',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, layer)
);

CREATE TABLE IF NOT EXISTS dating_premium_plans (
    id                TEXT PRIMARY KEY,
    plan_type         TEXT NOT NULL CHECK (plan_type IN ('subscription','one_time')),
    name              TEXT NOT NULL,
    price_inr_paise   BIGINT NOT NULL,
    duration_days     INT,
    description       TEXT,
    is_active         BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_payment_intents (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  UUID NOT NULL,
    plan_id                  TEXT NOT NULL REFERENCES dating_premium_plans(id),
    amount_inr_paise         BIGINT NOT NULL,
    razorpay_order_id        TEXT NOT NULL UNIQUE,
    razorpay_subscription_id TEXT,
    status                   TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','attempted','paid','failed','cancelled')),
    source                   TEXT NOT NULL DEFAULT 'app',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at                  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS dating_payment_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_intent_id   UUID REFERENCES dating_payment_intents(id),
    razorpay_event_id   TEXT NOT NULL UNIQUE,
    event_type          TEXT NOT NULL,
    payload             JSONB NOT NULL,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS dating_data_exports (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL,
    requested_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at          TIMESTAMPTZ,
    download_url          TEXT,
    download_expires_at   TIMESTAMPTZ,
    status                TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','processing','ready','failed','expired'))
);

CREATE TABLE IF NOT EXISTS dating_consent_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    consent_type    TEXT NOT NULL,
    granted         BOOLEAN NOT NULL,
    policy_version  TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_admin_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_admin_id  UUID NOT NULL,
    action          TEXT NOT NULL,
    target_user_id  UUID,
    target_resource TEXT,
    reason          TEXT,
    policy_code     TEXT,
    internal_notes  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dating_account_risk (
    user_id           UUID PRIMARY KEY,
    risk_score        INT  NOT NULL CHECK (risk_score BETWEEN 0 AND 100),
    risk_level        TEXT NOT NULL DEFAULT 'allow'
        CHECK (risk_level IN
            ('allow','reduce_reach','require_recheck',
             'hide_from_discovery','chat_hold','admin_review','suspend')),
    signals           JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dating_device_fingerprints (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    fingerprint     TEXT NOT NULL,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip              TEXT,
    UNIQUE(user_id, fingerprint)
);

```

## API types (request/response Go structs with JSON tags)
```go
type actOnReportRequest struct {
	Action       string `json:"action" binding:"required"`
	TargetUserID string `json:"target_user_id"`
}

type scanRequest struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	SenderID       string `json:"sender_id"`
	Body           string `json:"body"`
}

type setPhotoModerationRequest struct {
	Status string `json:"status" binding:"required"`
	Reason string `json:"reason"`
}

type shareLocationRequest struct {
	ContactID       string   `json:"contact_id"`
	DurationMinutes int      `json:"duration_minutes"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
}

type scheduleMeetRequest struct {
	WithUserID string    `json:"with_user_id"`
	When       time.Time `json:"when"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Venue      string    `json:"venue"`
}

type meetCheckInRequest struct {
	Status string `json:"status"`
}

type blockRequest struct {
	TargetUserID string `json:"target_user_id"`
}

type reportRequest struct {
	TargetID string `json:"target_id"`
	Category string `json:"category"`
	Details  string `json:"details"`
}

type createSparkRequest struct {
	ToUserID   string `json:"to_user_id"`
	TargetKind string `json:"target_kind"`
	TargetRef  string `json:"target_ref"`
	Note       string `json:"note,omitempty"`
}

type addStashRequest struct {
	CandidateID string `json:"candidate_id"`
}

type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type selfieRequest struct {
	Embedding []float64 `json:"embedding"`
}

type requestVouchRequest struct {
	VoucheeID    string  `json:"vouchee_id"`
	Relationship string  `json:"relationship"`
	CommunityID  *string `json:"community_id,omitempty"`
	Note         string  `json:"note,omitempty"`
}
```
