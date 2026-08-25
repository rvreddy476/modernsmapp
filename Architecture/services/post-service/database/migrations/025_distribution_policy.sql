-- Module 1 P0-1: canonical distribution policy.
-- Typed, versioned scalar policy: {"version":1,"main_feed":bool,
-- "notify_subscribers":bool,"create_reel_preview":bool}.
-- NULL = no policy = legacy behavior (post appears in social home, as all
-- posts did before this migration). posts.visibility stays canonical and is
-- NOT duplicated into the JSON. Explicit group/community destinations stay
-- normalized in crosspost_links. distribution_rev increases monotonically on
-- every policy write so event consumers can reject stale updates.
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS distribution     JSONB,
    ADD COLUMN IF NOT EXISTS distribution_rev BIGINT NOT NULL DEFAULT 0;
