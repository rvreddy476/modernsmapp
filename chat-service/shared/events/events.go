package events

import (
	"encoding/json"
	"time"
)

// Event type constants for chat-service domain events.
const (
	ConversationCreated = "ConversationCreated"
	MessageCreated      = "MessageCreated"
	MessageDeleted      = "MessageDeleted"
	MemberAdded         = "MemberAdded"
	MemberRemoved       = "MemberRemoved"
	ReactionToggled     = "ReactionToggled"
)

// Event type constants for call-service domain events.
const (
	CallCreated            = "CallCreated"
	CallInvited            = "CallInvited"
	CallAccepted           = "CallAccepted"
	CallDeclined           = "CallDeclined"
	CallExpired            = "CallExpired"
	CallJoined             = "CallJoined"
	CallLeft               = "CallLeft"
	CallEnded              = "CallEnded"
	CallParticipantMuted   = "CallParticipantMuted"
	CallParticipantRemoved = "CallParticipantRemoved"
	CallUpgraded           = "CallUpgraded"
)

// EventEnvelope is the CloudEvents-style structure used on Kafka.
type EventEnvelope struct {
	EventID     string          `json:"event_id"`
	EventType   string          `json:"event_type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	TraceID     string          `json:"trace_id"`
	ActorUserID *string         `json:"actor_user_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

type ConversationCreatedPayload struct {
	ConversationID string    `json:"conversation_id"`
	Type           string    `json:"type"`
	Title          string    `json:"title,omitempty"`
	CreatedBy      string    `json:"created_by"`
	MemberIDs      []string  `json:"member_ids"`
	CreatedAt      time.Time `json:"created_at"`
}

type MessageCreatedPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	Type           string    `json:"type"`
	CreatedAt      time.Time `json:"created_at"`
}

type MessageDeletedPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	DeletedBy      string    `json:"deleted_by"`
	DeletedAt      time.Time `json:"deleted_at"`
}

type MemberAddedPayload struct {
	ConversationID string    `json:"conversation_id"`
	UserID         string    `json:"user_id"`
	AddedBy        string    `json:"added_by"`
	Role           string    `json:"role"`
	AddedAt        time.Time `json:"added_at"`
}

type MemberRemovedPayload struct {
	ConversationID string    `json:"conversation_id"`
	UserID         string    `json:"user_id"`
	RemovedBy      string    `json:"removed_by"`
	RemovedAt      time.Time `json:"removed_at"`
}

type ReactionToggledPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	UserID         string    `json:"user_id"`
	Emoji          string    `json:"emoji"`
	Added          bool      `json:"added"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// ---------------------------------------------------------------------------
// Call event payloads
// ---------------------------------------------------------------------------

type CallCreatedPayload struct {
	CallID          string    `json:"call_id"`
	CallType        string    `json:"call_type"`
	SourceType      string    `json:"source_type"`
	SourceID        string    `json:"source_id,omitempty"`
	InitiatorUserID string    `json:"initiator_user_id"`
	AudioOnly       bool      `json:"audio_only"`
	CreatedAt       time.Time `json:"created_at"`
}

type CallInvitedPayload struct {
	CallID        string    `json:"call_id"`
	InviteID      string    `json:"invite_id"`
	InviterUserID string    `json:"inviter_user_id"`
	InviteeUserID string    `json:"invitee_user_id"`
	CallType      string    `json:"call_type"`
	CreatedAt     time.Time `json:"created_at"`
}

type CallAcceptedPayload struct {
	CallID   string    `json:"call_id"`
	InviteID string    `json:"invite_id"`
	UserID   string    `json:"user_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type CallDeclinedPayload struct {
	CallID   string    `json:"call_id"`
	InviteID string    `json:"invite_id"`
	UserID   string    `json:"user_id"`
	DeclinedAt time.Time `json:"declined_at"`
}

type CallExpiredPayload struct {
	CallID      string    `json:"call_id"`
	EndedReason string    `json:"ended_reason"`
	ExpiredAt   time.Time `json:"expired_at"`
}

type CallJoinedPayload struct {
	CallID   string    `json:"call_id"`
	UserID   string    `json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
}

type CallLeftPayload struct {
	CallID string    `json:"call_id"`
	UserID string    `json:"user_id"`
	LeftAt time.Time `json:"left_at"`
}

type CallEndedPayload struct {
	CallID          string    `json:"call_id"`
	InitiatorUserID string    `json:"initiator_user_id"`
	EndedBy         string    `json:"ended_by"`
	EndedReason     string    `json:"ended_reason"`
	DurationSeconds int       `json:"duration_seconds"`
	SourceType      string    `json:"source_type"`
	SourceID        string    `json:"source_id,omitempty"`
	EndedAt         time.Time `json:"ended_at"`
}

type CallParticipantMutedPayload struct {
	CallID       string    `json:"call_id"`
	TargetUserID string    `json:"target_user_id"`
	MutedBy      string    `json:"muted_by"`
	MutedAt      time.Time `json:"muted_at"`
}

type CallParticipantRemovedPayload struct {
	CallID       string    `json:"call_id"`
	TargetUserID string    `json:"target_user_id"`
	RemovedBy    string    `json:"removed_by"`
	RemovedAt    time.Time `json:"removed_at"`
}

type CallUpgradedPayload struct {
	CallID     string    `json:"call_id"`
	UpgradedBy string    `json:"upgraded_by"`
	FromType   string    `json:"from_type"`
	ToType     string    `json:"to_type"`
	UpgradedAt time.Time `json:"upgraded_at"`
}
