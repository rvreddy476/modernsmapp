package domain

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

// CallType
const (
	CallTypeDirectAudio = "direct_audio"
	CallTypeDirectVideo = "direct_video"
	CallTypeGroupAudio  = "group_audio"
	CallTypeGroupVideo  = "group_video"
)

// CallState
const (
	CallStateInitiated = "initiated"
	CallStateRinging   = "ringing"
	CallStateActive    = "active"
	CallStateEnded     = "ended"
	CallStateCanceled  = "canceled"
	CallStateFailed    = "failed"
	CallStateExpired   = "expired"
)

// SourceType
const (
	SourceTypeChat        = "chat"
	SourceTypeProfile     = "profile"
	SourceTypeGroup       = "group"
	SourceTypeCircle      = "circle"
	SourceTypeConnections = "connections"
)

// ParticipantRole
const (
	RoleHost        = "host"
	RoleParticipant = "participant"
	RoleModerator   = "moderator"
	RoleSpeaker     = "speaker"
	RoleListener    = "listener"
)

// InviteState
const (
	InviteStateInvited   = "invited"
	InviteStateDelivered = "delivered"
	InviteStateAccepted  = "accepted"
	InviteStateDeclined  = "declined"
	InviteStateMissed    = "missed"
	InviteStateCanceled  = "canceled"
	InviteStateFailed    = "failed"
)

// JoinState
const (
	JoinStateNotJoined    = "not_joined"
	JoinStateJoining      = "joining"
	JoinStateJoined       = "joined"
	JoinStateReconnecting = "reconnecting"
	JoinStateLeft         = "left"
	JoinStateRemoved      = "removed"
)

// EndedReason
const (
	EndedReasonCompleted = "completed"
	EndedReasonTimeout   = "timeout"
	EndedReasonCanceled  = "canceled"
	EndedReasonHostLeft  = "host_left"
	EndedReasonAllLeft   = "all_left"
	EndedReasonFailed    = "failed"
	EndedReasonMissed    = "missed"
)

// JoinMode
const (
	JoinModeOpen       = "open"
	JoinModeInviteOnly = "invite_only"
)

// DeliveryChannel
const (
	DeliveryChannelWebSocket = "websocket"
	DeliveryChannelPush      = "push"
	DeliveryChannelInApp     = "in_app"
)

// DeliveryStatus
const (
	DeliveryStatusPending   = "pending"
	DeliveryStatusDelivered = "delivered"
	DeliveryStatusFailed    = "failed"
)

// ResponseStatus
const (
	ResponseStatusPending  = "pending"
	ResponseStatusAccepted = "accepted"
	ResponseStatusDeclined = "declined"
	ResponseStatusMissed   = "missed"
	ResponseStatusCanceled = "canceled"
	ResponseStatusExpired  = "expired"
)

// RoomStatus
const (
	RoomStatusAllocated = "allocated"
	RoomStatusActive    = "active"
	RoomStatusClosed    = "closed"
	RoomStatusFailed    = "failed"
)

// ---------------------------------------------------------------------------
// Domain models
// ---------------------------------------------------------------------------

type CallSession struct {
	ID               uuid.UUID  `json:"id"`
	CallType         string     `json:"call_type"`
	SourceType       string     `json:"source_type"`
	SourceID         *uuid.UUID `json:"source_id,omitempty"`
	InitiatorUserID  uuid.UUID  `json:"initiator_user_id"`
	RoomID           *uuid.UUID `json:"room_id,omitempty"`
	State            string     `json:"state"`
	RegionCode       string     `json:"region_code"`
	AudioOnly        bool       `json:"audio_only"`
	RecordingEnabled bool       `json:"recording_enabled"`
	MaxParticipants  int        `json:"max_participants"`
	JoinMode         string     `json:"join_mode"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	AnsweredAt       *time.Time `json:"answered_at,omitempty"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	EndedReason      *string    `json:"ended_reason,omitempty"`
	MetadataJSON     []byte     `json:"metadata_json,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CallParticipant struct {
	ID               uuid.UUID  `json:"id"`
	CallSessionID    uuid.UUID  `json:"call_session_id"`
	UserID           uuid.UUID  `json:"user_id"`
	Role             string     `json:"role"`
	InviteState      string     `json:"invite_state"`
	JoinState        string     `json:"join_state"`
	AudioMuted       bool       `json:"audio_muted"`
	VideoMuted       bool       `json:"video_muted"`
	HandRaised       bool       `json:"hand_raised"`
	IsScreenSharing  bool       `json:"is_screen_sharing"`
	JoinedAt         *time.Time `json:"joined_at,omitempty"`
	LeftAt           *time.Time `json:"left_at,omitempty"`
	LastQualityScore *float32   `json:"last_quality_score,omitempty"`
	DurationSeconds  *int       `json:"duration_seconds,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CallInvite struct {
	ID              uuid.UUID  `json:"id"`
	CallSessionID   uuid.UUID  `json:"call_session_id"`
	InviterUserID   uuid.UUID  `json:"inviter_user_id"`
	InviteeUserID   uuid.UUID  `json:"invitee_user_id"`
	DeliveryChannel string     `json:"delivery_channel"`
	DeliveryStatus  string     `json:"delivery_status"`
	ResponseStatus  string     `json:"response_status"`
	CreatedAt       time.Time  `json:"created_at"`
	DeliveredAt     *time.Time `json:"delivered_at,omitempty"`
	RespondedAt     *time.Time `json:"responded_at,omitempty"`
	MetadataJSON    []byte     `json:"metadata_json,omitempty"`
}

type CallRoom struct {
	ID               uuid.UUID  `json:"id"`
	RoomKey          string     `json:"room_key"`
	Provider         string     `json:"provider"`
	ProviderRoomName string     `json:"provider_room_name"`
	RegionCode       string     `json:"region_code"`
	AssignedNodeID   *string    `json:"assigned_node_id,omitempty"`
	Status           string     `json:"status"`
	MaxParticipants  int        `json:"max_participants"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	MetadataJSON     []byte     `json:"metadata_json,omitempty"`
}

type CallDeviceSession struct {
	ID                    uuid.UUID  `json:"id"`
	UserID                uuid.UUID  `json:"user_id"`
	DeviceID              string     `json:"device_id"`
	Platform              string     `json:"platform"`
	AppVersion            string     `json:"app_version"`
	WebSocketSessionID    *string    `json:"websocket_session_id,omitempty"`
	NetworkType           *string    `json:"network_type,omitempty"`
	IsOnline              bool       `json:"is_online"`
	LastSeenAt            time.Time  `json:"last_seen_at"`
	CallPermissionGranted bool       `json:"call_permission_granted"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type GroupCallPolicy struct {
	GroupID          uuid.UUID `json:"group_id"`
	WhoCanStartCall  string    `json:"who_can_start_call"`
	WhoCanJoinCall   string    `json:"who_can_join_call"`
	WhoCanInvite     string    `json:"who_can_invite"`
	DefaultAudioOnly bool      `json:"default_audio_only"`
	MaxParticipants  int       `json:"max_participants"`
	RecordingAllowed bool      `json:"recording_allowed"`
	MutedJoinDefault bool      `json:"muted_join_default"`
	WhoCanRecord     string    `json:"who_can_record"`
	JoinMode         string    `json:"join_mode"`
	MetadataJSON     []byte    `json:"metadata_json,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CallEventSummary struct {
	ID                uuid.UUID  `json:"id"`
	CallSessionID     uuid.UUID  `json:"call_session_id"`
	ParticipantUserID *uuid.UUID `json:"participant_user_id,omitempty"`
	EventType         string     `json:"event_type"`
	EventAt           time.Time  `json:"event_at"`
	PayloadJSON       []byte     `json:"payload_json,omitempty"`
}

// IsDirectCall returns true if the call type is 1:1.
func (c *CallSession) IsDirectCall() bool {
	return c.CallType == CallTypeDirectAudio || c.CallType == CallTypeDirectVideo
}

// IsGroupCall returns true if the call type is group.
func (c *CallSession) IsGroupCall() bool {
	return c.CallType == CallTypeGroupAudio || c.CallType == CallTypeGroupVideo
}
