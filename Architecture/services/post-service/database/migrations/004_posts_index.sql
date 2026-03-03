CREATE INDEX IF NOT EXISTS idx_posts_author_created
    ON posts(author_id, created_at DESC);
