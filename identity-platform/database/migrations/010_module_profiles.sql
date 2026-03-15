-- 010_module_profiles.sql: Per-module profile overrides (Postbook/Posttube/Postgram)
-- When use_global_identity=true, module reads display_name/avatar/bio from profile.profiles.
-- When false, module uses its own name_override/avatar_override_url.
-- Module-specific fields (banner, watermark, links, defaults) always live here.

CREATE TABLE IF NOT EXISTS profile.module_profiles (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    module               TEXT NOT NULL CHECK (module IN ('postbook', 'posttube', 'postgram')),
    use_global_identity  BOOLEAN NOT NULL DEFAULT TRUE,
    name_override        TEXT,
    avatar_override_url  TEXT,
    banner_url           TEXT,
    watermark_url        TEXT,
    links                JSONB NOT NULL DEFAULT '[]',
    defaults             JSONB NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, module)
);

CREATE INDEX IF NOT EXISTS idx_module_profiles_user
    ON profile.module_profiles(user_id);

COMMENT ON TABLE profile.module_profiles IS 'Per-module profile overrides for Postbook/Posttube/Postgram';
