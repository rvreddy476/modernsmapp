-- profile-service schema.
--
-- WHY THIS FILE EXISTS
--
-- profile-service had no schema of its own. It relied entirely on the
-- boot-time migration runner, which was pointed at a disabled directory, so
-- six of the ten tables its queries reference were never created in any
-- environment. The service reported healthy and returned 500 on every request
-- that touched them:
--
--     GET /v1/profiles/:id/stats  -> 500  (profile.profile_stats absent)
--     GET /v1/profiles/:id/links  -> 500  (profile.user_links absent)
--     GET /v1/profiles/:id/about  -> 500  (profile.user_about absent)
--
-- The DDL below is not new. It is consolidated from the definitions that
-- already existed but were never applied:
--
--     profile.profile_links   identity-platform/database/schema.sql
--     profile.user_links      identity-platform/database/schema.sql
--     profile.user_about      identity-platform/database/schema.sql
--     profile.handle_history  database/migrations/009_handle_history.sql
--     profile.module_profiles database/migrations/010_module_profiles.sql
--     profile.profile_stats   database/migrations/012_profile_stats.sql
--
-- WHAT IS DELIBERATELY NOT HERE
--
-- profile.follows, profile.blocks and profile.friendships. Those are the
-- shadow social-graph tables retired by internal/http/retired_graph_routes.go:
-- graph-service owns the canonical graph, and a block written here was
-- enforced by nothing, so the user was told they were protected and was not.
-- The routes answer 410 Gone. Creating the tables again would hand the dead
-- store methods somewhere to write and reopen that bypass.
--
-- TYPE CHOICES
--
-- schema.sql declares visibility as the enum `visibility_level`. This database
-- contains zero enum types — every column that schema.sql types as an enum is
-- TEXT in reality, including auth.users.account_type. Introducing the enum for
-- one column would make a fresh database diverge from every existing one, and
-- adding a value to a PostgreSQL enum is a migration whereas widening a CHECK
-- is not. TEXT + CHECK gives the same rejection with less to unpick later.
--
-- Every statement is idempotent, so the pipeline can re-run this file against a
-- database that already has it.

CREATE SCHEMA IF NOT EXISTS profile;

-- profile.profiles is NOT created here. It is provisioned by auth-service
-- (services/auth-service/database/setup.sql) together with the auth and usr
-- base tables, and the foreign keys below point at it. That ownership is worth
-- revisiting, but duplicating the definition would be worse: two files
-- creating one table means the version that wins is whichever service booted
-- first.

-- ---------------------------------------------------------------------------
-- Rich profile content
-- ---------------------------------------------------------------------------

-- Curated link list shown on the profile (the "link in bio" surface).
CREATE TABLE IF NOT EXISTS profile.profile_links (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  UUID         NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    title       VARCHAR(100) NOT NULL,
    url         VARCHAR(500) NOT NULL,
    icon        VARCHAR(50),
    category    VARCHAR(50),
    sort_order  INT          NOT NULL DEFAULT 0,
    click_count BIGINT       NOT NULL DEFAULT 0,
    is_pinned   BOOLEAN      NOT NULL DEFAULT FALSE,
    visibility  TEXT         NOT NULL DEFAULT 'public'
                             CHECK (visibility IN ('public', 'followers', 'friends', 'only_me')),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Ordering is part of the query, not a client-side sort: the list is rendered
-- in sort_order and paginating an unordered read would repeat rows.
CREATE INDEX IF NOT EXISTS idx_profile_links_profile
    ON profile.profile_links (profile_id, sort_order);

-- Social handles. Keyed on (user_id, platform) because a profile has at most
-- one link per platform, which makes the upsert a primary-key conflict rather
-- than a delete-then-insert that can lose rows if it fails half way.
CREATE TABLE IF NOT EXISTS profile.user_links (
    user_id       UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    platform      TEXT NOT NULL,
    url           TEXT NOT NULL,
    display_label TEXT DEFAULT '',
    sort_order    INT  DEFAULT 0,
    PRIMARY KEY (user_id, platform)
);

-- Free-form profile sections (work, education, interests…). The payload is
-- JSONB because each section has a different shape and the service validates
-- it; a column per field would need a migration for every new section type.
CREATE TABLE IF NOT EXISTS profile.user_about (
    user_id    UUID        NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    section    TEXT        NOT NULL,
    item_id    UUID        NOT NULL DEFAULT gen_random_uuid(),
    data       JSONB       NOT NULL,
    visibility TEXT        NOT NULL DEFAULT 'public',
    sort_order INT         DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section, item_id)
);

CREATE INDEX IF NOT EXISTS idx_user_about_section
    ON profile.user_about (user_id, section);

-- ---------------------------------------------------------------------------
-- Handles
-- ---------------------------------------------------------------------------

-- Username changes, kept for two reasons: redirecting links that point at an
-- old handle, and enforcing the change cooldown. Without the history a user
-- could cycle handles freely and strand every link that referenced them.
CREATE TABLE IF NOT EXISTS profile.handle_history (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    old_username   TEXT NOT NULL,
    new_username   TEXT NOT NULL,
    changed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cooldown_until TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);

-- DESC on changed_at: both reads want the most recent row, and a descending
-- index lets the planner stop after one.
CREATE INDEX IF NOT EXISTS idx_handle_history_user
    ON profile.handle_history (user_id, changed_at DESC);

CREATE INDEX IF NOT EXISTS idx_handle_history_old_username
    ON profile.handle_history (old_username, changed_at DESC);

COMMENT ON TABLE profile.handle_history IS
    'Username changes, for old-handle redirects and cooldown enforcement.';

-- ---------------------------------------------------------------------------
-- Per-module overrides
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS profile.module_profiles (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    module              TEXT NOT NULL CHECK (module IN ('postbook', 'posttube', 'postgram')),
    use_global_identity BOOLEAN NOT NULL DEFAULT TRUE,
    name_override       TEXT,
    avatar_override_url TEXT,
    banner_url          TEXT,
    watermark_url       TEXT,
    links               JSONB NOT NULL DEFAULT '[]',
    defaults            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, module)
);

CREATE INDEX IF NOT EXISTS idx_module_profiles_user
    ON profile.module_profiles (user_id);

COMMENT ON TABLE profile.module_profiles IS
    'Per-module profile overrides for Postbook/Posttube/Postgram.';

-- ---------------------------------------------------------------------------
-- Denormalised counters
-- ---------------------------------------------------------------------------

-- Counters maintained by event consumers rather than computed on read. A
-- follower count computed with COUNT(*) is the query that takes the profile
-- page down once an account gets large.
CREATE TABLE IF NOT EXISTS profile.profile_stats (
    user_id         UUID PRIMARY KEY REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    post_count      INT NOT NULL DEFAULT 0,
    follower_count  INT NOT NULL DEFAULT 0,
    following_count INT NOT NULL DEFAULT 0,
    friend_count    INT NOT NULL DEFAULT 0,
    total_sparks    INT NOT NULL DEFAULT 0,
    is_creator      BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- PARTIAL index: creator listings are the only query that filters on this, and
-- creators are a small fraction of rows, so indexing the false side would cost
-- writes for a read nobody performs.
CREATE INDEX IF NOT EXISTS idx_profile_stats_creator
    ON profile.profile_stats (is_creator) WHERE is_creator = TRUE;

-- ---------------------------------------------------------------------------
-- Event consumption
-- ---------------------------------------------------------------------------

-- Inbox for the Kafka consumer. Delivery is at-least-once, so the consumer
-- records each event id and skips repeats; without this a redelivered
-- follower event double-counts a stat permanently.
CREATE TABLE IF NOT EXISTS profile.inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);

-- Supports the retention sweep. The table is append-only per event, so without
-- a cleanup path indexed by time it grows forever.
CREATE INDEX IF NOT EXISTS idx_profile_inbox_cleanup
    ON profile.inbox_events (processed_at);

-- ---------------------------------------------------------------------------
-- Account lifecycle: hide / unhide (auth-service 30-day deletion flow)
-- ---------------------------------------------------------------------------

-- Marks a profile hidden because auth-service reported user.deactivated or
-- user.deletion_scheduled. Reversible: user.reactivated / deletion_cancelled
-- deletes the row again. See internal/purge and internal/store/purge.go.
--
-- ON DELETE CASCADE: when user.purge_requested erases profile.profiles, this
-- marker must go with it rather than dangling on a user_id nothing references
-- anymore.
CREATE TABLE IF NOT EXISTS profile.hidden_profiles (
    user_id   UUID PRIMARY KEY REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    reason    TEXT NOT NULL,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE profile.hidden_profiles IS
    'Accounts hidden by auth-service deactivate/scheduled-deletion; reversible, never erases.';
