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
	UserBlocked           = "UserBlocked"           // payload: UserBlockedPayload

	GroupCreated       = "GroupCreated"       // payload: GroupCreatedPayload
	GroupMemberJoined  = "GroupMemberJoined"  // payload: GroupMemberJoinedPayload
	GroupMemberLeft    = "GroupMemberLeft"    // payload: GroupMemberLeftPayload
	GroupPostCreated   = "GroupPostCreated"   // payload: GroupPostCreatedPayload
	GroupPostDeleted   = "GroupPostDeleted"   // payload: GroupPostDeletedPayload
	GroupPostPinned    = "GroupPostPinned"    // payload: GroupPostPinnedPayload
	GroupPostUnpinned  = "GroupPostUnpinned"  // payload: GroupPostUnpinnedPayload
	GroupPostCommented = "GroupPostCommented" // payload: GroupPostCommentedPayload
	GroupPostSparked   = "GroupPostSparked"   // payload: GroupPostSparkedPayload
	MemberBanLifted    = "MemberBanLifted"    // payload: MemberBanLiftedPayload

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
	ReportFiled     = "ReportFiled"     // payload: ReportFiledPayload
	ReportResolved  = "ReportResolved"  // payload: ReportFiledPayload
	ReportDismissed = "ReportDismissed" // payload: ReportFiledPayload

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
	EventUserFollowed   = "user.followed"
	EventUserUnfollowed = "user.unfollowed"
	EventUserMuted      = "user.muted"

	EventUserDeletionRequested   = "user.deletion_requested"
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

	// Commerce — Seller lifecycle
	EventSellerSubmitted = "commerce.seller.submitted"
	EventSellerApproved  = "commerce.seller.approved"
	EventSellerRejected  = "commerce.seller.rejected"
	EventSellerSuspended = "commerce.seller.suspended"

	// Commerce — Product lifecycle
	EventProductApproved = "commerce.product.approved"

	// Commerce — Fulfillment lifecycle
	EventCommerceOrderPaid      = "commerce.order.paid"
	EventCommerceOrderShipped   = "commerce.order.shipped"
	EventCommerceOrderDelivered = "commerce.order.delivered"
	EventCommerceInvoiceIssued  = "commerce.invoice.issued"
	EventCommerceSellerNewOrder = "commerce.seller.new_order"

	// Feature Flags
	EventFlagEvaluated = "flag.evaluated" // payload: FlagEvaluatedPayload

	// Security
	EventUserLoginAnomaly = "user.login_anomaly" // payload: UserLoginAnomalyPayload

	// Spam / Content Safety
	EventSpamDetected = "content.spam_detected" // payload: SpamDetectedPayload

	// Mentions
	EventUserMentioned = "user.mentioned" // payload: UserMentionedPayload

	// Creator Analytics Events (reel engagement)
	EventReelLiked     = "reel.liked"
	EventReelCommented = "reel.commented"

	// Reel Lifecycle (Gold Spec)
	ReelDraftCreated         = "reel.draft.created"
	ReelDraftUpdated         = "reel.draft.updated"
	ReelPublishRequested     = "reel.publish.requested"
	ReelPublished            = "reel.published"
	ReelDeleted              = "reel.deleted"
	ReelViewed               = "reel.viewed"
	ReelBoostSet             = "reel.boost.set"
	ReelCommentCreated       = "reel.comment.created"
	ReelShared               = "reel.shared"
	ReelSaved                = "reel.saved"
	AudioTrackCreated        = "audio.track.created"
	AudioUsageIncremented    = "audio.usage.incremented"
	MediaProcessingProgress  = "media.processing.progress"
	MediaProcessingCompleted = "media.processing.completed"
	CrossPostCreated         = "crosspost.created"
	CrossPostCompleted       = "crosspost.completed"

	// Groups V2 Events
	GroupUpdated           = "group.updated"
	GroupDeleted           = "group.deleted"
	GroupArchived          = "group.archived"
	GroupMemberRemoved     = "group.member.removed"
	GroupMemberBanned      = "group.member.banned"
	GroupMemberRoleChanged = "group.member.role_changed"
	GroupInviteSent        = "group.invite.sent"
	GroupInviteAccepted    = "group.invite.accepted"
	GroupInviteRejected    = "group.invite.rejected"
	GroupJoinRequested     = "group.join.requested"
	GroupJoinApproved      = "group.join.approved"
	GroupJoinRejected      = "group.join.rejected"

	// Video Processing Lifecycle
	VideoUploaded  = "video.uploaded"
	VideoProcessed = "video.processed"
	VideoReady     = "video.ready"
	VideoFailed    = "video.failed"

	// Profile Sync + Cross-Post v3
	VideoPublished       = "video.published"
	FlickPublished       = "flick.published"
	CrosspostRemoved     = "crosspost.removed"
	ModuleProfileUpdated = "module_profile.updated"
	HandleChanged        = "handle.changed"
	UploadDeleted        = "upload.deleted"

	// Broadcast Channel Events
	EventChannelCreated         = "channel.created"
	EventChannelUpdated         = "channel.updated"
	EventChannelDeleted         = "channel.deleted"
	EventChannelSubscribed      = "channel.subscribed"
	EventChannelUnsubscribed    = "channel.unsubscribed"
	EventChannelUpdatePublished = "channel.update.published"
	EventChannelUpdateDeleted   = "channel.update.deleted"
	EventChannelMemberBanned    = "channel.member.banned"

	// Broadcast Channel Engagement Events
	EventChannelUpdateEchoed = "channel.update.echoed"

	// Broadcast Channel Comment Events (realtime)
	EventChannelCommentCreated = "channel.comment.created"
	EventChannelCommentDeleted = "channel.comment.deleted"
	EventChannelCommentUpdated = "channel.comment.updated"

	// Community Events
	EventCommunityCreated           = "community.created"
	EventCommunityUpdated           = "community.updated"
	EventCommunityDeleted           = "community.deleted"
	EventCommunityMemberJoined      = "community.member.joined"
	EventCommunityMemberLeft        = "community.member.left"
	EventCommunityMemberBanned      = "community.member.banned"
	EventCommunityMemberRoleChanged = "community.member.role_changed"
	EventCommunitySpaceCreated      = "community.space.created"
	EventCommunitySpaceRemoved      = "community.space.removed"
	EventCommunitySpaceQuarantined  = "community.space.quarantined"

	// Voice/Video Calling
	EventCallCreated            = "call.created"
	EventCallInvited            = "call.invited"
	EventCallAccepted           = "call.accepted"
	EventCallDeclined           = "call.declined"
	EventCallExpired            = "call.expired"
	EventCallJoined             = "call.joined"
	EventCallLeft               = "call.left"
	EventCallEnded              = "call.ended"
	EventCallParticipantMuted   = "call.participant.muted"
	EventCallParticipantRemoved = "call.participant.removed"
	EventCallUpgraded           = "call.upgraded"

	// Post Repost (Echo) Events
	EventPostReposted     = "post.reposted"
	EventPostRepostUndone = "post.repost_undone"

	// Q&A Events
	EventQAQuestionCreated      = "qa.question.created"
	EventQAQuestionUpdated      = "qa.question.updated"
	EventQAQuestionDeleted      = "qa.question.deleted"
	EventQAQuestionClosed       = "qa.question.closed"
	EventQAAnswerCreated        = "qa.answer.created"
	EventQAAnswerUpdated        = "qa.answer.updated"
	EventQAAnswerDeleted        = "qa.answer.deleted"
	EventQABestAnswerSelected   = "qa.answer.best_selected"
	EventQAAnswerCommentCreated = "qa.answer.comment.created"
	EventQAQuestionVoted        = "qa.question.voted"
	EventQAAnswerVoted          = "qa.answer.voted"
	EventQAAnswerRequested      = "qa.answer.requested"
	EventQAReputationChanged    = "qa.reputation.changed"
	EventQAQuestionReported     = "qa.question.reported"
	EventQAAnswerReported       = "qa.answer.reported"
	EventQAModerationAction     = "qa.moderation.action"
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

type UserUnfollowedPayload struct {
	FollowerID string    `json:"follower_id"`
	FolloweeID string    `json:"followee_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

type PostDeletedPayload struct {
	PostID    string    `json:"post_id"`
	AuthorID  string    `json:"author_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type UserDeletionRequestedPayload struct {
	UserID      string    `json:"user_id"`
	RequestedAt time.Time `json:"requested_at"`
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

	// Optional URLs surfaced once transcode succeeds. Empty on `failed` status.
	// HLSMasterURL points at the master.m3u8 generated by GenerateHLSVariants;
	// MP4URL is the single-bitrate fallback for clients without HLS support.
	// Consumers should prefer HLS when present and fall back to MP4 otherwise.
	HLSMasterURL string `json:"hls_master_url,omitempty"`
	MP4URL       string `json:"mp4_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
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

type GroupPostDeletedPayload struct {
	GroupID   string    `json:"group_id"`
	PostID    string    `json:"post_id"`
	DeletedBy string    `json:"deleted_by"`
	DeletedAt time.Time `json:"deleted_at"`
}

type GroupPostPinnedPayload struct {
	GroupID  string    `json:"group_id"`
	PostID   string    `json:"post_id"`
	PinnedBy string    `json:"pinned_by"`
	PinnedAt time.Time `json:"pinned_at"`
}

type GroupPostUnpinnedPayload struct {
	GroupID    string    `json:"group_id"`
	PostID     string    `json:"post_id"`
	UnpinnedBy string    `json:"unpinned_by"`
	UnpinnedAt time.Time `json:"unpinned_at"`
}

type GroupPostCommentedPayload struct {
	GroupID   string    `json:"group_id"`
	PostID    string    `json:"post_id"`
	CommentID string    `json:"comment_id"`
	AuthorID  string    `json:"author_id"`
	Body      string    `json:"body"`
	ParentID  string    `json:"parent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type GroupPostSparkedPayload struct {
	GroupID   string    `json:"group_id"`
	PostID    string    `json:"post_id"`
	UserID    string    `json:"user_id"`
	SparkedAt time.Time `json:"sparked_at"`
}

type MemberBanLiftedPayload struct {
	GroupID  string    `json:"group_id"`
	UserID   string    `json:"user_id"`
	LiftedBy string    `json:"lifted_by"`
	LiftedAt time.Time `json:"lifted_at"`
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
	SubscriptionID string    `json:"subscription_id"`
	SubscriberID   string    `json:"subscriber_id"`
	CreatorID      string    `json:"creator_id"`
	TierName       string    `json:"tier_name"`
	Price          float64   `json:"price"`
	Currency       string    `json:"currency"`
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

type ReportResolvedPayload struct {
	ReportID   string    `json:"report_id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	ActorID    string    `json:"actor_id"`
	ResolvedAt time.Time `json:"resolved_at"`
}

type ReportDismissedPayload struct {
	ReportID    string    `json:"report_id"`
	EntityType  string    `json:"entity_type"`
	EntityID    string    `json:"entity_id"`
	ActorID     string    `json:"actor_id"`
	DismissedAt time.Time `json:"dismissed_at"`
}

// --- Feature Flag Payloads ---

type FlagEvaluatedPayload struct {
	FlagKey string `json:"flag_key"`
	UserID  string `json:"user_id"`
	Enabled bool   `json:"enabled"`
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
	StreamID         string    `json:"stream_id"`
	HostID           string    `json:"host_id"`
	Title            string    `json:"title"`
	PlaybackURL      string    `json:"playback_url,omitempty"`
	PlaybackProtocol string    `json:"playback_protocol,omitempty"`
	StartedAt        time.Time `json:"started_at"`
}

type LiveEndedPayload struct {
	StreamID     string    `json:"stream_id"`
	HostID       string    `json:"host_id"`
	DurationSecs int       `json:"duration_secs"`
	PeakViewers  int       `json:"peak_viewers"`
	TotalViewers int       `json:"total_viewers"`
	EndedAt      time.Time `json:"ended_at"`
}

// --- Security Payloads ---

type UserLoginAnomalyPayload struct {
	UserID      string    `json:"user_id"`
	IP          string    `json:"ip"`
	DeviceID    string    `json:"device_id"`
	Platform    string    `json:"platform"`
	IsNewIP     bool      `json:"is_new_ip"`
	IsNewDevice bool      `json:"is_new_device"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// --- Spam / Content Safety Payloads ---

type SpamDetectedPayload struct {
	UserID string  `json:"user_id"`
	PostID string  `json:"post_id,omitempty"`
	Reason string  `json:"reason"`
	Score  float64 `json:"score"`
}

// --- Mention Payloads ---

type UserMentionedPayload struct {
	MentionedUserID string    `json:"mentioned_user_id"`
	AuthorID        string    `json:"author_id"`
	PostID          string    `json:"post_id"`
	CommentID       string    `json:"comment_id,omitempty"`
	OccurredAt      time.Time `json:"occurred_at"`
}

// --- Creator Analytics Payloads ---

type ReelLikedPayload struct {
	ReelID    string    `json:"reel_id"`
	UserID    string    `json:"user_id"`
	CreatorID string    `json:"creator_id"`
	LikedAt   time.Time `json:"liked_at"`
}

type ReelCommentedPayload struct {
	ReelID    string    `json:"reel_id"`
	UserID    string    `json:"user_id"`
	CreatorID string    `json:"creator_id"`
	CommentID string    `json:"comment_id"`
	CreatedAt time.Time `json:"created_at"`
}

// --- Reel Lifecycle Payloads (Gold Spec) ---

type ReelDraftCreatedPayload struct {
	DraftID   string    `json:"draft_id"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ReelDraftUpdatedPayload struct {
	DraftID   string    `json:"draft_id"`
	AuthorID  string    `json:"author_id"`
	Fields    []string  `json:"fields"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ReelPublishRequestedPayload struct {
	ReelID      string    `json:"reel_id"`
	AuthorID    string    `json:"author_id"`
	RequestedAt time.Time `json:"requested_at"`
}

type ReelPublishedPayload struct {
	ReelID      string    `json:"reel_id"`
	AuthorID    string    `json:"author_id"`
	Caption     string    `json:"caption"`
	Hashtags    []string  `json:"hashtags"`
	PublishedAt time.Time `json:"published_at"`
}

type ReelDeletedPayload struct {
	ReelID    string    `json:"reel_id"`
	AuthorID  string    `json:"author_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type ReelViewedPayload struct {
	ReelID    string `json:"reel_id"`
	ViewerID  string `json:"viewer_id"`
	SessionID string `json:"session_id"`
	WatchedMs int64  `json:"watched_ms"`
	Surface   string `json:"surface"`
}

type ReelBoostSetPayload struct {
	ReelID     string    `json:"reel_id"`
	BoostType  string    `json:"boost_type"`
	Multiplier float64   `json:"multiplier"`
	SetBy      string    `json:"set_by"`
	SetAt      time.Time `json:"set_at"`
}

type ReelCommentCreatedPayload struct {
	CommentID string    `json:"comment_id"`
	ReelID    string    `json:"reel_id"`
	AuthorID  string    `json:"author_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type ReelSharedPayload struct {
	ReelID    string    `json:"reel_id"`
	UserID    string    `json:"user_id"`
	ShareType string    `json:"share_type"`
	SharedAt  time.Time `json:"shared_at"`
}

type ReelSavedPayload struct {
	ReelID  string    `json:"reel_id"`
	UserID  string    `json:"user_id"`
	SavedAt time.Time `json:"saved_at"`
}

type AudioTrackCreatedPayload struct {
	AudioID       string    `json:"audio_id"`
	SourceMediaID string    `json:"source_media_id"`
	Title         string    `json:"title"`
	DurationMs    int64     `json:"duration_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

type AudioUsageIncrementedPayload struct {
	AudioID    string    `json:"audio_id"`
	UserID     string    `json:"user_id"`
	ReelID     string    `json:"reel_id"`
	UsageCount int       `json:"usage_count"`
	OccurredAt time.Time `json:"occurred_at"`
}

type MediaProcessingProgressPayload struct {
	MediaID    string    `json:"media_id"`
	Stage      string    `json:"stage"`
	Progress   float64   `json:"progress"`
	OccurredAt time.Time `json:"occurred_at"`
}

type MediaProcessingCompletedPayload struct {
	MediaID      string    `json:"media_id"`
	Status       string    `json:"status"`
	RenditionIDs []string  `json:"rendition_ids,omitempty"`
	CompletedAt  time.Time `json:"completed_at"`
}

type CrossPostCreatedPayload struct {
	CrossPostID  string    `json:"crosspost_id"`
	SourceReelID string    `json:"source_reel_id"`
	TargetType   string    `json:"target_type"`
	CreatedAt    time.Time `json:"created_at"`
}

type CrossPostCompletedPayload struct {
	CrossPostID  string    `json:"crosspost_id"`
	SourceReelID string    `json:"source_reel_id"`
	TargetType   string    `json:"target_type"`
	Status       string    `json:"status"`
	CompletedAt  time.Time `json:"completed_at"`
}

// VideoProcessedPayload is emitted after media processing extracts video metadata.
type VideoProcessedPayload struct {
	PostID           string  `json:"post_id"`
	MediaAssetID     string  `json:"media_asset_id"`
	DurationSeconds  float64 `json:"duration_seconds"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	Orientation      string  `json:"orientation"`
	ComputedCategory string  `json:"computed_category"`
}

// VideoReadyPayload is emitted when video transcoding is complete and playback URLs are available.
type VideoReadyPayload struct {
	PostID       string `json:"post_id"`
	PlaybackURL  string `json:"playback_url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// VideoFailedPayload is emitted when video processing fails.
type VideoFailedPayload struct {
	PostID string `json:"post_id"`
	Error  string `json:"error"`
}

// --- Profile Sync + Cross-Post v3 Payloads ---

type VideoPublishedPayload struct {
	PostID      string    `json:"post_id"`
	AuthorID    string    `json:"author_id"`
	Title       string    `json:"title"`
	Category    string    `json:"category"` // long_video or flick
	PublishedAt time.Time `json:"published_at"`
}

type FlickPublishedPayload struct {
	PostID      string    `json:"post_id"`
	AuthorID    string    `json:"author_id"`
	Caption     string    `json:"caption"`
	PublishedAt time.Time `json:"published_at"`
}

type CrosspostRemovedPayload struct {
	CrosspostID  string    `json:"crosspost_id"`
	SourcePostID string    `json:"source_post_id"`
	SourceModule string    `json:"source_module"`
	TargetPostID string    `json:"target_post_id"`
	RemovedAt    time.Time `json:"removed_at"`
}

type ModuleProfileUpdatedPayload struct {
	UserID    string    `json:"user_id"`
	Module    string    `json:"module"` // postbook, posttube, postgram
	UpdatedAt time.Time `json:"updated_at"`
}

type HandleChangedPayload struct {
	UserID      string    `json:"user_id"`
	OldUsername string    `json:"old_username"`
	NewUsername string    `json:"new_username"`
	ChangedAt   time.Time `json:"changed_at"`
}

type UploadDeletedPayload struct {
	PostID      string    `json:"post_id"`
	AuthorID    string    `json:"author_id"`
	ContentType string    `json:"content_type"`
	DeletedAt   time.Time `json:"deleted_at"`
}

// --- Groups V2 Payloads ---

type GroupUpdatedPayload struct {
	GroupID   string    `json:"group_id"`
	ActorID   string    `json:"actor_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GroupDeletedPayload struct {
	GroupID   string    `json:"group_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type GroupArchivedPayload struct {
	GroupID    string    `json:"group_id"`
	ActorID    string    `json:"actor_id"`
	ArchivedAt time.Time `json:"archived_at"`
}

type GroupMemberRemovedPayload struct {
	GroupID   string    `json:"group_id"`
	UserID    string    `json:"user_id"`
	RemovedBy string    `json:"removed_by"`
	RemovedAt time.Time `json:"removed_at"`
}

type GroupMemberBannedPayload struct {
	GroupID  string    `json:"group_id"`
	UserID   string    `json:"user_id"`
	BannedBy string    `json:"banned_by"`
	BannedAt time.Time `json:"banned_at"`
}

type GroupMemberRoleChangedPayload struct {
	GroupID   string    `json:"group_id"`
	UserID    string    `json:"user_id"`
	OldRole   string    `json:"old_role"`
	NewRole   string    `json:"new_role"`
	ChangedBy string    `json:"changed_by"`
	ChangedAt time.Time `json:"changed_at"`
}

type GroupInviteSentPayload struct {
	GroupID   string    `json:"group_id"`
	InviterID string    `json:"inviter_id"`
	InviteeID string    `json:"invitee_id"`
	InviteID  string    `json:"invite_id"`
	SentAt    time.Time `json:"sent_at"`
}

type GroupInviteAcceptedPayload struct {
	GroupID    string    `json:"group_id"`
	InviteID   string    `json:"invite_id"`
	UserID     string    `json:"user_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type GroupInviteRejectedPayload struct {
	GroupID    string    `json:"group_id"`
	InviteID   string    `json:"invite_id"`
	UserID     string    `json:"user_id"`
	RejectedAt time.Time `json:"rejected_at"`
}

type GroupJoinRequestedPayload struct {
	GroupID     string    `json:"group_id"`
	UserID      string    `json:"user_id"`
	RequestID   string    `json:"request_id"`
	RequestedAt time.Time `json:"requested_at"`
}

type GroupJoinApprovedPayload struct {
	GroupID    string    `json:"group_id"`
	UserID     string    `json:"user_id"`
	RequestID  string    `json:"request_id"`
	ApprovedBy string    `json:"approved_by"`
	ApprovedAt time.Time `json:"approved_at"`
}

type GroupJoinRejectedPayload struct {
	GroupID    string    `json:"group_id"`
	UserID     string    `json:"user_id"`
	RequestID  string    `json:"request_id"`
	RejectedBy string    `json:"rejected_by"`
	RejectedAt time.Time `json:"rejected_at"`
}

// ---------------------------------------------------------------------------
// Call event payloads
// ---------------------------------------------------------------------------

type CallInvitedPayload struct {
	CallID        string    `json:"call_id"`
	InviteID      string    `json:"invite_id"`
	InviterUserID string    `json:"inviter_user_id"`
	InviteeUserID string    `json:"invitee_user_id"`
	CallType      string    `json:"call_type"`
	CreatedAt     time.Time `json:"created_at"`
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

// NewEnvelope creates an EventEnvelope with a new EventID and
// propagated TraceID from context.
// --- Channel Comment Payloads ---

type ChannelCommentCreatedPayload struct {
	CommentID string    `json:"comment_id"`
	UpdateID  string    `json:"update_id"`
	ChannelID string    `json:"channel_id"`
	AuthorID  string    `json:"author_id"`
	Body      string    `json:"body"`
	ParentID  string    `json:"parent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ChannelCommentDeletedPayload struct {
	CommentID string    `json:"comment_id"`
	UpdateID  string    `json:"update_id"`
	ChannelID string    `json:"channel_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type ChannelCommentUpdatedPayload struct {
	CommentID string    `json:"comment_id"`
	UpdateID  string    `json:"update_id"`
	ChannelID string    `json:"channel_id"`
	ActorID   string    `json:"actor_id"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- Post Repost (Echo) Payloads ---

type PostRepostedPayload struct {
	RepostID          string    `json:"repost_id"`
	ReposterUserID    string    `json:"reposter_user_id"`
	OriginalPostID    string    `json:"original_post_id"`
	OriginalAuthorID  string    `json:"original_author_id"`
	RepostType        string    `json:"repost_type"` // "plain" or "quote"
	QuoteText         string    `json:"quote_text,omitempty"`
	Visibility        string    `json:"visibility"`
	SourceContextType string    `json:"source_context_type,omitempty"`
	SourceContextID   string    `json:"source_context_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type PostRepostUndonePayload struct {
	RepostID         string    `json:"repost_id"`
	ReposterUserID   string    `json:"reposter_user_id"`
	OriginalPostID   string    `json:"original_post_id"`
	OriginalAuthorID string    `json:"original_author_id"`
	RepostType       string    `json:"repost_type"`
	UndoneAt         time.Time `json:"undone_at"`
}

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
