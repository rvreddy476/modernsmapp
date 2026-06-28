# Module: memories-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /collections/:collectionId
DELETE /collections/:collectionId/items/:itemId
GET /collections
GET /collections/:collectionId
GET /collections/:collectionId/items
GET /on-this-day
GET /preferences
GET /share/:token
GET /slambooks
GET /slambooks/:slambookId
GET /slambooks/:slambookId/moderation
GET /slambooks/:slambookId/opinion-space
GET /slambook-template-packs
POST /collections
POST /collections/:collectionId/items
POST /slambooks
POST /slambooks/:slambookId/archive
POST /slambooks/:slambookId/invites
POST /slambooks/:slambookId/moderation/:sessionId
POST /slambooks/:slambookId/opinion-space/:itemId/pin
POST /slambooks/:slambookId/opinion-space/reorder
POST /slambooks/:slambookId/responses
POST /slambooks/:slambookId/share-link
PUT /collections/:collectionId
PUT /preferences
GROUP /v1/memories
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS memories.collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    title       VARCHAR(200) NOT NULL,
    description TEXT DEFAULT '',
    cover_url   TEXT,
    visibility  VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'friends', 'private')),
    item_count  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories.collection_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id UUID NOT NULL REFERENCES memories.collections(id) ON DELETE CASCADE,
    post_id       UUID,
    media_url     TEXT,
    caption       TEXT DEFAULT '',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories.on_this_day (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    memory_date   DATE NOT NULL,
    post_id       UUID NOT NULL,
    years_ago     INTEGER NOT NULL,
    snippet       TEXT DEFAULT '',
    media_url     TEXT,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories.preferences (
    user_id             UUID PRIMARY KEY,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    hidden_years        INTEGER[] DEFAULT '{}',
    hidden_people_ids   UUID[] DEFAULT '{}',
    notification_time   TIME DEFAULT '09:00',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories.slambooks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id UUID NOT NULL,
  context_type slambook_context_type NOT NULL DEFAULT 'profile',
  context_id UUID NULL,
  title VARCHAR(140) NOT NULL,
  subtitle VARCHAR(240) NULL,
  description TEXT NULL,
  category VARCHAR(50) NOT NULL,
  theme_key VARCHAR(80) NOT NULL DEFAULT 'classic',
  cover_media_id UUID NULL,
  visibility slambook_visibility NOT NULL DEFAULT 'invited_only',
  response_identity_mode response_identity_mode NOT NULL DEFAULT 'named',
  approval_required BOOLEAN NOT NULL DEFAULT TRUE,
  allow_custom_cards BOOLEAN NOT NULL DEFAULT TRUE,
  allow_reactions BOOLEAN NOT NULL DEFAULT FALSE,
  allow_comments BOOLEAN NOT NULL DEFAULT FALSE,
  allow_share_link BOOLEAN NOT NULL DEFAULT TRUE,
  max_responses_per_user INT NOT NULL DEFAULT 1,
  opens_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  closes_at TIMESTAMPTZ NULL,
  status slambook_status NOT NULL DEFAULT 'active',
  invited_count INT NOT NULL DEFAULT 0,
  response_count INT NOT NULL DEFAULT 0,
  approved_count INT NOT NULL DEFAULT 0,
  pinned_count INT NOT NULL DEFAULT 0,
  last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ NULL,
  CONSTRAINT chk_slambook_dates CHECK (closes_at IS NULL OR closes_at > opens_at)
);

CREATE TABLE IF NOT EXISTS memories.slambook_collaborators (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  user_id UUID NOT NULL,
  role slambook_role NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (slambook_id, user_id)
);

CREATE TABLE IF NOT EXISTS memories.slambook_template_packs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key VARCHAR(80) NOT NULL UNIQUE,
  title VARCHAR(120) NOT NULL,
  description TEXT NULL,
  category VARCHAR(50) NOT NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memories.slambook_templates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pack_id UUID NOT NULL REFERENCES memories.slambook_template_packs(id) ON DELETE CASCADE,
  title VARCHAR(140) NOT NULL,
  prompt TEXT NOT NULL,
  response_type card_response_type NOT NULL,
  placeholder_text VARCHAR(200) NULL,
  help_text TEXT NULL,
  config JSONB NOT NULL DEFAULT '{}'::jsonb,
  order_index INT NOT NULL DEFAULT 0,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memories.slambook_template_options (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id UUID NOT NULL REFERENCES memories.slambook_templates(id) ON DELETE CASCADE,
  label VARCHAR(120) NOT NULL,
  value VARCHAR(120) NOT NULL,
  order_index INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS memories.slambook_cards (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  source_type card_source_type NOT NULL DEFAULT 'custom',
  template_id UUID NULL REFERENCES memories.slambook_templates(id),
  title VARCHAR(140) NOT NULL,
  prompt TEXT NOT NULL,
  response_type card_response_type NOT NULL,
  placeholder_text VARCHAR(200) NULL,
  help_text TEXT NULL,
  config JSONB NOT NULL DEFAULT '{}'::jsonb,
  is_required BOOLEAN NOT NULL DEFAULT FALSE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  locked_after_response BOOLEAN NOT NULL DEFAULT FALSE,
  order_index INT NOT NULL DEFAULT 0,
  version_no INT NOT NULL DEFAULT 1,
  created_by_user_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ NULL
);

CREATE TABLE IF NOT EXISTS memories.slambook_card_options (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  card_id UUID NOT NULL REFERENCES memories.slambook_cards(id) ON DELETE CASCADE,
  label VARCHAR(120) NOT NULL,
  value VARCHAR(120) NOT NULL,
  order_index INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS memories.slambook_invites (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  inviter_user_id UUID NOT NULL,
  invite_type invite_type NOT NULL,
  target_user_id UUID NULL,
  target_email VARCHAR(320) NULL,
  target_ref_id UUID NULL,
  share_token UUID NULL DEFAULT gen_random_uuid(),
  message TEXT NULL,
  status invite_status NOT NULL DEFAULT 'pending',
  opened_at TIMESTAMPTZ NULL,
  accepted_at TIMESTAMPTZ NULL,
  declined_at TIMESTAMPTZ NULL,
  expires_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memories.slambook_response_sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  invite_id UUID NULL REFERENCES memories.slambook_invites(id) ON DELETE SET NULL,
  responder_user_id UUID NULL,
  actor_fingerprint_hash VARCHAR(128) NULL,
  display_name_snapshot VARCHAR(120) NULL,
  identity_mode response_identity_mode NOT NULL,
  status response_session_status NOT NULL DEFAULT 'draft',
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  draft_last_saved_at TIMESTAMPTZ NULL,
  submitted_at TIMESTAMPTZ NULL,
  moderated_at TIMESTAMPTZ NULL,
  moderated_by_user_id UUID NULL,
  moderation_reason TEXT NULL,
  client_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ NULL
);

CREATE TABLE IF NOT EXISTS memories.slambook_response_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id UUID NOT NULL REFERENCES memories.slambook_response_sessions(id) ON DELETE CASCADE,
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  card_id UUID NOT NULL REFERENCES memories.slambook_cards(id) ON DELETE CASCADE,
  response_type card_response_type NOT NULL,
  answer_text TEXT NULL,
  answer_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  media_asset_id UUID NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at TIMESTAMPTZ NULL,
  CONSTRAINT uq_slambook_response_item UNIQUE (session_id, card_id)
);

CREATE TABLE IF NOT EXISTS memories.opinion_space_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES memories.slambook_response_sessions(id) ON DELETE CASCADE,
  response_item_id UUID NOT NULL UNIQUE REFERENCES memories.slambook_response_items(id) ON DELETE CASCADE,
  status opinion_board_item_status NOT NULL DEFAULT 'pending',
  is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
  board_section VARCHAR(80) NULL,
  board_order NUMERIC(20,6) NOT NULL DEFAULT 0,
  x_pos INT NULL,
  y_pos INT NULL,
  z_index INT NOT NULL DEFAULT 0,
  featured_badge VARCHAR(80) NULL,
  owner_note TEXT NULL,
  approved_by_user_id UUID NULL,
  approved_at TIMESTAMPTZ NULL,
  hidden_reason TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memories.slambook_blocks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  blocked_user_id UUID NULL,
  blocked_fingerprint_hash VARCHAR(128) NULL,
  reason TEXT NULL,
  blocked_by_user_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memories.slambook_moderation_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slambook_id UUID NOT NULL REFERENCES memories.slambooks(id) ON DELETE CASCADE,
  session_id UUID NULL REFERENCES memories.slambook_response_sessions(id) ON DELETE SET NULL,
  response_item_id UUID NULL REFERENCES memories.slambook_response_items(id) ON DELETE SET NULL,
  actor_user_id UUID NOT NULL,
  action moderation_action_type NOT NULL,
  reason TEXT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```
