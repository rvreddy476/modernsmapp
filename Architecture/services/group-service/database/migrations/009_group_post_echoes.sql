CREATE TABLE IF NOT EXISTS group_post_echoes (
    id UUID PRIMARY KEY,
    post_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    echo_type VARCHAR(20) NOT NULL DEFAULT 'share',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(post_id, user_id)
);
