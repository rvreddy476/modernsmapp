-- Migration 072: the Action button (pinned contract 23 Oct 2026).
--
-- A post (any content type) or a profile carries at most one labelled
-- external link — "Apply now · epfindia.gov.in". Viewers never get the raw
-- URL on a feed row: they get /l/<id>, which resolves through
-- GET /v1/links/:id (active links only) and counts the tap.
--
--   status         active        shown to viewers
--                  blocked       hidden; the author sees why (status_reason)
--                  under_review  hidden until a moderator restores or blocks
--                  removed       replaced or deleted by the author; history
--   status_reason  unsafe        the safety recheck found it on a threat list
--                  moderator     a moderator blocked it
--                  reports       3 distinct scam_link reporters in 7 days
--   safety_status  unchecked     no safety checker was configured (dev only;
--                                production refuses to add such a link)
--                  safe / unsafe Google Web Risk's last verdict
--
-- One live (non-removed) link per post and per profile; replacing the URL
-- marks the old row removed and inserts a new id, so a shared /l/<old id>
-- stops working. A label-only change keeps the id.
--
-- action_link_clicks_daily feeds clicks_7d; action_link_reports is the
-- distinct-reporter ledger behind the under_review threshold (trust-safety
-- ReportFiled, reason scam_link). Both are keyed by link, so a replaced link
-- starts from zero.
--
-- Idempotent.

CREATE TABLE IF NOT EXISTS action_links (
    id                    UUID PRIMARY KEY,
    post_id               UUID REFERENCES posts(id) ON DELETE CASCADE,
    profile_owner_id      UUID,
    author_id             UUID NOT NULL,
    url                   TEXT NOT NULL,
    domain                TEXT NOT NULL,
    label                 TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'active',
    status_reason         TEXT,
    safety_status         TEXT NOT NULL DEFAULT 'unchecked',
    safety_checked_at     TIMESTAMPTZ,
    recheck_claimed_until TIMESTAMPTZ,
    moderated_at          TIMESTAMPTZ,
    moderated_by          UUID,
    clicks_total          BIGINT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_at            TIMESTAMPTZ
);

-- Exactly one owner: a post, or a profile (whose owner is the author).
ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_owner;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_owner
    CHECK ((post_id IS NULL) <> (profile_owner_id IS NULL)
       AND (profile_owner_id IS NULL OR profile_owner_id = author_id));

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_url;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_url
    CHECK (url LIKE 'https://%' AND char_length(url) <= 500);

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_domain;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_domain
    CHECK (char_length(domain) BETWEEN 3 AND 253 AND domain = lower(domain) AND domain NOT LIKE 'www.%');

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_label;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_label
    CHECK (label IN ('apply_now', 'learn_more', 'sign_up', 'register', 'book_now', 'shop_now',
                     'order_now', 'download', 'contact_us', 'visit_website', 'watch_more'));

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_status;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_status
    CHECK (status IN ('active', 'blocked', 'under_review', 'removed'));

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_status_reason;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_status_reason
    CHECK (status_reason IS NULL OR status_reason IN ('unsafe', 'moderator', 'reports'));

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_safety;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_safety
    CHECK (safety_status IN ('unchecked', 'safe', 'unsafe'));

ALTER TABLE action_links DROP CONSTRAINT IF EXISTS chk_action_links_clicks;
ALTER TABLE action_links ADD CONSTRAINT chk_action_links_clicks
    CHECK (clicks_total >= 0);

-- One live link per post and per profile.
CREATE UNIQUE INDEX IF NOT EXISTS uq_action_links_post_live
    ON action_links (post_id) WHERE post_id IS NOT NULL AND status <> 'removed';
CREATE UNIQUE INDEX IF NOT EXISTS uq_action_links_profile_live
    ON action_links (profile_owner_id) WHERE profile_owner_id IS NOT NULL AND status <> 'removed';
-- Stats over every link a post or profile ever had.
CREATE INDEX IF NOT EXISTS idx_action_links_post ON action_links (post_id) WHERE post_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_action_links_profile ON action_links (profile_owner_id) WHERE profile_owner_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_action_links_author ON action_links (author_id);
-- The daily recheck's queue: oldest verdict first, live links only.
CREATE INDEX IF NOT EXISTS idx_action_links_recheck
    ON action_links (safety_checked_at NULLS FIRST)
    WHERE status IN ('active', 'under_review');
-- The moderation queue.
CREATE INDEX IF NOT EXISTS idx_action_links_status_updated
    ON action_links (status, updated_at DESC)
    WHERE status IN ('under_review', 'blocked');

CREATE TABLE IF NOT EXISTS action_link_clicks_daily (
    link_id UUID   NOT NULL REFERENCES action_links(id) ON DELETE CASCADE,
    day     DATE   NOT NULL,
    clicks  BIGINT NOT NULL DEFAULT 0 CHECK (clicks >= 0),
    PRIMARY KEY (link_id, day)
);

-- One row per (link, reporter): a redelivered ReportFiled, or a second
-- report by the same person, never counts twice. reported_at is that
-- reporter's latest scam_link report on the link.
CREATE TABLE IF NOT EXISTS action_link_reports (
    link_id     UUID        NOT NULL REFERENCES action_links(id) ON DELETE CASCADE,
    reporter_id UUID        NOT NULL,
    report_id   UUID        NOT NULL,
    reported_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (link_id, reporter_id)
);
CREATE INDEX IF NOT EXISTS idx_action_link_reports_reporter ON action_link_reports (reporter_id);

-- A reel / long-video studio draft remembers its Action button; the URL
-- rules are checked when it is saved, eligibility and the safety check when
-- it publishes (CreatePost).
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS action_label TEXT;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS action_url   TEXT;

-- A moderator's restore / block of an action link is audited like the other
-- admin writes (post_admin_audit, migration 049), in the same transaction.
-- The two column CHECKs were declared inline in 049, so they carry
-- PostgreSQL's default names. Guarded so a schema built without 049 (store
-- fixtures) can still apply this file; every real database has the table.
DO $$
BEGIN
    IF to_regclass('post_admin_audit') IS NOT NULL THEN
        ALTER TABLE post_admin_audit DROP CONSTRAINT IF EXISTS post_admin_audit_action_check;
        ALTER TABLE post_admin_audit ADD CONSTRAINT post_admin_audit_action_check
            CHECK (action IN ('report.review', 'comment.moderate', 'action_link.review'));
        ALTER TABLE post_admin_audit DROP CONSTRAINT IF EXISTS post_admin_audit_target_type_check;
        ALTER TABLE post_admin_audit ADD CONSTRAINT post_admin_audit_target_type_check
            CHECK (target_type IN ('content_report', 'comment', 'action_link'));
    END IF;
END
$$;
