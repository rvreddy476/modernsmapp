-- Migration 071: a reel / long-video draft remembers its members-only gate
-- (2026-10-23).
--
-- The upload studio created the post and only then PUT the gate
-- (/v1/posts/:postId/membership), so for a moment every members-only post
-- was public: feed fan-out, search and subscriber pushes all saw it open.
-- The gate is now part of the create itself. A draft that is published later
-- (now, or by the schedule worker) must carry it too, so it is stored here
-- and handed to CreatePost at publication, which checks it against the
-- author's active tiers in monetization-service and writes it on the post
-- row in the post's own transaction.
--
-- NULL = open. No foreign key: tiers belong to monetization-service.

ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS tier_required_id UUID;
