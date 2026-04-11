-- =============================================================================
-- QA-SERVICE — Q&A domain schema
-- Bootstrap on startup via embed.go → BootstrapSchema
-- =============================================================================

-- ---------------------------------------------------------------------------
-- Core: Profiles, Topics, Questions, Answers, Comments
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS qa_profiles (
    user_id       UUID PRIMARY KEY,
    display_name  TEXT NOT NULL DEFAULT '',
    bio           TEXT NOT NULL DEFAULT '',
    expertise_areas TEXT[] NOT NULL DEFAULT '{}',
    reputation_score INT NOT NULL DEFAULT 0,
    question_count   INT NOT NULL DEFAULT 0,
    answer_count     INT NOT NULL DEFAULT 0,
    best_answer_count INT NOT NULL DEFAULT 0,
    is_verified    BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS topics (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    slug            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    icon_url        TEXT NOT NULL DEFAULT '',
    parent_topic_id UUID REFERENCES topics(id) ON DELETE SET NULL,
    question_count  INT NOT NULL DEFAULT 0,
    follower_count  INT NOT NULL DEFAULT 0,
    is_featured     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Idempotent migrations: add columns that may be missing from earlier schema versions
ALTER TABLE topics ADD COLUMN IF NOT EXISTS description    TEXT    NOT NULL DEFAULT '';
ALTER TABLE topics ADD COLUMN IF NOT EXISTS icon_url       TEXT    NOT NULL DEFAULT '';
ALTER TABLE topics ADD COLUMN IF NOT EXISTS question_count INT     NOT NULL DEFAULT 0;
ALTER TABLE topics ADD COLUMN IF NOT EXISTS follower_count INT     NOT NULL DEFAULT 0;
ALTER TABLE topics ADD COLUMN IF NOT EXISTS is_featured    BOOLEAN NOT NULL DEFAULT false;
-- Alias parent_topic_id → parent_id for stores that use the canonical column name
ALTER TABLE topics ADD COLUMN IF NOT EXISTS parent_topic_id UUID REFERENCES topics(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS qa_communities (
    id                     UUID PRIMARY KEY,
    owner_id               UUID NOT NULL,
    name                   TEXT NOT NULL,
    community_type         TEXT NOT NULL DEFAULT 'public',
    status                 TEXT NOT NULL DEFAULT 'active',
    qa_question_count      INT NOT NULL DEFAULT 0,
    qa_answer_count        INT NOT NULL DEFAULT 0,
    qa_contributor_count   INT NOT NULL DEFAULT 0,
    last_qa_activity_at    TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at             TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS qa_community_members (
    community_id UUID NOT NULL REFERENCES qa_communities(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    role         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (community_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_qa_settings (
    community_id               UUID PRIMARY KEY REFERENCES qa_communities(id) ON DELETE CASCADE,
    qa_enabled                 BOOLEAN NOT NULL DEFAULT true,
    ask_permission             TEXT NOT NULL DEFAULT 'members',
    answer_permission          TEXT NOT NULL DEFAULT 'everyone',
    auto_suggest_topics        BOOLEAN NOT NULL DEFAULT true,
    suggested_topic_ids        UUID[] NOT NULL DEFAULT '{}',
    require_approval           BOOLEAN NOT NULL DEFAULT false,
    welcome_message            TEXT NOT NULL DEFAULT '',
    total_questions_count      INT NOT NULL DEFAULT 0,
    total_answers_count        INT NOT NULL DEFAULT 0,
    unique_contributors_count  INT NOT NULL DEFAULT 0,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS topic_aliases (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic_id  UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    alias     TEXT NOT NULL UNIQUE,
    language  TEXT NOT NULL DEFAULT 'en'
);

CREATE TABLE IF NOT EXISTS questions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id       UUID NOT NULL,
    community_id    UUID REFERENCES qa_communities(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    body_html       TEXT NOT NULL DEFAULT '',
    slug            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'open',
    visibility      TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public','unlisted')),
    language        TEXT NOT NULL DEFAULT 'en',
    vote_score      INT NOT NULL DEFAULT 0,
    upvote_count    INT NOT NULL DEFAULT 0,
    downvote_count  INT NOT NULL DEFAULT 0,
    answer_count    INT NOT NULL DEFAULT 0,
    view_count      INT NOT NULL DEFAULT 0,
    follow_count    INT NOT NULL DEFAULT 0,
    is_answered     BOOLEAN NOT NULL DEFAULT false,
    best_answer_id  UUID,
    closed_reason   TEXT,
    closed_by       UUID,
    merged_into_id  UUID REFERENCES questions(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

ALTER TABLE questions ADD COLUMN IF NOT EXISTS community_id UUID REFERENCES qa_communities(id) ON DELETE SET NULL;
ALTER TABLE questions DROP CONSTRAINT IF EXISTS questions_status_check;
ALTER TABLE questions
    ADD CONSTRAINT questions_status_check
    CHECK (status IN ('open','closed','merged','deleted','pending_approval'));

CREATE TABLE IF NOT EXISTS question_topics (
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    topic_id    UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    PRIMARY KEY (question_id, topic_id)
);

CREATE TABLE IF NOT EXISTS question_tags (
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    tag         TEXT NOT NULL,
    PRIMARY KEY (question_id, tag)
);

CREATE TABLE IF NOT EXISTS question_media (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    media_id    UUID NOT NULL,
    sort_order  INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS answers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id     UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    author_id       UUID NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    body_html       TEXT NOT NULL DEFAULT '',
    vote_score      INT NOT NULL DEFAULT 0,
    upvote_count    INT NOT NULL DEFAULT 0,
    downvote_count  INT NOT NULL DEFAULT 0,
    is_best         BOOLEAN NOT NULL DEFAULT false,
    is_accepted     BOOLEAN NOT NULL DEFAULT false,
    comment_count   INT NOT NULL DEFAULT 0,
    reference_count INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS answer_media (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    answer_id UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    media_id  UUID NOT NULL,
    sort_order INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS answer_references (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    answer_id   UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    sort_order  INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS answer_comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    answer_id  UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    author_id  UUID NOT NULL,
    body       TEXT NOT NULL,
    vote_score INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

-- ---------------------------------------------------------------------------
-- Engagement: Follows, Votes, Saves, Answer Requests
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS question_follows (
    user_id     UUID NOT NULL,
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, question_id)
);

CREATE TABLE IF NOT EXISTS topic_follows (
    user_id    UUID NOT NULL,
    topic_id   UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, topic_id)
);

CREATE TABLE IF NOT EXISTS contributor_follows (
    follower_id UUID NOT NULL,
    followed_id UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followed_id)
);

CREATE TABLE IF NOT EXISTS question_votes (
    user_id     UUID NOT NULL,
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    vote_type   TEXT NOT NULL CHECK (vote_type IN ('up','down')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, question_id)
);

CREATE TABLE IF NOT EXISTS answer_votes (
    user_id   UUID NOT NULL,
    answer_id UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    vote_type TEXT NOT NULL CHECK (vote_type IN ('up','down')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, answer_id)
);

CREATE TABLE IF NOT EXISTS answer_comment_votes (
    user_id    UUID NOT NULL,
    comment_id UUID NOT NULL REFERENCES answer_comments(id) ON DELETE CASCADE,
    vote_type  TEXT NOT NULL CHECK (vote_type IN ('up','down')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, comment_id)
);

CREATE TABLE IF NOT EXISTS question_saves (
    user_id     UUID NOT NULL,
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, question_id)
);

CREATE TABLE IF NOT EXISTS answer_saves (
    user_id   UUID NOT NULL,
    answer_id UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, answer_id)
);

CREATE TABLE IF NOT EXISTS answer_requests (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id       UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    requester_id      UUID NOT NULL,
    requested_user_id UUID NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','declined','expired')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS question_community_context (
    question_id                   UUID PRIMARY KEY REFERENCES questions(id) ON DELETE CASCADE,
    community_id                  UUID REFERENCES qa_communities(id) ON DELETE SET NULL,
    community_name_snapshot       TEXT NOT NULL DEFAULT '',
    community_visibility          TEXT NOT NULL DEFAULT 'public',
    is_pinned                     BOOLEAN NOT NULL DEFAULT false,
    pinned_at                     TIMESTAMPTZ,
    pinned_by_user_id             UUID,
    community_moderation_status   TEXT NOT NULL DEFAULT 'active',
    community_moderation_notes    TEXT NOT NULL DEFAULT '',
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS community_topic_affinity (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id   UUID NOT NULL REFERENCES qa_communities(id) ON DELETE CASCADE,
    topic_id       UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    question_count INT NOT NULL DEFAULT 0,
    answer_count   INT NOT NULL DEFAULT 0,
    view_count     INT NOT NULL DEFAULT 0,
    recency_score  NUMERIC(10,4) NOT NULL DEFAULT 0.0,
    affinity_score NUMERIC(10,4) NOT NULL DEFAULT 0.0,
    last_question_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (community_id, topic_id)
);

-- ---------------------------------------------------------------------------
-- System: Duplicates, Reputation, Badges, Verification, Moderation, Scopes
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS question_duplicates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id     UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    duplicate_of_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    marked_by       UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reputation_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    event_type  TEXT NOT NULL,
    points      INT NOT NULL,
    source_type TEXT NOT NULL DEFAULT '',
    source_id   UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS contributor_badges (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    badge_type TEXT NOT NULL,
    badge_name TEXT NOT NULL,
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS verification_profiles (
    user_id     UUID PRIMARY KEY,
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_by UUID,
    credentials JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS moderation_reports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id UUID NOT NULL,
    target_type TEXT NOT NULL CHECK (target_type IN ('question','answer','comment','user')),
    target_id   UUID NOT NULL,
    reason      TEXT NOT NULL,
    details     TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','reviewed','resolved','dismissed')),
    reviewed_by UUID,
    resolved_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS moderation_actions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id   UUID REFERENCES moderation_reports(id) ON DELETE SET NULL,
    actor_id    UUID NOT NULL,
    action_type TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   UUID NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS question_translations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    language    TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS answer_translations (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    answer_id UUID NOT NULL REFERENCES answers(id) ON DELETE CASCADE,
    language  TEXT NOT NULL,
    body      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS local_scopes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id   UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    latitude      DOUBLE PRECISION NOT NULL,
    longitude     DOUBLE PRECISION NOT NULL,
    radius_km     INT NOT NULL DEFAULT 50,
    location_name TEXT NOT NULL DEFAULT ''
);

-- ---------------------------------------------------------------------------
-- Indexes
-- ---------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_questions_author       ON questions(author_id);
CREATE INDEX IF NOT EXISTS idx_questions_status        ON questions(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_questions_vote_score    ON questions(vote_score DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_questions_created       ON questions(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_questions_slug          ON questions(slug) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_questions_unanswered    ON questions(created_at DESC) WHERE answer_count = 0 AND status = 'open' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_questions_community_status ON questions(community_id, status, created_at DESC) WHERE community_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_questions_global_status ON questions(status, created_at DESC) WHERE community_id IS NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_answers_question        ON answers(question_id, created_at);
CREATE INDEX IF NOT EXISTS idx_answers_author          ON answers(author_id);
CREATE INDEX IF NOT EXISTS idx_answers_vote_score      ON answers(question_id, vote_score DESC);

CREATE INDEX IF NOT EXISTS idx_answer_comments_answer  ON answer_comments(answer_id, created_at);

CREATE INDEX IF NOT EXISTS idx_reputation_user         ON reputation_events(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_topics_slug             ON topics(slug);
CREATE INDEX IF NOT EXISTS idx_topics_featured         ON topics(is_featured) WHERE is_featured = true;

CREATE INDEX IF NOT EXISTS idx_question_topics_topic   ON question_topics(topic_id);
CREATE INDEX IF NOT EXISTS idx_moderation_reports_status ON moderation_reports(status, created_at);

CREATE INDEX IF NOT EXISTS idx_answer_requests_user    ON answer_requests(requested_user_id, status);
CREATE INDEX IF NOT EXISTS idx_local_scopes_question   ON local_scopes(question_id);
CREATE INDEX IF NOT EXISTS idx_contributor_badges_user ON contributor_badges(user_id);
CREATE INDEX IF NOT EXISTS idx_qa_communities_status ON qa_communities(status, last_qa_activity_at DESC);
CREATE INDEX IF NOT EXISTS idx_qa_community_members_user ON qa_community_members(user_id, community_id);
CREATE INDEX IF NOT EXISTS idx_qcc_community_pinned ON question_community_context(community_id, is_pinned, created_at DESC) WHERE is_pinned = true;
CREATE INDEX IF NOT EXISTS idx_cta_community_affinity ON community_topic_affinity(community_id, affinity_score DESC, question_count DESC);
