-- Gamification: Streaks, Badges, Missions, Loyalty Points

-- User streaks (daily posting, login, creator uploads)
CREATE TABLE IF NOT EXISTS user_streaks (
    user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    type          TEXT NOT NULL CHECK (type IN ('daily_post','daily_login','creator_upload')),
    current_count INT NOT NULL DEFAULT 0,
    longest_count INT NOT NULL DEFAULT 0,
    last_action_at DATE NOT NULL DEFAULT CURRENT_DATE,
    started_at    DATE NOT NULL DEFAULT CURRENT_DATE
);

-- Badges earned by users
CREATE TABLE IF NOT EXISTS user_badges (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('verified','creator','top_contributor','helpful_member','streak_30','streak_100','early_adopter','expert')),
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (user_id, type)
);
CREATE INDEX IF NOT EXISTS idx_badges_user ON user_badges(user_id) WHERE is_visible = TRUE;

-- Missions / challenges
CREATE TABLE IF NOT EXISTS missions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       TEXT NOT NULL,
    description TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('post_count','spark_count','follower_gain','community_join')),
    target      INT NOT NULL,
    reward_type TEXT NOT NULL CHECK (reward_type IN ('badge','points','feature_unlock')),
    reward_data JSONB,
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS mission_progress (
    mission_id   UUID NOT NULL REFERENCES missions(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    progress     INT NOT NULL DEFAULT 0,
    completed    BOOLEAN NOT NULL DEFAULT FALSE,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (mission_id, user_id)
);

-- Loyalty points ledger
CREATE TABLE IF NOT EXISTS loyalty_points (
    user_id         UUID PRIMARY KEY REFERENCES users(id),
    balance         BIGINT NOT NULL DEFAULT 0,
    lifetime_earned BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS point_transactions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id),
    amount     INT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('post_reward','streak_bonus','mission_reward','commerce_spend','referral')),
    ref_id     UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_point_tx_user ON point_transactions(user_id, created_at DESC);
