package events

import (
	"encoding/json"
	"time"
)

// EventType constants for identity-platform domain events.
const (
	UserRegistered     = "UserRegistered"     // payload: UserRegisteredPayload
	UserLoggedIn       = "UserLoggedIn"       // payload: UserLoggedInPayload
	UserProfileUpdated = "UserProfileUpdated" // payload: UserProfileUpdatedPayload
	UserSettingsUpdated = "UserSettingsUpdated" // payload: UserSettingsUpdatedPayload
	UserSuspended      = "UserSuspended"      // payload: UserSuspendedPayload

	// Social / relationship events
	FollowCreated         = "FollowCreated"         // payload: FollowPayload
	FollowDeleted         = "FollowDeleted"         // payload: FollowPayload
	FriendRequestSent     = "FriendRequestSent"     // payload: FriendRequestPayload
	FriendRequestAccepted = "FriendRequestAccepted" // payload: FriendRequestPayload
	FriendRequestRejected = "FriendRequestRejected" // payload: FriendRequestPayload
	UserBlocked           = "UserBlocked"           // payload: BlockPayload
	UserUnblocked         = "UserUnblocked"         // payload: BlockPayload
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

// UserRegisteredPayload is emitted by auth-service on new registration.
type UserRegisteredPayload struct {
	UserID    string    `json:"user_id"`
	Phone     string    `json:"phone,omitempty"`
	Email     *string   `json:"email,omitempty"`
	FirstName string    `json:"first_name,omitempty"`
	LastName  string    `json:"last_name,omitempty"`
	DOB       string    `json:"dob,omitempty"`
	Gender    string    `json:"gender,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// UserLoggedInPayload is emitted by auth-service on login.
type UserLoggedInPayload struct {
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	DeviceID  string    `json:"device_id"`
	Platform  string    `json:"platform"`
	IP        string    `json:"ip"`
	Timestamp time.Time `json:"timestamp"`
}

// UserProfileUpdatedPayload is emitted by profile-service on profile changes.
type UserProfileUpdatedPayload struct {
	UserID        string    `json:"user_id"`
	DisplayName   string    `json:"display_name"`
	FirstName     string    `json:"first_name,omitempty"`
	LastName      string    `json:"last_name,omitempty"`
	Bio           string    `json:"bio"`
	AvatarMediaID *string   `json:"avatar_media_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UserSettingsUpdatedPayload is emitted by user-service on settings changes.
type UserSettingsUpdatedPayload struct {
	UserID            string    `json:"user_id"`
	AccountVisibility string    `json:"account_visibility"`
	AllowMessagesFrom string    `json:"allow_messages_from"`
	AllowCommentsFrom string    `json:"allow_comments_from"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// UserSuspendedPayload is emitted when a user is suspended.
type UserSuspendedPayload struct {
	UserID      string    `json:"user_id"`
	Until       time.Time `json:"until"`
	Reason      string    `json:"reason"`
	AdminID     string    `json:"admin_id,omitempty"`
	SuspendedAt time.Time `json:"suspended_at"`
}

// FollowPayload is emitted when a follow relationship is created or deleted.
type FollowPayload struct {
	ActorID   string    `json:"actor_id"`
	TargetID  string    `json:"target_id"`
	Timestamp time.Time `json:"timestamp"`
}

// FriendRequestPayload is emitted for friend request lifecycle events.
type FriendRequestPayload struct {
	RequesterID string    `json:"requester_id"`
	AddresseeID string    `json:"addressee_id"`
	Status      string    `json:"status"`
	Timestamp   time.Time `json:"timestamp"`
}

// BlockPayload is emitted when a user blocks or unblocks another user.
type BlockPayload struct {
	BlockerID string    `json:"blocker_id"`
	BlockedID string    `json:"blocked_id"`
	Timestamp time.Time `json:"timestamp"`
}
