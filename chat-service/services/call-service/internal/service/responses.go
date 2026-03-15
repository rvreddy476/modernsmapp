package service

import (
	"time"

	"github.com/atpost/chat-call-service/internal/sfu"
	"github.com/google/uuid"
)

// CallResponse is the standard API response for a call session.
type CallResponse struct {
	ID               uuid.UUID             `json:"id"`
	CallType         string                `json:"call_type"`
	SourceType       string                `json:"source_type"`
	SourceID         *uuid.UUID            `json:"source_id,omitempty"`
	InitiatorUserID  uuid.UUID             `json:"initiator_user_id"`
	State            string                `json:"state"`
	AudioOnly        bool                  `json:"audio_only"`
	MaxParticipants  int                   `json:"max_participants"`
	JoinMode         string                `json:"join_mode"`
	Participants     []ParticipantResponse `json:"participants"`
	StartedAt        *time.Time            `json:"started_at,omitempty"`
	AnsweredAt       *time.Time            `json:"answered_at,omitempty"`
	EndedAt          *time.Time            `json:"ended_at,omitempty"`
	EndedReason      *string               `json:"ended_reason,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
}

// JoinResponse is returned when a user joins a call.
type JoinResponse struct {
	CallID              uuid.UUID      `json:"call_id"`
	SFUToken            string         `json:"sfu_token"`
	SFURoomName         string         `json:"sfu_room_name"`
	ICEServers          []sfu.ICEServer `json:"ice_servers"`
	SignalingEndpoint   string         `json:"signaling_endpoint"`
	ReconnectGraceSeconds int          `json:"reconnect_grace_seconds"`
}

// ParticipantResponse is the API representation of a call participant.
type ParticipantResponse struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	Role            string     `json:"role"`
	InviteState     string     `json:"invite_state"`
	JoinState       string     `json:"join_state"`
	AudioMuted      bool       `json:"audio_muted"`
	VideoMuted      bool       `json:"video_muted"`
	HandRaised      bool       `json:"hand_raised"`
	IsScreenSharing bool       `json:"is_screen_sharing"`
	JoinedAt        *time.Time `json:"joined_at,omitempty"`
	LeftAt          *time.Time `json:"left_at,omitempty"`
	DurationSeconds *int       `json:"duration_seconds,omitempty"`
}

// CallHistoryItem is a simplified call for the history list.
type CallHistoryItem struct {
	ID               uuid.UUID             `json:"id"`
	CallType         string                `json:"call_type"`
	SourceType       string                `json:"source_type"`
	SourceID         *uuid.UUID            `json:"source_id,omitempty"`
	InitiatorUserID  uuid.UUID             `json:"initiator_user_id"`
	State            string                `json:"state"`
	AudioOnly        bool                  `json:"audio_only"`
	EndedReason      *string               `json:"ended_reason,omitempty"`
	DurationSeconds  int                   `json:"duration_seconds"`
	IsMissed         bool                  `json:"is_missed"`
	IsIncoming       bool                  `json:"is_incoming"`
	Participants     []ParticipantResponse `json:"participants"`
	CreatedAt        time.Time             `json:"created_at"`
	EndedAt          *time.Time            `json:"ended_at,omitempty"`
}

// InviteResponse is returned after sending invitations.
type InviteResponse struct {
	CallID      uuid.UUID `json:"call_id"`
	InvitesSent int       `json:"invites_sent"`
}
