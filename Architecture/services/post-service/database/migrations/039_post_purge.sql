-- Soft delete → "Recently deleted" → hard purge (founder decision, 2026-09-04).
--
-- Deleting a post keeps its rows and objects and sets posts.deleted_at; the
-- author can restore it from "Recently deleted" for POST_PURGE_AFTER (30 days
-- in production). After that the purge worker (internal/postpurge) erases
-- the post's rows in one transaction and hands its media to media-service.
--
-- idx_posts_deleted_at: the worker scans "deleted_at <= now - window"
-- oldest-first. Every existing index on posts is partial on
-- `deleted_at IS NULL`, i.e. it excludes exactly the rows the worker needs.
--
-- post_purge_media: the media assets a purged post was the LAST post to
-- reference. The post_media rows that prove that are gone once the purge
-- commits, so the intent is written here first and the worker drains it —
-- a media-service outage after the purge must not leak the objects forever.
-- One row per (media, post); attempts/next_attempt_at back a failed call off.

CREATE INDEX IF NOT EXISTS idx_posts_deleted_at
    ON posts (deleted_at ASC)
    WHERE deleted_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS post_purge_media (
    media_id        UUID        NOT NULL,
    post_id         UUID        NOT NULL,
    owner_id        UUID        NOT NULL,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts        INT         NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_id, post_id)
);

CREATE INDEX IF NOT EXISTS idx_post_purge_media_due
    ON post_purge_media (next_attempt_at ASC);
