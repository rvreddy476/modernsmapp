-- Migration 044: one content_type constraint, so two shipped features stop
-- being impossible.
--
-- ─── What was wrong ───────────────────────────────────────────────────
--
-- `posts` carried TWO CHECK constraints on content_type, and a row must
-- satisfy every CHECK on its table. So the set of values actually
-- insertable was their INTERSECTION, not their union — and each one was
-- missing a value the other allowed:
--
--   chk_content_type        … 'video_embed', 'voice'
--   chk_posts_content_type  … 'video_embed', 'flick_embed'
--
--   insertable  = post, poll, reel, video, flick, long_video, video_embed
--   IMPOSSIBLE  = voice        (absent from chk_posts_content_type)
--                 flick_embed  (absent from chk_content_type)
--
-- Both of those are values the CODE actively writes:
--
--   flick_embed — store/postgres/crosspost_links.go:51 sets it when the
--                 source post is a flick or a reel, so CROSSPOSTING A
--                 FLICK fails on a constraint violation. Crossposting a
--                 long video works, because it writes 'video_embed',
--                 which is in both lists. That asymmetry is why nobody
--                 noticed: the feature works for exactly half its inputs.
--
--   voice       — service/post.go:602 lists it as a valid content type
--                 and isVoiceContentType() branches on it (Module 1
--                 P0-6, a voice-only post). Every one of those inserts
--                 fails.
--
-- ─── How it happened ──────────────────────────────────────────────────
--
-- 003_content_type_reel_video created chk_content_type, correctly
-- dropping and re-adding it. 009_crosspost_v2 then added flick_embed —
-- but under a NEW name, and its DROP named `chk_posts_content_type` and
-- `posts_content_type_check`, i.e. itself and a guess, never the
-- constraint that actually existed. So instead of replacing 003's
-- constraint it sat alongside it, and the AND of the two silently
-- excluded the value 009 was written to add. A later migration then
-- added 'voice' to 003's list only, which broke the second feature the
-- same way.
--
-- The lesson is in the shape, not the values: `DROP CONSTRAINT IF EXISTS`
-- under a different name from the one you are adding does not replace
-- anything. It accumulates.
--
-- ─── The fix ──────────────────────────────────────────────────────────
--
-- One constraint, holding the UNION. chk_content_type keeps the name
-- because it is the older and canonical one; chk_posts_content_type is
-- dropped rather than widened, so a third name can never appear.
--
-- Idempotent, and safe to run against a database where either, both or
-- neither exists.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_content_type;
ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_content_type_check;
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_content_type;

ALTER TABLE posts ADD CONSTRAINT chk_content_type
    CHECK (content_type IN (
        -- ordinary posts
        'post', 'poll', 'voice',
        -- video, current vocabulary
        'flick', 'long_video',
        -- video, legacy synonyms kept because rows still carry them;
        -- shared/postclassify treats reel→flick and video→long_video
        'reel', 'video',
        -- crosspost embeds, written by CreateCrosspostLink
        'video_embed', 'flick_embed'
    ));

-- A row that violates the new constraint cannot exist — the new set is a
-- strict superset of what was insertable before — so no data repair is
-- needed and none is attempted.
