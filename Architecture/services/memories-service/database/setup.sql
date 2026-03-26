CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Memories service schema
CREATE SCHEMA IF NOT EXISTS memories;

-- User-created memory collections (albums)
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

CREATE INDEX IF NOT EXISTS idx_mc_user ON memories.collections (user_id, created_at DESC);

-- Items in a memory collection
CREATE TABLE IF NOT EXISTS memories.collection_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id UUID NOT NULL REFERENCES memories.collections(id) ON DELETE CASCADE,
    post_id       UUID,
    media_url     TEXT,
    caption       TEXT DEFAULT '',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mci_collection ON memories.collection_items (collection_id, sort_order);

-- On This Day cache: pre-computed daily memories for each user
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_otd_user_date_post ON memories.on_this_day (user_id, memory_date, post_id);
CREATE INDEX IF NOT EXISTS idx_otd_user_date ON memories.on_this_day (user_id, memory_date);

-- User preferences for memories (hide years, opt-out people, etc.)
CREATE TABLE IF NOT EXISTS memories.preferences (
    user_id             UUID PRIMARY KEY,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    hidden_years        INTEGER[] DEFAULT '{}',
    hidden_people_ids   UUID[] DEFAULT '{}',
    notification_time   TIME DEFAULT '09:00',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- -----------------------------------------------------------------------------
-- SlamBook + Opinion Space (v1)
-- -----------------------------------------------------------------------------

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'slambook_context_type') THEN
    CREATE TYPE slambook_context_type AS ENUM ('profile', 'group', 'community', 'event');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'slambook_visibility') THEN
    CREATE TYPE slambook_visibility AS ENUM ('private', 'invited_only', 'friends_only', 'group_members_only', 'community_members_only', 'public');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'slambook_status') THEN
    CREATE TYPE slambook_status AS ENUM ('draft', 'active', 'closed', 'archived', 'deleted');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'slambook_role') THEN
    CREATE TYPE slambook_role AS ENUM ('owner', 'editor', 'moderator');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'response_identity_mode') THEN
    CREATE TYPE response_identity_mode AS ENUM ('named', 'anonymous_allowed', 'anonymous_owner_only', 'fully_anonymous');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'card_source_type') THEN
    CREATE TYPE card_source_type AS ENUM ('template', 'custom');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'card_response_type') THEN
    CREATE TYPE card_response_type AS ENUM ('text', 'long_text', 'one_word', 'emoji', 'single_choice', 'multi_choice', 'rating', 'yes_no', 'image', 'voice', 'doodle', 'video', 'memory_story');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'invite_type') THEN
    CREATE TYPE invite_type AS ENUM ('user', 'email', 'group', 'community', 'link');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'invite_status') THEN
    CREATE TYPE invite_status AS ENUM ('pending', 'sent', 'opened', 'accepted', 'declined', 'expired', 'revoked');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'response_session_status') THEN
    CREATE TYPE response_session_status AS ENUM ('draft', 'submitted', 'pending', 'approved', 'rejected', 'hidden', 'deleted');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'opinion_board_item_status') THEN
    CREATE TYPE opinion_board_item_status AS ENUM ('pending', 'visible', 'hidden', 'archived', 'rejected');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'moderation_action_type') THEN
    CREATE TYPE moderation_action_type AS ENUM ('approve', 'reject', 'hide', 'delete', 'restore', 'block_user');
  END IF;
END $$;

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

CREATE INDEX IF NOT EXISTS idx_slambooks_owner_status
  ON memories.slambooks(owner_user_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_slambooks_context
  ON memories.slambooks(context_type, context_id, status);

CREATE INDEX IF NOT EXISTS idx_slambook_cards_slambook_order
  ON memories.slambook_cards(slambook_id, order_index)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_slambook_invites_target_user
  ON memories.slambook_invites(target_user_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_slambook_invites_token
  ON memories.slambook_invites(share_token);

CREATE INDEX IF NOT EXISTS idx_response_sessions_slambook_status
  ON memories.slambook_response_sessions(slambook_id, status, created_at DESC)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_response_sessions_responder
  ON memories.slambook_response_sessions(responder_user_id, created_at DESC)
  WHERE responder_user_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_response_sessions_single_responder
  ON memories.slambook_response_sessions(slambook_id, responder_user_id)
  WHERE responder_user_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_response_items_slambook_card
  ON memories.slambook_response_items(slambook_id, card_id)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_opinion_space_items_slambook_visible
  ON memories.opinion_space_items(slambook_id, status, is_pinned DESC, board_order ASC);

CREATE INDEX IF NOT EXISTS idx_moderation_log_slambook_created
  ON memories.slambook_moderation_log(slambook_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_slambook_blocks_user
  ON memories.slambook_blocks(slambook_id, blocked_user_id);

INSERT INTO memories.slambook_template_packs (key, title, description, category)
VALUES
  ('friendship', 'Friendship Pack', 'Warm prompts for best friends and close bonds.', 'friendship'),
  ('birthday', 'Birthday Pack', 'Celebratory prompts for wishes and memories.', 'birthday'),
  ('farewell', 'Farewell Pack', 'Reflection prompts for goodbyes and transitions.', 'farewell')
ON CONFLICT (key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    category = EXCLUDED.category,
    is_active = TRUE;

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'First impression of me', 'What was your first impression of me?', 'text', 'Share your first memory of meeting me', 'Keep it kind and specific.', 0
FROM memories.slambook_template_packs p
WHERE p.key = 'friendship'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'First impression of me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'One word for me', 'Describe me in one word.', 'text', 'Only one word', 'Short answers feel punchy on the board.', 1
FROM memories.slambook_template_packs p
WHERE p.key = 'friendship'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'One word for me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Best memory with me', 'What is your favorite memory with me?', 'text', 'Tell the story', 'Moments and details make this special.', 2
FROM memories.slambook_template_packs p
WHERE p.key = 'friendship'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Best memory with me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'What makes me special', 'What makes me stand out to you?', 'text', 'Tell me what you notice most', 'Focus on something genuine.', 3
FROM memories.slambook_template_packs p
WHERE p.key = 'friendship'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'What makes me special'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'One thing I should never change', 'What is one thing I should never change about myself?', 'text', 'A trait, habit, or value', 'This should feel encouraging.', 4
FROM memories.slambook_template_packs p
WHERE p.key = 'friendship'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'One thing I should never change'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Birthday wish for me', 'What is your birthday wish for me this year?', 'text', 'Write a heartfelt wish', 'Keep it personal.', 0
FROM memories.slambook_template_packs p
WHERE p.key = 'birthday'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Birthday wish for me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Funniest memory with me', 'What is the funniest memory we share?', 'text', 'Tell the memory', 'Funny details make the board lively.', 1
FROM memories.slambook_template_packs p
WHERE p.key = 'birthday'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Funniest memory with me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Describe me in three words', 'If you had to describe me in three words, what would they be?', 'text', 'Three words only', 'Keep it crisp.', 2
FROM memories.slambook_template_packs p
WHERE p.key = 'birthday'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Describe me in three words'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'What should this year bring me', 'What do you hope this year brings into my life?', 'text', 'Dream big for me', 'Aspirational answers work well here.', 3
FROM memories.slambook_template_packs p
WHERE p.key = 'birthday'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'What should this year bring me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'A message for future me', 'Leave a message for my future self.', 'text', 'Write something future me should read', 'Thoughtful notes age beautifully.', 4
FROM memories.slambook_template_packs p
WHERE p.key = 'birthday'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'A message for future me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'What will you miss about me', 'What will you miss most about me?', 'text', 'Share one thing you will miss', 'This should feel warm, not harsh.', 0
FROM memories.slambook_template_packs p
WHERE p.key = 'farewell'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'What will you miss about me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Message for my future', 'What message do you want to leave for my future self?', 'text', 'Leave a note I can remember later', 'Supportive answers work best.', 1
FROM memories.slambook_template_packs p
WHERE p.key = 'farewell'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Message for my future'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Final advice for me', 'What is one piece of advice you want me to carry forward?', 'text', 'Give me one honest piece of advice', 'Kind honesty only.', 2
FROM memories.slambook_template_packs p
WHERE p.key = 'farewell'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Final advice for me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'One thing you never told me', 'What is one thing you never told me before?', 'text', 'Say the thing now', 'This should still stay respectful.', 3
FROM memories.slambook_template_packs p
WHERE p.key = 'farewell'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'One thing you never told me'
  );

INSERT INTO memories.slambook_templates (pack_id, title, prompt, response_type, placeholder_text, help_text, order_index)
SELECT p.id, 'Your favorite memory with me', 'Which memory with me do you want us to never forget?', 'text', 'Write the memory down', 'Memory-rich answers shine on the board.', 4
FROM memories.slambook_template_packs p
WHERE p.key = 'farewell'
  AND NOT EXISTS (
    SELECT 1 FROM memories.slambook_templates t
    WHERE t.pack_id = p.id AND t.title = 'Your favorite memory with me'
  );
