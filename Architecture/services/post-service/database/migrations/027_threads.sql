-- Module 1 P0-8: basic multi-post threads.
-- Root posts carry thread_root_id = own id, seq 0; entries chain via
-- thread_reply_to_id and increment thread_seq. Standalone posts leave all
-- three NULL/0. thread_idempotency makes thread creation replay-safe: the
-- same idempotency key always resolves to the same root post.
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS thread_root_id     UUID,
    ADD COLUMN IF NOT EXISTS thread_reply_to_id UUID,
    ADD COLUMN IF NOT EXISTS thread_seq         INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_posts_thread
    ON posts (thread_root_id, thread_seq)
    WHERE thread_root_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS thread_idempotency (
    idempotency_key UUID PRIMARY KEY,
    root_post_id    UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
