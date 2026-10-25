-- Cross-service parent tables post-service's migration chain points at, for
-- the integration database only (scripts/ci/integration-services.yaml).
--
-- post-service migrations 009-012 alter user_preferences and reference users,
-- channels and media_assets, which belong to user-service and media-service.
-- Several suites create these stubs themselves (openSubscriptionDB,
-- subscriptionTestPool, openHubMediaDB); others (openReviewAuditDB, the M7
-- helpers, POLL/STORY/...) assume an earlier package already did. Creating
-- them up front removes that ordering dependency. The shapes are copied from
-- the test helpers, not from the real services:
--   * media-service's real media_assets has NOT NULL columns the tests never
--     fill and no moderation_status column;
--   * user-service's real users.display_name is NOT NULL and the tests insert
--     users(id) only.
-- So do NOT replace these with the other services' setup.sql.

CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY);

CREATE TABLE IF NOT EXISTS user_preferences (user_id UUID PRIMARY KEY);

-- user-service's phase-6 channels table (userServiceChannelsDDL in
-- internal/store/postgres/channel_subscriptions_integration_test.go).
CREATE TABLE IF NOT EXISTS channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    handle TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    contact_email TEXT NOT NULL DEFAULT '',
    collab_status TEXT NOT NULL DEFAULT 'closed',
    content_schedule TEXT NOT NULL DEFAULT '',
    subscriber_count INTEGER NOT NULL DEFAULT 0,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The media_assets stub every post-service helper uses, plus every column
-- post-service's production queries read. Leaving optional columns to "the
-- suites that need them" made the result depend on package order: a suite
-- that ran before the helper that added alt_text / hls_master_key failed with
-- 42703. The read columns are nullable or defaulted here (unlike media-service,
-- where storage_key is NOT NULL) because the tests insert only the first four.
--
-- Guarded by TestMediaAssetsStubCoversEveryColumnPostServiceReads
-- (Architecture/services/post-service/internal/store/postgres/
-- media_assets_stub_test.go): it fails when a column post-service reads is
-- missing here, or a column here is not one of media-service's.
CREATE TABLE IF NOT EXISTS media_assets (
    id UUID PRIMARY KEY,
    uploader_id UUID NOT NULL,
    file_type TEXT NOT NULL,
    processing_status TEXT NOT NULL,
    moderation_status TEXT NOT NULL DEFAULT 'pending',
    duration_seconds INTEGER,
    duration_ms INTEGER,
    width INTEGER,
    height INTEGER,
    alt_text TEXT DEFAULT '',
    alt_decorative BOOLEAN NOT NULL DEFAULT FALSE,
    hls_master_key TEXT,
    storage_key TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
