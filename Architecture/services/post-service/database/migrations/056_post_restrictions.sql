-- 056: case-specific post restrictions (Copyright Match plan, section 6.2;
-- prerequisites P-1, P-2, P-4).
--
-- A restriction is a hold that ONE case places on ONE post. It is never
-- written into posts.review_status: the base status stays whatever the
-- moderation authority (033) says, and a post's viewer-facing eligibility
-- becomes "base approved AND no active restriction", read through the
-- generated column effective_review_status. Releasing one case's hold
-- touches only that case's row; two independent copyright cases and a
-- (reserved) safety case on the same post never interfere.
--
-- Expand-only: new tables, additive columns with defaults.

CREATE TABLE IF NOT EXISTS post_restrictions (
    restriction_id    UUID PRIMARY KEY,
    post_id           UUID NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
    source            TEXT NOT NULL CHECK (source IN ('copyright', 'safety')),   -- 'safety' reserved; refused in v1
    case_id           UUID NOT NULL,
    issuer            TEXT NOT NULL CHECK (issuer = 'trust-safety-service'),
    scope             TEXT NOT NULL DEFAULT 'global' CHECK (scope IN ('global')), -- country scope is decision L-13
    state             TEXT NOT NULL CHECK (state IN ('active', 'released')),
    case_revision     BIGINT NOT NULL CHECK (case_revision > 0),
    last_decision_id  UUID NOT NULL,
    policy_version    TEXT NOT NULL,
    reason_code       TEXT NOT NULL,
    first_placed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    placed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),   -- the latest placement
    released_at       TIMESTAMPTZ,                          -- the latest release; NULL while active
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (post_id, source, case_id)
);

CREATE INDEX IF NOT EXISTS idx_post_restrictions_active ON post_restrictions (post_id) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS idx_post_restrictions_case   ON post_restrictions (source, case_id);
CREATE INDEX IF NOT EXISTS idx_post_restrictions_sync   ON post_restrictions (source, updated_at, restriction_id);

-- Append-only audit of every restriction command post-service accepted,
-- keyed by the decision_id trust-safety minted (the idempotency key). A
-- replay of the same decision returns the outcome recorded here.
CREATE TABLE IF NOT EXISTS post_restriction_events (
    decision_id                     UUID PRIMARY KEY,
    restriction_id                  UUID NOT NULL REFERENCES post_restrictions(restriction_id),
    post_id                         UUID NOT NULL,
    action                          TEXT NOT NULL CHECK (action IN ('place_hold', 'release_hold')),
    case_revision                   BIGINT NOT NULL,
    prev_state                      TEXT CHECK (prev_state IN ('active', 'released')),
    new_state                       TEXT NOT NULL CHECK (new_state IN ('active', 'released')),
    actor_id                        UUID NOT NULL,
    policy_version                  TEXT NOT NULL,
    reason_code                     TEXT NOT NULL,
    claims_digest                   BYTEA NOT NULL,       -- sha256 of the canonical claims minus issued/expires
    changed                         BOOLEAN NOT NULL,
    active_restriction_count_after  INT NOT NULL,         -- the outcome, so a replay answers exactly as the original did
    effective_review_status_after   TEXT NOT NULL,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS idx_post_restriction_events_restriction
    ON post_restriction_events (restriction_id, created_at);

CREATE OR REPLACE FUNCTION post_restriction_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'post_restriction_events is append-only (% refused)', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

DROP TRIGGER IF EXISTS trg_post_restriction_events_append_only ON post_restriction_events;
CREATE TRIGGER trg_post_restriction_events_append_only
    BEFORE UPDATE OR DELETE ON post_restriction_events
    FOR EACH ROW EXECUTE FUNCTION post_restriction_events_append_only();

DROP TRIGGER IF EXISTS trg_post_restriction_events_no_truncate ON post_restriction_events;
CREATE TRIGGER trg_post_restriction_events_no_truncate
    BEFORE TRUNCATE ON post_restriction_events
    FOR EACH STATEMENT EXECUTE FUNCTION post_restriction_events_append_only();

-- The eligibility projection on posts. active_restriction_count is
-- maintained by the restriction command inside its transaction (a recount
-- of the active rows, never ±1), and effective_review_status is generated
-- from it so no writer can set it directly.
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS active_restriction_count INT NOT NULL DEFAULT 0
        CHECK (active_restriction_count >= 0);

ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS effective_review_status TEXT GENERATED ALWAYS AS
        (CASE WHEN active_restriction_count > 0 THEN 'restricted' ELSE review_status END) STORED;

-- Deferred consistency check: at commit, a post's counter must equal its
-- number of active restriction rows. Fires on every restriction row change
-- and on every change to the counter itself, so a writer that forgets the
-- recount (or a hand edit) fails instead of publishing a held video.
CREATE OR REPLACE FUNCTION post_restrictions_assert_count() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target UUID;
    stored INT;
    actual INT;
BEGIN
    IF TG_TABLE_NAME = 'posts' THEN
        target := NEW.id;
    ELSIF TG_OP = 'DELETE' THEN
        target := OLD.post_id;
    ELSE
        target := NEW.post_id;
    END IF;
    SELECT active_restriction_count INTO stored FROM posts WHERE id = target;
    IF stored IS NULL THEN
        RETURN NULL; -- the post itself is gone (purge); nothing to assert
    END IF;
    SELECT COUNT(*) INTO actual FROM post_restrictions WHERE post_id = target AND state = 'active';
    IF stored <> actual THEN
        RAISE EXCEPTION 'posts.active_restriction_count for % is % but % restriction(s) are active', target, stored, actual
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS trg_post_restrictions_assert_count ON post_restrictions;
CREATE CONSTRAINT TRIGGER trg_post_restrictions_assert_count
    AFTER INSERT OR UPDATE OR DELETE ON post_restrictions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION post_restrictions_assert_count();

DROP TRIGGER IF EXISTS trg_posts_assert_restriction_count ON posts;
CREATE CONSTRAINT TRIGGER trg_posts_assert_restriction_count
    AFTER UPDATE OF active_restriction_count ON posts
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION post_restrictions_assert_count();
