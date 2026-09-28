-- Migration 054: end screens and cards that viewers see (MTube, 2026-09-29).
--
-- Additive on 012's video_end_screens / video_cards:
--
--   video_end_screens.video_mode  'specific' (target_id is a post) | 'latest'
--                                 | 'popular' (resolved at read time from the
--                                 channel's own public long videos)
--   video_end_screens.type        adds 'channel' (a channel other than the
--                                 video's own; channel_subscribe stays the
--                                 video's own channel)
--   end_screen_stats / card_stats per element per UTC day: impressions and
--                                 clicks, one of each per viewer per day
--                                 (deduped before the upsert). The owner's
--                                 GET sums the last 28 days.
--
-- position stays JSONB; its shape becomes {x, y, w} (fractions of the 16:9
-- frame). Rows still holding the old {slot:n} are read as that slot's default
-- place and rewritten on the next save, so no data migration is needed here.
--
-- Stats rows go with their element (ON DELETE CASCADE) and with the post.
-- Saving the same element again keeps its id (the save echoes it), so an
-- edit does not reset the editor's click rate.

ALTER TABLE video_end_screens ADD COLUMN IF NOT EXISTS video_mode TEXT NOT NULL DEFAULT 'specific';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'video_end_screens_video_mode_check') THEN
        ALTER TABLE video_end_screens ADD CONSTRAINT video_end_screens_video_mode_check
            CHECK (video_mode IN ('specific', 'latest', 'popular'));
    END IF;
END $$;

ALTER TABLE video_end_screens DROP CONSTRAINT IF EXISTS video_end_screens_type_check;
ALTER TABLE video_end_screens ADD CONSTRAINT video_end_screens_type_check
    CHECK (type IN ('video', 'playlist', 'channel_subscribe', 'channel', 'external_link'));

CREATE TABLE IF NOT EXISTS end_screen_stats (
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    element_id  UUID NOT NULL REFERENCES video_end_screens(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    impressions INT  NOT NULL DEFAULT 0,
    clicks      INT  NOT NULL DEFAULT 0,
    PRIMARY KEY (element_id, day)
);
CREATE INDEX IF NOT EXISTS idx_end_screen_stats_post_day ON end_screen_stats (post_id, day);

CREATE TABLE IF NOT EXISTS card_stats (
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    card_id     UUID NOT NULL REFERENCES video_cards(id) ON DELETE CASCADE,
    day         DATE NOT NULL,
    impressions INT  NOT NULL DEFAULT 0,
    clicks      INT  NOT NULL DEFAULT 0,
    PRIMARY KEY (card_id, day)
);
CREATE INDEX IF NOT EXISTS idx_card_stats_post_day ON card_stats (post_id, day);
