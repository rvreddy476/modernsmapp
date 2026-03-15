-- =============================================================================
-- CALL_DB — Call service schema (voice/video calling)
-- Run against: call_db
-- =============================================================================

\connect call_db;

CREATE SCHEMA IF NOT EXISTS calls;

-- -----------------------------------------------------------------------------
-- call_sessions — Core call lifecycle
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.call_sessions (
    id UUID PRIMARY KEY,
    call_type TEXT NOT NULL CHECK (call_type IN ('direct_audio', 'direct_video', 'group_audio', 'group_video')),
    source_type TEXT NOT NULL CHECK (source_type IN ('chat', 'profile', 'group', 'circle', 'connections')),
    source_id UUID,
    initiator_user_id UUID NOT NULL,
    room_id UUID,
    state TEXT NOT NULL DEFAULT 'initiated' CHECK (state IN ('initiated', 'ringing', 'active', 'ended', 'canceled', 'failed', 'expired')),
    region_code TEXT NOT NULL DEFAULT 'ap-south-1',
    audio_only BOOLEAN NOT NULL DEFAULT FALSE,
    recording_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    max_participants INT NOT NULL DEFAULT 2,
    join_mode TEXT NOT NULL DEFAULT 'invite_only' CHECK (join_mode IN ('open', 'invite_only')),
    started_at TIMESTAMPTZ,
    answered_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    ended_reason TEXT CHECK (ended_reason IN ('completed', 'timeout', 'canceled', 'host_left', 'all_left', 'failed', 'missed')),
    metadata_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_call_sessions_initiator ON calls.call_sessions(initiator_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_call_sessions_room ON calls.call_sessions(room_id) WHERE room_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_call_sessions_source ON calls.call_sessions(source_type, source_id) WHERE source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_call_sessions_state ON calls.call_sessions(state, created_at) WHERE state NOT IN ('ended', 'canceled', 'failed', 'expired');

-- -----------------------------------------------------------------------------
-- call_participants — Per-user state within a call
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.call_participants (
    id UUID PRIMARY KEY,
    call_session_id UUID NOT NULL REFERENCES calls.call_sessions(id),
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'participant' CHECK (role IN ('host', 'participant', 'moderator', 'speaker', 'listener')),
    invite_state TEXT NOT NULL DEFAULT 'invited' CHECK (invite_state IN ('invited', 'delivered', 'accepted', 'declined', 'missed', 'canceled', 'failed')),
    join_state TEXT NOT NULL DEFAULT 'not_joined' CHECK (join_state IN ('not_joined', 'joining', 'joined', 'reconnecting', 'left', 'removed')),
    audio_muted BOOLEAN NOT NULL DEFAULT FALSE,
    video_muted BOOLEAN NOT NULL DEFAULT TRUE,
    hand_raised BOOLEAN NOT NULL DEFAULT FALSE,
    is_screen_sharing BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at TIMESTAMPTZ,
    left_at TIMESTAMPTZ,
    last_quality_score REAL,
    duration_seconds INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (call_session_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_call_participants_user ON calls.call_participants(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_call_participants_session_join ON calls.call_participants(call_session_id, join_state);
CREATE INDEX IF NOT EXISTS idx_call_participants_session_invite ON calls.call_participants(call_session_id, invite_state);

-- -----------------------------------------------------------------------------
-- call_invites — Delivery tracking for call invitations
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.call_invites (
    id UUID PRIMARY KEY,
    call_session_id UUID NOT NULL REFERENCES calls.call_sessions(id),
    inviter_user_id UUID NOT NULL,
    invitee_user_id UUID NOT NULL,
    delivery_channel TEXT NOT NULL DEFAULT 'websocket' CHECK (delivery_channel IN ('websocket', 'push', 'in_app')),
    delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK (delivery_status IN ('pending', 'delivered', 'failed')),
    response_status TEXT NOT NULL DEFAULT 'pending' CHECK (response_status IN ('pending', 'accepted', 'declined', 'missed', 'canceled', 'expired')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    responded_at TIMESTAMPTZ,
    metadata_json JSONB,
    UNIQUE (call_session_id, invitee_user_id)
);

CREATE INDEX IF NOT EXISTS idx_call_invites_invitee ON calls.call_invites(invitee_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_call_invites_session ON calls.call_invites(call_session_id);

-- -----------------------------------------------------------------------------
-- call_rooms — SFU room allocation
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.call_rooms (
    id UUID PRIMARY KEY,
    room_key TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL DEFAULT 'livekit' CHECK (provider IN ('livekit', 'janus', 'stub')),
    provider_room_name TEXT,
    region_code TEXT NOT NULL DEFAULT 'ap-south-1',
    assigned_node_id TEXT,
    status TEXT NOT NULL DEFAULT 'allocated' CHECK (status IN ('allocated', 'active', 'closed', 'failed')),
    max_participants INT NOT NULL DEFAULT 2,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    metadata_json JSONB
);

CREATE INDEX IF NOT EXISTS idx_call_rooms_status ON calls.call_rooms(region_code, status) WHERE status IN ('allocated', 'active');

-- -----------------------------------------------------------------------------
-- call_device_sessions — Device tracking for multi-device users
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.call_device_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    device_id VARCHAR(64) NOT NULL,
    platform TEXT NOT NULL DEFAULT 'web' CHECK (platform IN ('web', 'flutter_android', 'flutter_ios', 'desktop')),
    app_version TEXT,
    websocket_session_id TEXT,
    network_type TEXT,
    is_online BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    call_permission_granted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_call_device_sessions_user_online ON calls.call_device_sessions(user_id, is_online) WHERE is_online = TRUE;
CREATE INDEX IF NOT EXISTS idx_call_device_sessions_ws ON calls.call_device_sessions(websocket_session_id) WHERE websocket_session_id IS NOT NULL;

-- -----------------------------------------------------------------------------
-- group_call_policies — Per-group call settings
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.group_call_policies (
    group_id UUID PRIMARY KEY,
    who_can_start_call TEXT NOT NULL DEFAULT 'any_member' CHECK (who_can_start_call IN ('admin_only', 'admin_and_mod', 'any_member')),
    who_can_join_call TEXT NOT NULL DEFAULT 'any_member' CHECK (who_can_join_call IN ('admin_only', 'admin_and_mod', 'any_member')),
    who_can_invite TEXT NOT NULL DEFAULT 'any_member' CHECK (who_can_invite IN ('admin_only', 'admin_and_mod', 'any_member')),
    default_audio_only BOOLEAN NOT NULL DEFAULT FALSE,
    max_participants INT NOT NULL DEFAULT 25,
    recording_allowed BOOLEAN NOT NULL DEFAULT FALSE,
    muted_join_default BOOLEAN NOT NULL DEFAULT TRUE,
    who_can_record TEXT NOT NULL DEFAULT 'admin_only' CHECK (who_can_record IN ('admin_only', 'admin_and_mod', 'any_member', 'nobody')),
    join_mode TEXT NOT NULL DEFAULT 'open' CHECK (join_mode IN ('open', 'invite_only')),
    metadata_json JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- -----------------------------------------------------------------------------
-- call_event_summaries — Analytics event log per call
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.call_event_summaries (
    id UUID PRIMARY KEY,
    call_session_id UUID NOT NULL REFERENCES calls.call_sessions(id),
    participant_user_id UUID,
    event_type TEXT NOT NULL,
    event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    payload_json JSONB
);

CREATE INDEX IF NOT EXISTS idx_call_event_summaries_session ON calls.call_event_summaries(call_session_id, event_at);
CREATE INDEX IF NOT EXISTS idx_call_event_summaries_user ON calls.call_event_summaries(participant_user_id, event_at DESC) WHERE participant_user_id IS NOT NULL;

-- -----------------------------------------------------------------------------
-- Outbox + Idempotency (same pattern as chat_db)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS calls.outbox_events (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_call_outbox_unpublished ON calls.outbox_events(id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS calls.idempotency_keys (
    key TEXT PRIMARY KEY,
    request_hash TEXT NOT NULL,
    response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);
CREATE INDEX IF NOT EXISTS idx_call_idempotency_expires ON calls.idempotency_keys(expires_at);
