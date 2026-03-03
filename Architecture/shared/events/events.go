package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/atpost/shared/o11y/trace"
	"github.com/google/uuid"
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
	UserUnsuspended    = "UserUnsuspended"    // payload: UserUnsuspendedPayload

	MediaTranscodeRequested = "MediaTranscodeRequested" // payload: MediaTranscodeRequestedPayload
	MediaTranscodeCompleted = "MediaTranscodeCompleted" // payload: MediaTranscodeCompletedPayload

	FriendRequestSent     = "FriendRequestSent"     // payload: FriendRequestSentPayload
	FriendRequestAccepted = "FriendRequestAccepted" // payload: FriendRequestAcceptedPayload
	FriendRequestDeclined = "FriendRequestDeclined" // payload: FriendRequestDeclinedPayload
	FriendRemoved         = "FriendRemoved"         // payload: FriendRemovedPayload
	UserBlocked           = "UserBlocked"            // payload: UserBlockedPayload

	GroupCreated      = "GroupCreated"      // payload: GroupCreatedPayload
	GroupMemberJoined = "GroupMemberJoined" // payload: GroupMemberJoinedPayload
	GroupMemberLeft   = "GroupMemberLeft"   // payload: GroupMemberLeftPayload
	GroupPostCreated  = "GroupPostCreated"  // payload: GroupPostCreatedPayload

	StoryCreated = "StoryCreated" // payload: StoryCreatedPayload
	StoryViewed  = "StoryViewed"  // payload: StoryViewedPayload

	// Endorsements & Reputation (Phase 6)
	UserEndorsed = "UserEndorsed" // payload: UserEndorsedPayload

	// Business Pages (Phase 6)
	BusinessReviewCreated = "BusinessReviewCreated" // payload: BusinessReviewCreatedPayload

	// Monetization (Phase 7)
	SubscriptionCreated  = "SubscriptionCreated"  // payload: SubscriptionCreatedPayload
	SubscriptionCanceled = "SubscriptionCanceled" // payload: SubscriptionCanceledPayload
	PayoutRequested      = "PayoutRequested"      // payload: PayoutRequestedPayload

	// Video Analytics & Quality Scoring
	VideoImpression        = "VideoImpression"        // payload: VideoImpressionPayload
	VideoPlayStart         = "VideoPlayStart"         // payload: VideoPlayStartPayload
	VideoHeartbeat         = "VideoHeartbeat"         // payload: VideoHeartbeatPayload
	VideoMilestone         = "VideoMilestone"         // payload: VideoMilestonePayload
	VideoPlayEnd           = "VideoPlayEnd"           // payload: VideoPlayEndPayload
	VideoFollowFromContent = "VideoFollowFromContent" // payload: VideoEngagementPayload
	VideoNotInterested     = "VideoNotInterested"     // payload: VideoEngagementPayload
	VideoReport            = "VideoReport"            // payload: VideoEngagementPayload
	VideoBlockCreator      = "VideoBlockCreator"      // payload: VideoEngagementPayload

	// Trust & Safety
	ReportFiled = "ReportFiled" // payload: ReportFiledPayload

	// Shop / E-Commerce
	ProductListed      = "ProductListed"      // payload: ProductListedPayload
	OrderCreated       = "OrderCreated"       // payload: OrderCreatedPayload
	OrderStatusUpdated = "OrderStatusUpdated" // payload: OrderStatusUpdatedPayload

	// Live Streaming
	LiveStarted = "LiveStarted" // payload: LiveStartedPayload
	LiveEnded   = "LiveEnded"   // payload: LiveEndedPayload
)

// v2.1 new event types
const (
	EventVisibilityPolicyCreated = "visibility_policy.created"
	EventVisibilityPolicyUpdated = "visibility_policy.updated"
	EventVisibilityPolicyDeleted = "visibility_policy.deleted"
	EventPostVisibilityChanged   = "post.visibility_changed"
	EventListingCreated          = "listing.created"
	EventListingUpdated          = "listing.updated"
	EventOrderCreated            = "order.created"
	EventOrderStatusChanged      = "order.status_changed"
	EventBookingCreated          = "booking.created"
	EventBookingStatusChanged    = "booking.status_changed"
	EventPaymentSucceeded        = "payment.succeeded"
	EventPaymentFailed           = "payment.failed"
	EventPaymentRefunded         = "payment.refunded"
	EventDisputeOpened           = "dispute.opened"
	EventDisputeResolved         = "dispute.resolved"
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
	PostID          string    `json:"post_id"`
	AuthorID        string    `json:"author_id"`
	Text            string    `json:"text"`
	Visibility      string    `json:"visibility"`
	ContentType     string    `json:"content_type"`     // "post", "poll", "reel", "video"
	DurationSeconds int       `json:"duration_seconds"` // 0 for non-video
	CreatedAt       time.Time `json:"created_at"`
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

type UserUnsuspendedPayload struct {
	UserID        string    `json:"user_id"`
	AdminID       string    `json:"admin_id,omitempty"`
	UnsuspendedAt time.Time `json:"unsuspended_at"`
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

type FriendRequestDeclinedPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	DeclinedAt time.Time `json:"declined_at"`
}

type FriendRemovedPayload struct {
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	RemovedBy string    `json:"removed_by"`
	RemovedAt time.Time `json:"removed_at"`
}

type UserBlockedPayload struct {
	BlockerID string    `json:"blocker_id"`
	BlockedID string    `json:"blocked_id"`
	BlockedAt time.Time `json:"blocked_at"`
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

type StoryCreatedPayload struct {
	StoryID   string    `json:"story_id"`
	AuthorID  string    `json:"author_id"`
	MediaType string    `json:"media_type"`
	CreatedAt time.Time `json:"created_at"`
}

type StoryViewedPayload struct {
	StoryID  string    `json:"story_id"`
	AuthorID string    `json:"author_id"`
	ViewerID string    `json:"viewer_id"`
	ViewedAt time.Time `json:"viewed_at"`
}

type UserEndorsedPayload struct {
	FromUserID string    `json:"from_user_id"`
	ToUserID   string    `json:"to_user_id"`
	SkillTag   string    `json:"skill_tag"`
	CreatedAt  time.Time `json:"created_at"`
}

type BusinessReviewCreatedPayload struct {
	PageID     string    `json:"page_id"`
	PageOwner  string    `json:"page_owner_id"`
	ReviewerID string    `json:"reviewer_id"`
	Rating     int       `json:"rating"`
	CreatedAt  time.Time `json:"created_at"`
}

type SubscriptionCreatedPayload struct {
	SubscriptionID string  `json:"subscription_id"`
	SubscriberID   string  `json:"subscriber_id"`
	CreatorID      string  `json:"creator_id"`
	TierName       string  `json:"tier_name"`
	Price          float64 `json:"price"`
	Currency       string  `json:"currency"`
	CreatedAt      time.Time `json:"created_at"`
}

type SubscriptionCanceledPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	CanceledAt     time.Time `json:"canceled_at"`
}

type PayoutRequestedPayload struct {
	UserID      string    `json:"user_id"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency"`
	MethodID    string    `json:"method_id"`
	RequestedAt time.Time `json:"requested_at"`
}

// --- Video Analytics Payloads ---

type VideoImpressionPayload struct {
	ContentID    string `json:"content_id"`
	CreatorID    string `json:"creator_id"`
	ViewerID     string `json:"viewer_id"`
	SessionID    string `json:"session_id"`
	Surface      string `json:"surface"`
	VisibleMS    int64  `json:"visible_ms"`
	DeviceIDHash string `json:"device_id_hash"`
	Country      string `json:"country"`
	IsAutoplay   bool   `json:"is_autoplay"`
}

type VideoPlayStartPayload struct {
	ContentID         string `json:"content_id"`
	CreatorID         string `json:"creator_id"`
	ViewerID          string `json:"viewer_id"`
	SessionID         string `json:"session_id"`
	Surface           string `json:"surface"`
	ContentType       string `json:"content_type"` // reel, long_video
	ContentDurationMS int64  `json:"content_duration_ms"`
	StartMethod       string `json:"start_method"` // autoplay, tap, resume
	IsAutoplay        bool   `json:"is_autoplay"`
	DeviceIDHash      string `json:"device_id_hash"`
	Country           string `json:"country"`
}

type VideoHeartbeatPayload struct {
	ContentID          string  `json:"content_id"`
	ViewerID           string  `json:"viewer_id"`
	SessionID          string  `json:"session_id"`
	WatchedMSIncrement int64   `json:"watched_ms_increment"`
	WatchedMSTotal     int64   `json:"watched_ms_total"`
	PlayheadPositionMS int64   `json:"playhead_position_ms"`
	PlaybackSpeed      float64 `json:"playback_speed"`
	LoopCount          int     `json:"loop_count"`
}

type VideoMilestonePayload struct {
	ContentID     string `json:"content_id"`
	CreatorID     string `json:"creator_id"`
	ViewerID      string `json:"viewer_id"`
	SessionID     string `json:"session_id"`
	ContentType   string `json:"content_type"`
	MilestoneType string `json:"milestone_type"` // VIEW_1S, VIEW_3S, PCT_25, etc.
	WatchedMS     int64  `json:"watched_ms"`
}

type VideoPlayEndPayload struct {
	ContentID            string  `json:"content_id"`
	CreatorID            string  `json:"creator_id"`
	ViewerID             string  `json:"viewer_id"`
	SessionID            string  `json:"session_id"`
	ContentType          string  `json:"content_type"`
	ContentDurationMS    int64   `json:"content_duration_ms"`
	WatchedMSTotal       int64   `json:"watched_ms_total"`
	MaxContinuousWatchMS int64   `json:"max_continuous_watch_ms"`
	PercentViewed        float64 `json:"percent_viewed"`
	LoopCount            int     `json:"loop_count"`
	EndReason            string  `json:"end_reason"` // swipe_next, back, ended, background, error
	Surface              string  `json:"surface"`
	Country              string  `json:"country"`
	DeviceIDHash         string  `json:"device_id_hash"`
	IsAutoplay           bool    `json:"is_autoplay"`
}

// VideoEngagementPayload is used for like, share, save, follow_from_content,
// not_interested, report, block_creator events on video content.
type VideoEngagementPayload struct {
	ContentID string `json:"content_id"`
	CreatorID string `json:"creator_id"`
	ViewerID  string `json:"viewer_id"`
	SessionID string `json:"session_id"`
	Surface   string `json:"surface"`
	Action    string `json:"action"` // like, share, save, follow, not_interested, report, block
}

// --- Trust & Safety Payloads ---

type ReportFiledPayload struct {
	ReportID   string    `json:"report_id"`
	ReporterID string    `json:"reporter_id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// --- Shop / E-Commerce Payloads ---

type ProductListedPayload struct {
	ProductID string    `json:"product_id"`
	SellerID  string    `json:"seller_id"`
	Title     string    `json:"title"`
	Price     float64   `json:"price"`
	Currency  string    `json:"currency"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
}

type OrderCreatedPayload struct {
	OrderID   string    `json:"order_id"`
	BuyerID   string    `json:"buyer_id"`
	SellerID  string    `json:"seller_id"`
	Total     float64   `json:"total"`
	Currency  string    `json:"currency"`
	ItemCount int       `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
}

type OrderStatusUpdatedPayload struct {
	OrderID   string    `json:"order_id"`
	BuyerID   string    `json:"buyer_id"`
	SellerID  string    `json:"seller_id"`
	OldStatus string    `json:"old_status"`
	NewStatus string    `json:"new_status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- Live Streaming Payloads ---

type LiveStartedPayload struct {
	StreamID  string    `json:"stream_id"`
	HostID    string    `json:"host_id"`
	Title     string    `json:"title"`
	StartedAt time.Time `json:"started_at"`
}

type LiveEndedPayload struct {
	StreamID     string    `json:"stream_id"`
	HostID       string    `json:"host_id"`
	DurationSecs int       `json:"duration_secs"`
	PeakViewers  int       `json:"peak_viewers"`
	TotalViewers int       `json:"total_viewers"`
	EndedAt      time.Time `json:"ended_at"`
}

// NewEnvelope creates an EventEnvelope with a new EventID and
// propagated TraceID from context.
func NewEnvelope(ctx context.Context, eventType string, actorUserID *string, payload json.RawMessage) EventEnvelope {
	traceID := trace.TraceIDFrom(ctx)

	return EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		TraceID:     traceID,
		ActorUserID: actorUserID,
		Payload:     payload,
	}
}
