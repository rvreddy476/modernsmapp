package events

import (
	"encoding/json"
	"time"
)

// EventType constants for known domain events.
const (
	UserRegistered = "UserRegistered" // payload: UserRegisteredPayload
	UserLoggedIn   = "UserLoggedIn"   // payload: UserLoggedInPayload

	PostCreated    = "PostCreated"    // payload: PostCreatedPayload
	PostDeleted    = "PostDeleted"    // payload: PostDeletedPayload
	UserFollowed   = "UserFollowed"   // payload: UserFollowedPayload
	UserUnfollowed = "UserUnfollowed" // payload: UserUnfollowedPayload

	PostReacted        = "PostReacted"        // payload: PostReactedPayload
	CommentReacted     = "CommentReacted"     // payload: CommentReactedPayload
	CommentCreated     = "CommentCreated"     // payload: CommentCreatedPayload
	UserProfileUpdated = "UserProfileUpdated" // payload: UserProfileUpdatedPayload
	ContentTakenDown   = "ContentTakenDown"   // payload: ContentTakenDownPayload
	UserSuspended      = "UserSuspended"      // payload: UserSuspendedPayload

	MediaTranscodeRequested = "MediaTranscodeRequested" // payload: MediaTranscodeRequestedPayload
	MediaTranscodeCompleted = "MediaTranscodeCompleted" // payload: MediaTranscodeCompletedPayload

	FriendRequestSent     = "FriendRequestSent"     // payload: FriendRequestSentPayload
	FriendRequestAccepted = "FriendRequestAccepted" // payload: FriendRequestAcceptedPayload

	GroupCreated      = "GroupCreated"      // payload: GroupCreatedPayload
	GroupMemberJoined = "GroupMemberJoined" // payload: GroupMemberJoinedPayload
	GroupMemberLeft   = "GroupMemberLeft"   // payload: GroupMemberLeftPayload
	GroupPostCreated  = "GroupPostCreated"  // payload: GroupPostCreatedPayload
)

// EventEnvelope is the CloudEvents-ish structure we use on Kafka.
type EventEnvelope struct {
	EventID     string          `json:"event_id"`
	EventType   string          `json:"event_type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	TraceID     string          `json:"trace_id"`
	ActorUserID *string         `json:"actor_user_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// UserRegisteredPayload definition.
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

// UserLoggedInPayload definition.
type UserLoggedInPayload struct {
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
	DeviceID  string    `json:"device_id"`
	Platform  string    `json:"platform"`
	IP        string    `json:"ip"`
	Timestamp time.Time `json:"timestamp"`
}

type PostCreatedPayload struct {
	PostID     string    `json:"post_id"`
	AuthorID   string    `json:"author_id"` // Simplified to string for JSON, usually UUID
	Text       string    `json:"text"`
	Visibility string    `json:"visibility"`
	CreatedAt  time.Time `json:"created_at"`
}

type UserFollowedPayload struct {
	FollowerID string    `json:"follower_id"`
	FolloweeID string    `json:"followee_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type PostReactedPayload struct {
	PostID       string    `json:"post_id"`
	PostAuthorID string    `json:"post_author_id"`
	ReactorID    string    `json:"reactor_id"`
	ReactType    string    `json:"react_type"` // like, love, etc.
	CreatedAt    time.Time `json:"created_at"`
}

type CommentReactedPayload struct {
	CommentID       string    `json:"comment_id"`
	PostID          string    `json:"post_id"`
	CommentAuthorID string    `json:"comment_author_id"`
	ReactorID       string    `json:"reactor_id"`
	ReactType       string    `json:"react_type"`
	CreatedAt       time.Time `json:"created_at"`
}

type CommentCreatedPayload struct {
	CommentID    string    `json:"comment_id"`
	PostID       string    `json:"post_id"`
	PostAuthorID string    `json:"post_author_id"`
	AuthorID     string    `json:"author_id"`
	Text         string    `json:"text"`
	CreatedAt    time.Time `json:"created_at"`
}

type UserProfileUpdatedPayload struct {
	UserID        string    `json:"user_id"`
	Username      string    `json:"username,omitempty"`
	DisplayName   string    `json:"display_name"`
	Bio           string    `json:"bio"`
	AvatarMediaID string    `json:"avatar_media_id,omitempty"`
	IsVerified    bool      `json:"is_verified"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ContentTakenDownPayload struct {
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Reason     string    `json:"reason"`
	AdminID    string    `json:"admin_id,omitempty"`
	DeletedAt  time.Time `json:"deleted_at"`
}

type UserSuspendedPayload struct {
	UserID      string    `json:"user_id"`
	Until       time.Time `json:"until"`
	Reason      string    `json:"reason"`
	AdminID     string    `json:"admin_id,omitempty"`
	SuspendedAt time.Time `json:"suspended_at"`
}

type MediaTranscodeRequestedPayload struct {
	MediaAssetID string `json:"media_id"`
	UploaderID   string `json:"uploader_id"`
	StorageKey   string `json:"storage_key"`
	MimeType     string `json:"mime_type"`
}

type MediaTranscodeCompletedPayload struct {
	MediaAssetID     string `json:"media_id"`
	ProcessingStatus string `json:"processing_status"`
}

type FriendRequestSentPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type FriendRequestAcceptedPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type GroupCreatedPayload struct {
	GroupID    string    `json:"group_id"`
	CreatorID  string    `json:"creator_id"`
	Name       string    `json:"name"`
	Visibility string    `json:"visibility"`
	CreatedAt  time.Time `json:"created_at"`
}

type GroupMemberJoinedPayload struct {
	GroupID  string    `json:"group_id"`
	UserID   string    `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type GroupMemberLeftPayload struct {
	GroupID string    `json:"group_id"`
	UserID  string    `json:"user_id"`
	LeftAt  time.Time `json:"left_at"`
}

type GroupPostCreatedPayload struct {
	GroupID   string    `json:"group_id"`
	PostID    string    `json:"post_id"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}
