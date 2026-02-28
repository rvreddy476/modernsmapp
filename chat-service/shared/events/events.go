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
