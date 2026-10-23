-- 073: copyright title review (founder quick wins, 2026-10-24).
--
-- Staff keep a catalogue of film and TV titles. A LONG VIDEO longer than
-- 20 minutes whose title, description or hashtags name a listed title, by an
-- uploader who is not one of that title's verified owner channels, is
-- published but HELD for copyright review: hidden from every viewer except
-- its author, no feed fan-out, no search, no subscriber notifications, until
-- a reviewer clears it (then all of that happens, once) or blocks it (it
-- stays hidden and counts as an upheld copyright block).
--
-- The hold is the case-specific restriction machinery of 056, not a second
-- hiding system: one post_restrictions row with source 'title_review', so
-- posts.effective_review_status reads 'restricted' and every viewer path
-- that already honours restrictions honours this one. Its issuer is
-- post-service itself (the rule runs at publication, inside the publishing
-- transaction), which is why the source is NOT 'copyright': trust-safety's
-- signed restriction command refuses any source but its own, and none of
-- the other review writers (ModeratePost, appeals, resubmit, restore) reads
-- or writes restrictions, so only this file's decision path lifts it.
--
-- Expand-only: new tables, widened CHECKs.

-- 1. The catalogue. Never deleted: an entry is switched off (active=false)
--    so a past review can always name what it matched.
CREATE TABLE IF NOT EXISTS copyright_review_titles (
    id                 UUID PRIMARY KEY,
    title              TEXT NOT NULL CHECK (btrim(title) <> '' AND char_length(title) <= 200),
    aliases            TEXT[] NOT NULL DEFAULT '{}' CHECK (cardinality(aliases) <= 50),
    -- Metadata for the reviewer (the film's language); matching does not
    -- filter on it, because a post's own language defaults to "en".
    language           TEXT CHECK (language IS NULL OR (char_length(language) BETWEEN 2 AND 16)),
    -- Channels verified to own the title: their uploads are never held.
    owner_channel_ids  UUID[] NOT NULL DEFAULT '{}' CHECK (cardinality(owner_channel_ids) <= 100),
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    created_by         UUID NOT NULL,
    updated_by         UUID NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_copyright_review_titles_active
    ON copyright_review_titles (created_at DESC, id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_copyright_review_titles_list
    ON copyright_review_titles (created_at DESC, id);

-- 2. One review per post. state is what the reviewer decided; the hold
--    itself is the post_restrictions row named by restriction_id.
CREATE TABLE IF NOT EXISTS copyright_title_reviews (
    id                   UUID PRIMARY KEY,
    post_id              UUID NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    author_id            UUID NOT NULL,
    restriction_id       UUID NOT NULL UNIQUE,
    title_id             UUID NOT NULL REFERENCES copyright_review_titles(id),
    matched_title        TEXT NOT NULL,          -- the entry's title when it matched
    matched_term         TEXT NOT NULL,          -- the title or alias that hit
    matched_field        TEXT NOT NULL CHECK (matched_field IN ('title', 'description', 'hashtag')),
    duration_seconds     INT CHECK (duration_seconds IS NULL OR duration_seconds >= 0),
    -- The video was still transcoding when it was published, so its length
    -- was unknown: it is held (fail closed) but not queued or announced to
    -- the author until the transcode measures it. Under the threshold the
    -- hold is lifted automatically; over it the review is queued.
    awaiting_duration    BOOLEAN NOT NULL DEFAULT FALSE,
    state                TEXT NOT NULL CHECK (state IN ('pending', 'cleared', 'blocked')),
    -- The publication's PostCreated was withheld; a clear emits it (once:
    -- announced_at is set in the same transaction).
    announce_on_clear    BOOLEAN NOT NULL,
    announced_at         TIMESTAMPTZ,
    -- Whether the author was told of the hold (the clear / block notices
    -- follow only a hold notice).
    author_notified      BOOLEAN NOT NULL DEFAULT FALSE,
    -- "I own the rights": the author's note for the reviewer.
    author_note          TEXT CHECK (author_note IS NULL OR char_length(author_note) <= 1000),
    author_requested_at  TIMESTAMPTZ,
    decided_by           UUID,                   -- NULL with decided_at set = the system (duration rule)
    decided_at           TIMESTAMPTZ,
    decision_note        TEXT CHECK (decision_note IS NULL OR char_length(decision_note) <= 2000),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT copyright_title_reviews_decision_shape CHECK (
        (state = 'pending' AND decided_at IS NULL AND decided_by IS NULL)
        OR (state = 'blocked' AND decided_at IS NOT NULL AND decided_by IS NOT NULL)
        OR (state = 'cleared' AND decided_at IS NOT NULL)),
    CONSTRAINT copyright_title_reviews_awaiting_pending CHECK (NOT awaiting_duration OR state <> 'blocked')
);

-- The reviewer queue: oldest first, measured holds only.
CREATE INDEX IF NOT EXISTS idx_copyright_title_reviews_queue
    ON copyright_title_reviews (created_at, id) WHERE state = 'pending' AND NOT awaiting_duration;
CREATE INDEX IF NOT EXISTS idx_copyright_title_reviews_state
    ON copyright_title_reviews (state, decided_at DESC, id);
CREATE INDEX IF NOT EXISTS idx_copyright_title_reviews_author
    ON copyright_title_reviews (author_id, created_at DESC);

-- 3. Append-only history of every transition (system and human alike).
CREATE TABLE IF NOT EXISTS copyright_title_review_events (
    id          UUID PRIMARY KEY,
    review_id   UUID NOT NULL,
    post_id     UUID NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('hold', 'duration_measured', 'auto_clear', 'author_request', 'clear', 'block')),
    actor_id    UUID,                            -- NULL = the system
    prev_state  TEXT,
    new_state   TEXT NOT NULL,
    note        TEXT CHECK (note IS NULL OR char_length(note) <= 2000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS idx_copyright_title_review_events_review
    ON copyright_title_review_events (review_id, created_at);

CREATE OR REPLACE FUNCTION copyright_title_review_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'copyright_title_review_events is append-only (% refused)', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

DROP TRIGGER IF EXISTS trg_copyright_title_review_events_append_only ON copyright_title_review_events;
CREATE TRIGGER trg_copyright_title_review_events_append_only
    BEFORE UPDATE OR DELETE ON copyright_title_review_events
    FOR EACH ROW EXECUTE FUNCTION copyright_title_review_events_append_only();

-- 4. post_restrictions admits the new source, issued by post-service only.
--    056 declared both CHECKs inline, so they carry PostgreSQL's default
--    names. Re-runnable: drop and re-add.
ALTER TABLE post_restrictions DROP CONSTRAINT IF EXISTS post_restrictions_source_check;
ALTER TABLE post_restrictions ADD CONSTRAINT post_restrictions_source_check
    CHECK (source IN ('copyright', 'safety', 'title_review'));
ALTER TABLE post_restrictions DROP CONSTRAINT IF EXISTS post_restrictions_issuer_check;
ALTER TABLE post_restrictions ADD CONSTRAINT post_restrictions_issuer_check
    CHECK ((source = 'title_review' AND issuer = 'post-service')
        OR (source <> 'title_review' AND issuer = 'trust-safety-service'));

-- 5. The review decisions and catalogue writes are audited like the other
--    admin writes (post_admin_audit, migrations 049 / 072), in the same
--    transaction. Guarded like 072's.
DO $$
BEGIN
    IF to_regclass('post_admin_audit') IS NOT NULL THEN
        ALTER TABLE post_admin_audit DROP CONSTRAINT IF EXISTS post_admin_audit_action_check;
        ALTER TABLE post_admin_audit ADD CONSTRAINT post_admin_audit_action_check
            CHECK (action IN ('report.review', 'comment.moderate', 'action_link.review',
                              'copyright_title.create', 'copyright_title.update', 'copyright_review.decide'));
        ALTER TABLE post_admin_audit DROP CONSTRAINT IF EXISTS post_admin_audit_target_type_check;
        ALTER TABLE post_admin_audit ADD CONSTRAINT post_admin_audit_target_type_check
            CHECK (target_type IN ('content_report', 'comment', 'action_link', 'copyright_title', 'copyright_review'));
    END IF;
END
$$;
