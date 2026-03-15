-- Close Friends List
CREATE TABLE IF NOT EXISTS close_friends (
    user_id    UUID NOT NULL,
    friend_id  UUID NOT NULL,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, friend_id),
    FOREIGN KEY (user_id)   REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (friend_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_close_friends_owner ON close_friends(user_id);

-- Circles (Named Custom Audiences)
CREATE TABLE IF NOT EXISTS circles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       VARCHAR(100) NOT NULL,
    emoji      VARCHAR(10),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_circles_owner ON circles(owner_id);

CREATE TABLE IF NOT EXISTS circle_members (
    circle_id  UUID NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (circle_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_circle_members_user ON circle_members(user_id);

-- Relationship Labels
CREATE TABLE IF NOT EXISTS relationship_labels (
    user_id    UUID NOT NULL,
    target_id  UUID NOT NULL,
    label      TEXT NOT NULL CHECK (label IN ('best_friend','family','colleague','classmate','acquaintance')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, target_id)
);

-- Favorites (always-top-feed accounts)
CREATE TABLE IF NOT EXISTS favorites (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, target_id)
);
CREATE INDEX IF NOT EXISTS idx_favorites_user ON favorites(user_id);
