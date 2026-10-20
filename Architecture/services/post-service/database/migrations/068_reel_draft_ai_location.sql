-- Migration 068: reel drafts carry the AI-generated label and the tagged
-- place (2026-10-21, upload studio after a walk of TikTok Studio).
--
-- The post row has had ai_generated and location_name / location_place_id
-- since migration 066, and a direct create sends them. A studio upload that
-- was saved as a draft first publishes FROM the draft row, so without these
-- columns the label and the place were dropped on the way to the post.
-- PublishDraft and the scheduled publisher copy them (service/drafts.go).

ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS ai_generated      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS location_name     TEXT;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS location_place_id TEXT;
