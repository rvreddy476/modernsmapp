-- 081: tube / long-video data integrity (2026-10-30, tube backend audit).
--
-- H2  A series is its creator's own videos. AddEpisodeToVideoSeries used to
--     check only that the caller owned the SERIES, so anyone could add a
--     stranger's popular video as an episode of their own series, and the
--     newest series won that video's watch-page panel (title, "Episode N"
--     label, Next button). The add now refuses it and the reads skip such
--     rows; the rows already written are deleted here (an episode row is a
--     link, not the video: the video is untouched), and episode_count is
--     recomputed for every series that lost one.
--
-- M3  A series cover is the creator's own image, and never blocks its
--     owner's delete. Covers naming someone else's upload are cleared, and
--     the foreign key becomes ON DELETE SET NULL, like a post losing a
--     cover: deleting the asset now clears the series cover instead of
--     being refused because a series still points at it.
--
-- M1  A playlist lists a video once, at a position nobody else holds.
--     AddPlaylistItem used to overwrite on (playlist_id, position) and
--     accepted any client position, negative included, and duplicates of
--     one post. Duplicates are removed (the earliest position stays), every
--     playlist is renumbered 0..n-1 in its current order, and a unique index
--     on (playlist_id, post_id) backs the add's own check.
--
-- L3  watch_progress.client_sent_at orders progress reports, so a late
--     heartbeat cannot move the resume point backwards.
--
-- L4  A playlist cover_url is platform media (/v1/media/<id>/serve of the
--     owner's own image), never an arbitrary URL every end-screen viewer
--     would fetch. Covers that are not are cleared (the card then borrows
--     the first item's thumbnail, as a playlist without a cover always did).

-- ── H2 ─────────────────────────────────────────────────────────────────────
WITH foreign_rows AS (
    DELETE FROM video_series_episodes e
    USING video_series vs, posts p
    WHERE vs.id = e.series_id
      AND p.id = e.post_id
      AND p.author_id <> vs.creator_id
    RETURNING e.series_id
)
UPDATE video_series vs
SET episode_count = (SELECT COUNT(*) FROM video_series_episodes WHERE series_id = vs.id),
    updated_at = NOW()
WHERE vs.id IN (SELECT DISTINCT series_id FROM foreign_rows);

-- ── M3 ─────────────────────────────────────────────────────────────────────
UPDATE video_series vs
SET cover_media_id = NULL, updated_at = NOW()
FROM media_assets m
WHERE m.id = vs.cover_media_id
  AND m.uploader_id <> vs.creator_id;

ALTER TABLE video_series DROP CONSTRAINT IF EXISTS video_series_cover_media_id_fkey;
ALTER TABLE video_series ADD CONSTRAINT video_series_cover_media_id_fkey
    FOREIGN KEY (cover_media_id) REFERENCES media_assets(id) ON DELETE SET NULL;

-- ── M1 ─────────────────────────────────────────────────────────────────────
-- The primary key (playlist_id, position) is not deferrable, so the
-- renumbering runs with it dropped and puts it back afterwards.
ALTER TABLE playlist_items DROP CONSTRAINT IF EXISTS playlist_items_pkey;

DELETE FROM playlist_items pi
USING (
    SELECT ctid, ROW_NUMBER() OVER (PARTITION BY playlist_id, post_id ORDER BY position, added_at) AS rn
    FROM playlist_items
) d
WHERE pi.ctid = d.ctid AND d.rn > 1;

UPDATE playlist_items pi
SET position = r.rn - 1
FROM (
    SELECT playlist_id, post_id,
           ROW_NUMBER() OVER (PARTITION BY playlist_id ORDER BY position, added_at, post_id) AS rn
    FROM playlist_items
) r
WHERE pi.playlist_id = r.playlist_id AND pi.post_id = r.post_id AND pi.position <> r.rn - 1;

ALTER TABLE playlist_items ADD CONSTRAINT playlist_items_pkey PRIMARY KEY (playlist_id, position);
CREATE UNIQUE INDEX IF NOT EXISTS uq_playlist_items_playlist_post ON playlist_items (playlist_id, post_id);

UPDATE playlists p
SET item_count = (SELECT COUNT(*) FROM playlist_items WHERE playlist_id = p.id)
WHERE item_count <> (SELECT COUNT(*) FROM playlist_items WHERE playlist_id = p.id);

-- ── L3 ─────────────────────────────────────────────────────────────────────
ALTER TABLE watch_progress ADD COLUMN IF NOT EXISTS client_sent_at TIMESTAMPTZ;

-- ── L4 ─────────────────────────────────────────────────────────────────────
UPDATE playlists
SET cover_url = NULL, updated_at = NOW()
WHERE cover_url IS NOT NULL
  AND btrim(cover_url) <> ''
  AND cover_url !~ '^/v1/media/[0-9a-fA-F-]{36}/serve(/[a-z0-9_-]+)?$';
