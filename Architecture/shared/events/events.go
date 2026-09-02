package events

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/atpost/shared/o11y/trace"
	"github.com/google/uuid"
)

// EventType constants for known domain events.
const (
	UserRegistered = "UserRegistered" // payload: UserRegisteredPayload
	UserLoggedIn   = "UserLoggedIn"   // payload: UserLoggedInPayload

	PostCreated             = "PostCreated"             // payload: PostCreatedPayload
	PostDeleted             = "PostDeleted"             // payload: PostDeletedPayload
	PostContentTypeChanged  = "PostContentTypeChanged"  // payload: PostContentTypeChangedPayload
	PostDistributionUpdated = "PostDistributionUpdated" // payload: PostDistributionUpdatedPayload
	// PostSearchEligibilityChanged is the single contract for every change
	// to a post's public-search eligibility (Module 2 M2-P0-2).
	PostSearchEligibilityChanged = "PostSearchEligibilityChanged" // payload: PostSearchEligibilityChangedPayload
	UserFollowed                 = "UserFollowed"                 // payload: UserFollowedPayload
	UserUnfollowed               = "UserUnfollowed"               // payload: UserUnfollowedPayload

	PostReacted        = "PostReacted"        // payload: PostReactedPayload
	CommentReacted     = "CommentReacted"     // payload: CommentReactedPayload
	CommentCreated     = "CommentCreated"     // payload: CommentCreatedPayload
	UserProfileUpdated = "UserProfileUpdated" // payload: UserProfileUpdatedPayload
	ContentTakenDown   = "ContentTakenDown"   // payload: ContentTakenDownPayload
	UserSuspended      = "UserSuspended"      // payload: UserSuspendedPayload
	UserUnsuspended    = "UserUnsuspended"    // payload: UserUnsuspendedPayload

	MediaTranscodeRequested = "MediaTranscodeRequested" // payload: MediaTranscodeRequestedPayload
	MediaTranscodeCompleted = "MediaTranscodeCompleted" // payload: MediaTranscodeCompletedPayload
	// MediaVoiceSafetyResolved carries the terminal voice-safety verdict
	// so post-service can release or reject a held voice post
	// (Module 1 fixes-v2 / Codex P0-2). Payload: MediaVoiceSafetyResolvedPayload.
	MediaVoiceSafetyResolved = "MediaVoiceSafetyResolved"

	// Connection lifecycle (messaging/privacy spec v2 §7.1 — the canonical
	// backend term is "connection", formerly "friend").
	ConnectionRequested        = "ConnectionRequested"        // payload: ConnectionRequestedPayload
	ConnectionAccepted         = "ConnectionAccepted"         // payload: ConnectionAcceptedPayload
	ConnectionDeclined         = "ConnectionDeclined"         // payload: ConnectionDeclinedPayload
	ConnectionRequestCancelled = "ConnectionRequestCancelled" // payload: ConnectionRequestCancelledPayload
	ConnectionRemoved          = "ConnectionRemoved"          // payload: ConnectionRemovedPayload
	UserBlocked                = "UserBlocked"                // payload: UserBlockedPayload
	UserUnblocked              = "UserUnblocked"              // payload: UserUnblockedPayload

	// Close-friends ("Trusted Circle") membership — friends-sheets spec §3.1.
	CloseFriendAdded   = "CloseFriendAdded"   // payload: CloseFriendChangedPayload
	CloseFriendRemoved = "CloseFriendRemoved" // payload: CloseFriendChangedPayload

	// Connection-request auto-filter — friends-sheets spec §5.1, §9.2.
	// Emitted by trust-safety after it moves a request to the filtered queue.
	ConnectionRequestFiltered = "ConnectionRequestFiltered" // payload: ConnectionRequestFilteredPayload

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

	// Live Streaming (v1 — RTMP/OBS; live-service)
	LiveStarted = "LiveStarted" // payload: LiveStartedPayload
	LiveEnded   = "LiveEnded"   // payload: LiveEndedPayload

	// Live Streaming v2 (LiveKit SFU; live-service-v2)
	LiveStreamStarted  = "live.stream.started"   // payload: LiveStreamStartedPayload
	LiveStreamEnded    = "live.stream.ended"     // payload: LiveStreamEndedPayload
	LiveStreamVODReady = "live.stream.vod_ready" // payload: LiveStreamVODReadyPayload
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
	EventQAQuestionPinned       = "qa.question.pinned"

	// Dating module — see C:\workspace\atpost\dating\PULSE_DATING_SPEC.md §11
	EventDatingProfileCreated        = "dating.profile.created"
	EventDatingProfileUpdated        = "dating.profile.updated"
	EventDatingProfilePaused         = "dating.profile.paused"
	EventDatingProfileDeleted        = "dating.profile.deleted"
	EventDatingSparkCreated          = "dating.spark.created"
	EventDatingSparkMatched          = "dating.spark.matched"
	EventDatingStashAdded            = "dating.stash.added"
	EventDatingStashRemoved          = "dating.stash.removed"
	EventDatingStashReactivated      = "dating.stash.reactivated"
	EventDatingMatchFormed           = "dating.match.formed"
	EventDatingMatchFirstMessage     = "dating.match.first_message"
	EventDatingMatchExpired          = "dating.match.expired"
	EventDatingMatchQuiet            = "dating.match.quiet"
	EventDatingMatchClosed           = "dating.match.closed"
	EventDatingVouchRequested        = "dating.vouch.requested"
	EventDatingVouchAccepted         = "dating.vouch.accepted"
	EventDatingVouchDeclined         = "dating.vouch.declined"
	EventDatingVouchRevoked          = "dating.vouch.revoked"
	EventDatingVerificationSubmitted = "dating.verification.submitted"
	EventDatingVerificationCompleted = "dating.verification.completed"
	EventDatingSafetyPanic           = "dating.safety.panic"
	EventDatingSafetyLocationShared  = "dating.safety.location_shared"
	EventDatingSafetyMeetScheduled   = "dating.safety.meet_scheduled"
	EventDatingSafetyMeetCheckin     = "dating.safety.meet_checkin"
	EventDatingSafetyMeetNoShow      = "dating.safety.meet_no_show"
	EventDatingReportCreated         = "dating.report.created"
	EventDatingBlockCreated          = "dating.block.created"
	EventDatingPremiumSubscribed     = "dating.premium.subscribed"
	EventDatingPremiumExpired        = "dating.premium.expired"
	// Sprint 4 — moderation (shadow + strict). Layer 1 = regex, Layer 2 = LLM.
	EventDatingModerationLayer1Result    = "dating.moderation.layer1.result"
	EventDatingModerationLayer2Requested = "dating.moderation.layer2.requested"
	EventDatingModerationLayer2Result    = "dating.moderation.layer2.result"
	// Sprint 5 — DPDP data export, profile purge, telemetry north-star.
	EventDatingDataExportRequested = "dating.data.export.requested"
	EventDatingDataExportReady     = "dating.data.export.ready"
	EventDatingProfilePurged       = "dating.profile.purged"
	EventDatingTelemetryNorthStar  = "dating.telemetry.north_star"

	// Phase 1 (§17, P1-6) — additional dating notification events. See
	// dating/PRODUCTION_GAP_ANALYSIS.md.
	EventDatingMatchQuietNotify        = "dating.match.quiet_notify"
	EventDatingSafeMeetReminder        = "dating.safe_meet.reminder"
	EventDatingSafeMeetMissedCheckIn   = "dating.safe_meet.missed_check_in"
	EventDatingSafetyPanicAcknowledged = "dating.safety.panic.acknowledged"
	EventDatingReportStatusUpdated     = "dating.report.status_updated"
	EventDatingVerificationRejected    = "dating.verification.rejected"
	EventDatingPhotoModerationRejected = "dating.photo.moderation_rejected"
	EventDatingPremiumPaymentFailure   = "dating.premium.payment_failure"
	EventDatingUserBlocked             = "dating.user.blocked"
	// Phase 1 — chat-side. Emitted by chat-service when a dating_match
	// conversation receives a message. Notification-service consumes it
	// to drive push when recipient isn't WS-connected.
	EventChatDatingMessageNew = "chat.dating.message.new"

	// Wallet (consumer wallet — BC of PPI). See services/wallet-service.
	EventWalletTopUpStarted    = "wallet.topup.started"
	EventWalletTopUpSucceeded  = "wallet.topup.succeeded"
	EventWalletTopUpFailed     = "wallet.topup.failed"
	EventWalletSendStarted     = "wallet.send.started"
	EventWalletSendSucceeded   = "wallet.send.succeeded"
	EventWalletSendFailed      = "wallet.send.failed"
	EventWalletReceiveCredited = "wallet.receive.credited"
	EventWalletMerchantDebited = "wallet.merchant.debited"
	EventWalletRefundIssued    = "wallet.refund.issued"
	EventWalletKYCCompleted    = "wallet.kyc.completed"
	EventWalletFrozen          = "wallet.frozen"
	EventWalletUnfrozen        = "wallet.unfrozen"

	// Bill-pay (Setu BBPS aggregator). See services/bill-pay-service.
	EventBillPayPaymentInitiated  = "billpay.payment.initiated"
	EventBillPayPaymentSucceeded  = "billpay.payment.succeeded"
	EventBillPayPaymentFailed     = "billpay.payment.failed"
	EventBillPayPaymentRefunded   = "billpay.payment.refunded"
	EventBillPayBillFetched       = "billpay.bill.fetched"
	EventBillPayBillDueSoon       = "billpay.bill.due_soon"
	EventBillPayScheduledExecuted = "billpay.scheduled.executed"
	EventBillPayScheduledFailed   = "billpay.scheduled.failed"
	EventBillPayAccountAdded      = "billpay.account.added"
	EventBillPayAccountRemoved    = "billpay.account.removed"

	// Food / FiGo module events
	EventFoodRestaurantCreated       = "food.restaurant.created"
	EventFoodRestaurantApproved      = "food.restaurant.approved"
	EventFoodRestaurantRejected      = "food.restaurant.rejected"
	EventFoodDeliveryPartnerCreated  = "food.delivery_partner.created"
	EventFoodDeliveryPartnerApproved = "food.delivery_partner.approved"
	EventFoodOrderPlaced             = "food.order.placed"
	EventFoodOrderPaymentSucceeded   = "food.order.payment_succeeded"
	EventFoodOrderPaymentFailed      = "food.order.payment_failed"
	EventFoodOrderConfirmed          = "food.order.confirmed"
	EventFoodOrderRestaurantAccepted = "food.order.restaurant_accepted"
	EventFoodOrderRestaurantRejected = "food.order.restaurant_rejected"
	EventFoodOrderPreparing          = "food.order.preparing"
	EventFoodOrderReadyForPickup     = "food.order.ready_for_pickup"
	EventFoodDeliveryAssigned        = "food.delivery.assigned"
	EventFoodDeliveryPickedUp        = "food.delivery.picked_up"
	EventFoodDeliveryDelivered       = "food.delivery.delivered"
	EventFoodOrderCancelled          = "food.order.cancelled"
	EventFoodOrderRefundRequested    = "food.order.refund_requested"
	EventFoodOrderRefunded           = "food.order.refunded"
	EventFoodRatingCreated           = "food.rating.created"
	EventFoodSettlementGenerated     = "food.settlement.generated"
	EventFoodSettlementPaid          = "food.settlement.paid"

	// Rider (Mopedu) — see C:\workspace\atpost\mopedu\MOPEDU_SPEC.md.
	EventRiderPartnerCreated               = "rider.partner.created"
	EventRiderPartnerKYCSubmitted          = "rider.partner.kyc_submitted"
	EventRiderPartnerKYCApproved           = "rider.partner.kyc_approved"
	EventRiderPartnerKYCRejected           = "rider.partner.kyc_rejected"
	EventRiderPartnerVehicleAdded          = "rider.partner.vehicle_added"
	EventRiderPartnerVehicleApproved       = "rider.partner.vehicle_approved"
	EventRiderPartnerVehicleRejected       = "rider.partner.vehicle_rejected"
	EventRiderPartnerApproved              = "rider.partner.approved"
	EventRiderPartnerSuspended             = "rider.partner.suspended"
	EventRiderPartnerBlocked               = "rider.partner.blocked"
	EventRiderPartnerOnline                = "rider.partner.online"
	EventRiderPartnerOffline               = "rider.partner.offline"
	EventRiderSubscriptionPaymentSubmitted = "rider.subscription.payment_submitted"
	EventRiderSubscriptionPaymentVerified  = "rider.subscription.payment_verified"
	EventRiderSubscriptionPaymentRejected  = "rider.subscription.payment_rejected"
	EventRiderSubscriptionActivated        = "rider.subscription.activated"
	EventRiderSubscriptionExpiring         = "rider.subscription.expiring"
	EventRiderSubscriptionExpired          = "rider.subscription.expired"
	EventRiderRideRequested                = "rider.ride.requested"
	EventRiderRideOffered                  = "rider.ride.offered"
	EventRiderRideOfferRejected            = "rider.ride.offer_rejected"
	EventRiderRideOfferExpired             = "rider.ride.offer_expired"
	EventRiderRideAssigned                 = "rider.ride.assigned"
	EventRiderRideArriving                 = "rider.ride.arriving"
	EventRiderRideArrived                  = "rider.ride.arrived"
	EventRiderRideStarted                  = "rider.ride.started"
	EventRiderRideCompleted                = "rider.ride.completed"
	EventRiderRideCancelled                = "rider.ride.cancelled"
	EventRiderRideExpired                  = "rider.ride.expired"
	EventRiderRideRated                    = "rider.ride.rated"
	EventRiderSafetySOS                    = "rider.safety.sos"
	EventRiderSafetyContactAlert           = "rider.safety.contact_alert"
	EventRiderSafetyIncidentAcknowledged   = "rider.safety.incident_acknowledged"
	EventRiderSafetyIncidentResolved       = "rider.safety.incident_resolved"
	EventRiderComplaintRaised              = "rider.complaint.raised"
	EventRiderComplaintUpdated             = "rider.complaint.updated"
	EventRiderShareTokenCreated            = "rider.share.token_created"
	EventRiderAuditAction                  = "rider.audit.action"
	EventRiderAdminAction                  = "rider.admin.action"

	// Sprint 4 — background jobs, document expiry, fraud, revenue rollup.
	EventRiderSubscriptionGracePeriod   = "rider.subscription.grace_period"
	EventRiderSubscriptionRenewed       = "rider.subscription.renewed"
	EventRiderSubscriptionRenewalFailed = "rider.subscription.renewal_failed"
	EventRiderDocumentExpiring          = "rider.document.expiring"
	EventRiderPartnerFraudFlagged       = "rider.partner.fraud_flagged"
	EventRiderDailyRevenueReport        = "rider.daily.revenue_report"
	EventRiderAdminQueueSummary         = "rider.admin.queue_summary"
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

	// Module 1 P0-1/P0-3 additive fields. Old producers omit them:
	// nil MainFeed / nil NotifySubscribers = legacy behavior (both true).
	MainFeed          *bool `json:"main_feed,omitempty"`
	NotifySubscribers *bool `json:"notify_subscribers,omitempty"`
	DistributionRev   int64 `json:"distribution_rev,omitempty"`
	// ChannelID is the author's canonical broadcast channel when one
	// exists — the subscriber fan-out key for PostTube uploads.
	ChannelID string `json:"channel_id,omitempty"`

	// ReviewStatus is the CANONICAL persisted moderation state of the post
	// row at publish time (Module 2 M2-P0-1).
	//
	// FAIL-CLOSED CONTRACT — read this before changing anything:
	// an empty/missing/unrecognized value means NOT ELIGIBLE for public
	// surfaces. It does NOT mean "legacy, therefore approved". Codex
	// explicitly rejected a legacy-permissive default because replayed
	// events, partially-deployed producers, and malformed payloads would
	// each reopen the exposure that this field exists to close.
	ReviewStatus string `json:"review_status,omitempty"`

	// SearchRev is the monotonic per-post search-eligibility revision.
	// Consumers apply an update only when it is strictly newer than the
	// revision they last applied, so replay and out-of-order delivery
	// cannot resurrect content. Creation is always revision 1.
	SearchRev int64 `json:"search_rev,omitempty"`
}

// PostSearchEligibilityChangedPayload is THE single contract for every
// change to a post's public-search eligibility (Module 2 M2-P0-2).
//
// One event covers approval, rejection, flagging, needs-changes,
// takedown, visibility change, and deletion, because search only cares
// about the resulting eligibility — not which internal path produced it.
// Every post-service path that mutates review_status or visibility MUST
// publish this transactionally with the row change (via the outbox), so
// no status-changing path can bypass the projection.
//
// Determinism (consumer side):
//
//	public + approved → idempotent upsert of the current document
//	anything else     → idempotent removal
//	deleted           → idempotent removal
//
// SearchRev is monotonic per post. A consumer MUST drop an event whose
// SearchRev is not greater than the revision it has already applied —
// that is what stops a late-delivered approval from resurrecting a post
// that was subsequently rejected or taken down.
type PostSearchEligibilityChangedPayload struct {
	PostID     string `json:"post_id"`
	AuthorID   string `json:"author_id"`
	Visibility string `json:"visibility"`
	// ReviewStatus is the canonical persisted value. Same fail-closed
	// contract as PostCreatedPayload.ReviewStatus.
	ReviewStatus string `json:"review_status"`
	// Deleted short-circuits to removal regardless of the other fields.
	Deleted bool `json:"deleted,omitempty"`
	// SearchRev is the monotonic revision. Required; a zero value is
	// treated as unusable and the consumer fails closed (removal).
	SearchRev int64 `json:"search_rev"`

	// Projection payload — the current canonical content, supplied so an
	// approval can index without a synchronous read-back into
	// post-service. Only meaningful when the post is eligible.
	Text        string    `json:"text,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`

	ChangedAt time.Time `json:"changed_at"`
}

// SearchEligible is the ONE place the eligibility rule is expressed, so
// producer and consumer cannot drift apart. Everything is normalized and
// allowlisted; anything unrecognized is ineligible.
//
// Module 2 M2-P0-1 acceptance: "empty, missing, malformed, pending,
// flagged, rejected, and needs_changes all fail closed."
func SearchEligible(visibility, reviewStatus string, deleted bool) bool {
	if deleted {
		return false
	}
	// Allowlist, not denylist: a new visibility or review value added in
	// future is ineligible until someone deliberately admits it here.
	if normalizeEligibilityToken(visibility) != "public" {
		return false
	}
	return normalizeEligibilityToken(reviewStatus) == "approved"
}

// normalizeEligibilityToken lowercases and trims a token for comparison.
// Kept deliberately strict: no aliasing, no prefix matching.
func normalizeEligibilityToken(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// MediaVoiceSafetyResolvedPayload is the terminal verdict for a voice
// asset's safety evaluation. ModerationStatus is one of:
//
//	approved — an evaluation ran and found the content safe
//	rejected — an evaluation ran and found the content unsafe
//	failed   — no verdict could be produced → manual review, NOT approval
//
// Consumers must be idempotent: the verdict may be delivered more than
// once, and the post transition is guarded to only apply from 'pending'.
type MediaVoiceSafetyResolvedPayload struct {
	MediaID          string `json:"media_id"`
	ModerationStatus string `json:"moderation_status"`
}

// PostDistributionUpdatedPayload is emitted (via the post-service outbox)
// whenever a post's distribution policy changes after creation. Consumers
// MUST treat DistributionRev monotonically: apply only if rev is greater
// than the last rev seen for the post; otherwise drop as stale.
type PostDistributionUpdatedPayload struct {
	PostID            string    `json:"post_id"`
	AuthorID          string    `json:"author_id"`
	ContentType       string    `json:"content_type"`
	MainFeed          bool      `json:"main_feed"`
	NotifySubscribers bool      `json:"notify_subscribers"`
	DistributionRev   int64     `json:"distribution_rev"`
	UpdatedAt         time.Time `json:"updated_at"`
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

// PostContentTypeChangedPayload is published by post-service when a
// reclassification flips a post between flick / long_video (or any
// other content_type transition). feed-service consumes this and
// updates the matching rows in social_feed.home_timeline_by_user
// and social_feed.author_timeline_by_author so the timeline-side
// content_type column doesn't drift from the source-of-truth in
// posts.content_type.
//
// Most commonly fires after MediaTranscodeCompleted lands real
// duration + dimensions on a video that was created with the
// safe-fallback content_type before transcode.
type PostContentTypeChangedPayload struct {
	PostID    string    `json:"post_id"`
	AuthorID  string    `json:"author_id"`
	OldType   string    `json:"old_type"`
	NewType   string    `json:"new_type"`
	ChangedAt time.Time `json:"changed_at"`
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

	// ModerationStatus is the transcode worker's frame-scan verdict
	// ("passed" / "rejected"); empty on the failed-transcode path.
	ModerationStatus string `json:"moderation_status,omitempty"`
}

type ConnectionRequestedPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type ConnectionAcceptedPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type ConnectionDeclinedPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	DeclinedAt time.Time `json:"declined_at"`
}

type ConnectionRequestCancelledPayload struct {
	SenderID    string    `json:"sender_id"`
	ReceiverID  string    `json:"receiver_id"`
	CancelledAt time.Time `json:"cancelled_at"`
}

type ConnectionRemovedPayload struct {
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

type UserUnblockedPayload struct {
	BlockerID   string    `json:"blocker_id"`
	BlockedID   string    `json:"blocked_id"`
	UnblockedAt time.Time `json:"unblocked_at"`
}

// CloseFriendChangedPayload is emitted on CloseFriendAdded / CloseFriendRemoved.
// feed-service consumes it to refresh the author's close-friends audience cache.
type CloseFriendChangedPayload struct {
	OwnerID    string    `json:"owner_id"`
	MemberID   string    `json:"member_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// ConnectionRequestFilteredPayload is emitted by trust-safety when a connection
// request is auto-filtered into the recipient's hidden queue (spec §9.2).
type ConnectionRequestFilteredPayload struct {
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	Reason     string    `json:"reason"`
	FilteredAt time.Time `json:"filtered_at"`
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

// --- Live Streaming v2 (LiveKit) Payloads ---
//
// Emitted by live-service-v2 (LiveKit SFU + Egress recording). The
// canonical key path is dot-namespaced ("live.stream.started") so the
// notification/feed consumers can route via prefix without colliding
// with the v1 RTMP "LiveStarted" message.

type LiveStreamStartedPayload struct {
	StreamID   string    `json:"stream_id"`
	CreatorID  string    `json:"creator_id"`
	Title      string    `json:"title"`
	Visibility string    `json:"visibility"` // public | followers | paid
	StartedAt  time.Time `json:"started_at"`
}

type LiveStreamEndedPayload struct {
	StreamID   string    `json:"stream_id"`
	CreatorID  string    `json:"creator_id"`
	EndedAt    time.Time `json:"ended_at"`
	ViewerPeak int       `json:"viewer_peak"`
}

type LiveStreamVODReadyPayload struct {
	StreamID     string `json:"stream_id"`
	CreatorID    string `json:"creator_id"`
	RecordingURL string `json:"recording_url"`
	DurationSec  int    `json:"duration_sec"`
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

// ── Module 4 M4-P0-4 — story moderation contract ───────────────────────────
//
// post-service emits StoryModerationRequested from the SAME transaction that
// creates the pending story, so a story can never exist without a request.
// trust-safety-service evaluates it and emits StoryModerationDecided.
// post-service applies the decision, and only it owns story publication state —
// trust-safety owns the evidence, not the row.
const (
	StoryModerationRequested = "StoryModerationRequested"
	StoryModerationDecided   = "StoryModerationDecided"
)

// StoryModerationRequestedPayload is the immutable snapshot under review.
//
// ContentRevision is what makes a decision safe to apply late: it pins the
// decision to the exact content that was evaluated, so a decision that arrives
// after the story changed cannot approve what is there now.
type StoryModerationRequestedPayload struct {
	StoryID         string `json:"story_id"`
	AuthorID        string `json:"author_id"`
	MediaID         string `json:"media_id"`
	MediaType       string `json:"media_type"`
	Caption         string `json:"caption"`
	ContentRevision int64  `json:"content_revision"`
}

// Story moderation terminal states. There is no "approved by default": a
// decision must be one of these three, and anything else is refused by the
// applying store.
const (
	StoryDecisionApproved     = "approved"
	StoryDecisionRejected     = "rejected"
	StoryDecisionManualReview = "manual_review"
)

// StoryModerationDecidedPayload is the terminal decision.
//
// DecisionID makes application idempotent and auditable; PolicyVersion records
// which ruleset produced it, so a later policy change does not silently
// reinterpret old evidence.
type StoryModerationDecidedPayload struct {
	StoryID         string `json:"story_id"`
	ContentRevision int64  `json:"content_revision"`
	Decision        string `json:"decision"`
	Reason          string `json:"reason,omitempty"`
	DecisionID      string `json:"decision_id"`
	PolicyVersion   string `json:"policy_version"`
	Issuer          string `json:"issuer"`
	Purpose         string `json:"purpose"`
	IssuedAtUnix    int64  `json:"issued_at_unix"`
	ExpiresAtUnix   int64  `json:"expires_at_unix"`
	Capability      string `json:"capability"`
}

// ── Account control — deactivate / delete / purge (auth-service) ────────────
//
// TikTok-style account lifecycle, emitted by identity auth-service via its
// transactional outbox onto identity.events.v1.
//
// Deactivation is fully reversible: the account stops being usable (sessions
// revoked, status 'deactivated') and the next successful password login
// reactivates it. Consumers should HIDE the user's presence, not erase data.
//
// Deletion is a scheduled purge with a 30-day recovery window. At request
// time ONLY user.deletion_scheduled is emitted — deliberately NOT
// user.deletion_requested, whose existing consumers (graph/post/user/dating)
// erase edges immediately and would turn a cancellable request into partial
// irreversible erasure. Consumers of user.deletion_scheduled should hide the
// account, and must be prepared for user.deletion_cancelled to undo that.
//
// user.purge_requested is the point of no return: emitted by the auth-side
// purge worker once the 30-day window has elapsed. Every service in the
// required set erases its slice and acks onto the purge-acks topic
// (platform.purge-acks.v1) with {"user_id","service","purged_at"}. Only when
// EVERY required service has acked does auth anonymise credentials and emit
// user.purged. Purge consumers must be idempotent: the worker re-emits
// user.purge_requested every 24h until the acks arrive.
const (
	EventUserDeactivated       = "user.deactivated"        // payload: UserDeactivatedPayload
	EventUserReactivated       = "user.reactivated"        // payload: UserReactivatedPayload
	EventUserDeletionScheduled = "user.deletion_scheduled" // payload: UserDeletionScheduledPayload
	EventUserDeletionCancelled = "user.deletion_cancelled" // payload: UserDeletionCancelledPayload
	EventUserPurgeRequested    = "user.purge_requested"    // payload: UserPurgeRequestedPayload
	EventUserPurged            = "user.purged"             // payload: UserPurgedPayload
)

// UserDeactivatedPayload — the user chose "Deactivate". Hide, don't erase.
type UserDeactivatedPayload struct {
	UserID        string    `json:"user_id"`
	DeactivatedAt time.Time `json:"deactivated_at"`
}

// UserReactivatedPayload — a deactivated user logged back in. Unhide.
type UserReactivatedPayload struct {
	UserID        string    `json:"user_id"`
	ReactivatedAt time.Time `json:"reactivated_at"`
}

// UserDeletionScheduledPayload — deletion requested; purge is scheduled, not
// begun. Reversible until ScheduledPurgeDate by the user logging in.
type UserDeletionScheduledPayload struct {
	UserID             string    `json:"user_id"`
	RequestedAt        time.Time `json:"requested_at"`
	ScheduledPurgeDate time.Time `json:"scheduled_purge_date"`
}

// UserDeletionCancelledPayload — the user logged in during the recovery
// window. Consumers that hid the account on user.deletion_scheduled unhide it.
type UserDeletionCancelledPayload struct {
	UserID      string    `json:"user_id"`
	CancelledAt time.Time `json:"cancelled_at"`
}

// UserPurgeRequestedPayload — the 30-day window elapsed. Each required
// service erases its slice and acks {"user_id","service","purged_at"} onto
// the purge-acks topic. Idempotent: may be re-delivered every 24h until acked.
type UserPurgeRequestedPayload struct {
	UserID      string    `json:"user_id"`
	RequestedAt time.Time `json:"requested_at"`
}

// UserPurgedPayload — every required service acked and auth anonymised the
// credential row. Terminal; the user id will never be seen again.
type UserPurgedPayload struct {
	UserID   string    `json:"user_id"`
	PurgedAt time.Time `json:"purged_at"`
}

// ── Private accounts — follow requests (graph-service) ──────────────────────
//
// A follow of a PRIVATE account becomes a follow REQUEST. Both events are
// written to graph_outbox_events in the same transaction as the row change,
// so they carry the outbox delivery guarantee. On accept, the canonical
// UserFollowed event is emitted alongside GraphFollowRequestAccepted — the
// accept IS the moment the follow edge becomes real.
const (
	GraphFollowRequested       = "graph.follow_requested"        // payload: FollowRequestedPayload
	GraphFollowRequestAccepted = "graph.follow_request_accepted" // payload: FollowRequestAcceptedPayload
)

// FollowRequestedPayload announces a new (or re-sent) pending follow request
// toward a private account. notification-service surfaces it in the target's
// request inbox.
type FollowRequestedPayload struct {
	RequesterID string    `json:"requester_id"`
	TargetID    string    `json:"target_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// FollowRequestAcceptedPayload announces that the target approved the
// request. The matching UserFollowed event carries the new edge itself.
type FollowRequestAcceptedPayload struct {
	RequesterID string    `json:"requester_id"`
	TargetID    string    `json:"target_id"`
	AcceptedAt  time.Time `json:"accepted_at"`
}

// Module 3 — module preferences and account visibility enforcement.
const (
	// UserModulesChanged is emitted by the identity user-service when a user
	// changes their module selection or home surface.
	// Payload: user_id, modules, home_module, occurred_at.
	UserModulesChanged = "user.modules_changed"

	// UserSettingsChanged is the identity user-service settings-changed
	// signal ({event_type, payload} envelope on identity.events.v1).
	// Payload: user_id, privacy_version, account_visibility (the NEW value),
	// occurred_at — graph-service uses account_visibility to auto-accept
	// pending follow requests on a private→public flip.
	UserSettingsChanged = "user.settings_changed"
)
