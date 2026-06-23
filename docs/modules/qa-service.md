# Module: qa-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /answers/:answerId
DELETE /answers/:answerId/save
DELETE /answers/:answerId/vote
DELETE /comments/:commentId
DELETE /comments/:commentId/vote
DELETE /:communityId/questions/:questionId/pin
DELETE /contributors/:userId/follow
DELETE /drafts/answers/:draftId
DELETE /drafts/questions/:draftId
DELETE /questions/:questionId
DELETE /questions/:questionId/best-answer
DELETE /questions/:questionId/follow
DELETE /questions/:questionId/save
DELETE /questions/:questionId/vote
DELETE /topics/:topicId/follow
GET /actions
GET /answer-requests
GET /answers/:answerId
GET /answers/:answerId/comments
GET /:communityId/qa-settings
GET /:communityId/questions
GET /:communityId/topics/popular
GET /drafts/answers
GET /drafts/questions
GET /feed/answer-queue
GET /feed/following
GET /feed/for-you
GET /feed/home
GET /feed/local
GET /feed/trending
GET /feed/unanswered
GET /leaderboard
GET /profile
GET /profile/:userId
GET /profile/:userId/answers
GET /profile/:userId/badges
GET /profile/:userId/questions
GET /profile/:userId/reputation
GET /questions
GET /questions/my
GET /questions/:questionId
GET /questions/:questionId/answers
GET /questions/similar
GET /questions/slug/:slug
GET /reports
GET /reports/:reportId
GET /saved/answers
GET /saved/questions
GET /search
GET /topics
GET /topics/following
GET /topics/slug/:slug
GET /topics/:topicId
GET /topics/:topicId/contributors
GET /topics/:topicId/questions
POST /answer-requests/:requestId/respond
POST /answers/:answerId/comments
POST /answers/:answerId/hide
POST /answers/:answerId/save
POST /answers/:answerId/vote
POST /comments/:commentId/hide
POST /comments/:commentId/vote
POST /:communityId/questions/:questionId/pin
POST /contributors/:userId/follow
POST /drafts/answers
POST /drafts/questions
POST /questions
POST /questions/:questionId/answers
POST /questions/:questionId/best-answer
POST /questions/:questionId/close
POST /questions/:questionId/duplicate
POST /questions/:questionId/follow
POST /questions/:questionId/hide
POST /questions/:questionId/lock
POST /questions/:questionId/merge
POST /questions/:questionId/reopen
POST /questions/:questionId/request-answer
POST /questions/:questionId/save
POST /questions/:questionId/vote
POST /reports
POST /reports/:reportId/dismiss
POST /reports/:reportId/resolve
POST /topics
POST /topics/:topicId/follow
PUT /answers/:answerId
PUT /comments/:commentId
PUT /:communityId/qa-settings
PUT /profile
PUT /questions/:questionId
GROUP /admin
GROUP /communities
GROUP /v1/qa
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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

CREATE TABLE IF NOT EXISTS question_drafts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id     UUID NOT NULL,
    community_id  UUID,
    title         TEXT NOT NULL DEFAULT '',
    body          TEXT NOT NULL DEFAULT '',
    tags          TEXT[] NOT NULL DEFAULT '{}',
    topic_ids     UUID[] NOT NULL DEFAULT '{}',
    is_anonymous  BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS answer_drafts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id   UUID NOT NULL,
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    body        TEXT NOT NULL DEFAULT '',
    is_anonymous BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (author_id, question_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type questionDraftRequest struct {
	ID          *string  `json:"id,omitempty"`
	CommunityID *string  `json:"communityId,omitempty"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags"`
	TopicIDs    []string `json:"topicIds"`
	IsAnonymous bool     `json:"isAnonymous,omitempty"`
}

type answerDraftRequest struct {
	ID          *string `json:"id,omitempty"`
	QuestionID  string  `json:"questionId"`
	Body        string  `json:"body"`
	IsAnonymous bool    `json:"isAnonymous,omitempty"`
}
```
