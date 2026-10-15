-- Migration 063: the Postgres reactions table accepts 'like', and becomes
-- the per-viewer record GET /v1/reels/liked is read from.
--
-- Why: the liked-reels list read Scylla user_reel_likes, which nothing
-- wrote, so the list was always empty. The like paths write different
-- stores (Scylla reactions_by_post for the heart state, Redis for the
-- /like toggle, Scylla reel_reactions for /v1/reels/:id/react) and none of
-- them can be listed by viewer. Every like path now also writes this
-- table (service/liked_reels.go), whose idx_reactions_user index answers
-- "my likes, newest first".
--
-- Migration 010 replaced the type check with the atpost vocabulary and
-- left 'like' out, although ValidReactionTypes accepts it: POST
-- /v1/posts/:id/react with "like" failed its INSERT. The check is now the
-- union of both vocabularies. Safe to run again.

ALTER TABLE reactions DROP CONSTRAINT IF EXISTS reactions_reaction_type_check;
ALTER TABLE reactions ADD CONSTRAINT reactions_reaction_type_check
    CHECK (reaction_type IN ('like','spark','supernova','tune','love','haha','wow','sad','angry'));
