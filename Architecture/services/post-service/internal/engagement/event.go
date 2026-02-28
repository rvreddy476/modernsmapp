package engagement

import (
	"time"

	"github.com/google/uuid"
)

// EngagementEvent is the canonical event envelope for all engagement actions.
// Every consumer reads this struct from Kafka. Fields enable dedup (EventID),
// ordering (UserSeqNo, ActionTS), and idempotent writes (IsSet + USING TIMESTAMP).
type EngagementEvent struct {
	// Identity
	EventID   string `json:"event_id"`   // UUIDv7 (time-sortable)
	EventType string `json:"event_type"` // e.g. "engagement.post.liked"

	// Ordering
	ActionTS  time.Time `json:"action_ts"`    // wall-clock captured inside Lua script
	UserSeqNo int64     `json:"user_seq_no"`  // per-user monotonic counter (Redis INCR)

	// Payload
	PostID     uuid.UUID `json:"post_id"`
	UserID     uuid.UUID `json:"user_id"`
	AuthorID   uuid.UUID `json:"author_id"`
	TargetType string    `json:"target_type"` // "post" or "comment"
	TargetID   uuid.UUID `json:"target_id"`   // post_id or comment_id
	Action     string    `json:"action"`      // "like", "share", "bookmark", "comment", "reply"
	IsSet      bool      `json:"is_set"`      // true = created, false = removed

	// Optional payload for comments/replies
	CommentBody string `json:"comment_body,omitempty"`
	ShareType   string `json:"share_type,omitempty"`  // "repost", "quote", "external"
	QuoteText   string `json:"quote_text,omitempty"`

	// Schema
	Version int `json:"version"` // 1
}

// Event type constants
const (
	EventPostLiked         = "engagement.post.liked"
	EventPostUnliked       = "engagement.post.unliked"
	EventCommentCreated    = "engagement.comment.created"
	EventCommentDeleted    = "engagement.comment.deleted"
	EventCommentLiked      = "engagement.comment.liked"
	EventCommentUnliked    = "engagement.comment.unliked"
	EventCommentDisliked   = "engagement.comment.disliked"
	EventCommentUndisliked = "engagement.comment.undisliked"
	EventReplyCreated      = "engagement.reply.created"
	EventPostShared        = "engagement.post.shared"
	EventPostBookmarked    = "engagement.post.bookmarked"
	EventPostUnbookmarked  = "engagement.post.unbookmarked"
)
