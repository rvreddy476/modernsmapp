// Package events wraps Kafka publishing for the dating-service.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Producer is a thin wrapper around kafka.Writer with typed Publish helpers.
type Producer struct {
	writer *kafka.Writer
}

// NewProducer returns a Producer using the default dialer.
func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

// NewProducerWithDialer is the constructor used by main.go so TLS / SASL
// configuration from transport.KafkaDialerFromEnv flows through.
func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

// Close flushes and closes the underlying writer.
func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

// --- Profile events --------------------------------------------------------

type ProfileCreatedPayload struct {
	UserID    string    `json:"user_id"`
	Intent    string    `json:"intent"`
	CreatedAt time.Time `json:"created_at"`
}

type ProfileUpdatedPayload struct {
	UserID        string    `json:"user_id"`
	FieldsChanged []string  `json:"fields_changed"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ProfilePausedPayload struct {
	UserID   string    `json:"user_id"`
	Paused   bool      `json:"paused"`
	PausedAt time.Time `json:"paused_at"`
}

type ProfileDeletedPayload struct {
	UserID    string    `json:"user_id"`
	Reason    string    `json:"reason"`
	DeletedAt time.Time `json:"deleted_at"`
}

func (p *Producer) PublishProfileCreated(ctx context.Context, userID uuid.UUID, intent string) error {
	return p.publish(ctx, events.EventDatingProfileCreated, &userID, ProfileCreatedPayload{
		UserID: userID.String(), Intent: intent, CreatedAt: time.Now(),
	})
}

func (p *Producer) PublishProfileUpdated(ctx context.Context, userID uuid.UUID, fieldsChanged []string) error {
	return p.publish(ctx, events.EventDatingProfileUpdated, &userID, ProfileUpdatedPayload{
		UserID: userID.String(), FieldsChanged: fieldsChanged, UpdatedAt: time.Now(),
	})
}

func (p *Producer) PublishProfilePaused(ctx context.Context, userID uuid.UUID, paused bool) error {
	return p.publish(ctx, events.EventDatingProfilePaused, &userID, ProfilePausedPayload{
		UserID: userID.String(), Paused: paused, PausedAt: time.Now(),
	})
}

func (p *Producer) PublishProfileDeleted(ctx context.Context, userID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventDatingProfileDeleted, &userID, ProfileDeletedPayload{
		UserID: userID.String(), Reason: reason, DeletedAt: time.Now(),
	})
}

// --- Spark events ----------------------------------------------------------

type SparkCreatedPayload struct {
	SparkID    string         `json:"spark_id"`
	FromUserID string         `json:"from_user_id"`
	ToUserID   string         `json:"to_user_id"`
	TargetKind string         `json:"target_kind"`
	TargetRef  string         `json:"target_ref"`
	Note       string         `json:"note,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Extra      map[string]any `json:"extra,omitempty"`
}

type SparkMatchedPayload struct {
	MatchID   string    `json:"match_id"`
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	MatchedAt time.Time `json:"matched_at"`
}

func (p *Producer) PublishSparkCreated(ctx context.Context, sparkID, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string) error {
	return p.publish(ctx, events.EventDatingSparkCreated, &fromUserID, SparkCreatedPayload{
		SparkID: sparkID.String(), FromUserID: fromUserID.String(), ToUserID: toUserID.String(),
		TargetKind: targetKind, TargetRef: targetRef, Note: note, CreatedAt: time.Now(),
	})
}

func (p *Producer) PublishSparkMatched(ctx context.Context, matchID, userA, userB uuid.UUID) error {
	return p.publish(ctx, events.EventDatingSparkMatched, &userA, SparkMatchedPayload{
		MatchID: matchID.String(), UserA: userA.String(), UserB: userB.String(), MatchedAt: time.Now(),
	})
}

// --- Stash events ----------------------------------------------------------

type StashAddedPayload struct {
	UserID      string    `json:"user_id"`
	CandidateID string    `json:"candidate_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	AddedAt     time.Time `json:"added_at"`
}

type StashRemovedPayload struct {
	UserID      string    `json:"user_id"`
	CandidateID string    `json:"candidate_id"`
	Reason      string    `json:"reason,omitempty"`
	RemovedAt   time.Time `json:"removed_at"`
}

type StashReactivatedPayload struct {
	UserID        string    `json:"user_id"`
	CandidateID   string    `json:"candidate_id"`
	Signal        string    `json:"signal"`
	ReactivatedAt time.Time `json:"reactivated_at"`
}

func (p *Producer) PublishStashAdded(ctx context.Context, userID, candidateID uuid.UUID, expiresAt time.Time) error {
	return p.publish(ctx, events.EventDatingStashAdded, &userID, StashAddedPayload{
		UserID: userID.String(), CandidateID: candidateID.String(),
		ExpiresAt: expiresAt, AddedAt: time.Now(),
	})
}

func (p *Producer) PublishStashRemoved(ctx context.Context, userID, candidateID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventDatingStashRemoved, &userID, StashRemovedPayload{
		UserID: userID.String(), CandidateID: candidateID.String(),
		Reason: reason, RemovedAt: time.Now(),
	})
}

func (p *Producer) PublishStashReactivated(ctx context.Context, userID, candidateID uuid.UUID, signal string) error {
	return p.publish(ctx, events.EventDatingStashReactivated, &userID, StashReactivatedPayload{
		UserID: userID.String(), CandidateID: candidateID.String(),
		Signal: signal, ReactivatedAt: time.Now(),
	})
}

// --- Match events ----------------------------------------------------------

type MatchFormedPayload struct {
	MatchID        string    `json:"match_id"`
	UserA          string    `json:"user_a"`
	UserB          string    `json:"user_b"`
	ConversationID string    `json:"conversation_id"`
	FormedAt       time.Time `json:"formed_at"`
}

type MatchFirstMessagePayload struct {
	MatchID     string    `json:"match_id"`
	ActorID     string    `json:"actor_id"`
	RecipientID string    `json:"recipient_id"`
	FirstSentAt time.Time `json:"first_sent_at"`
}

type MatchExpiredPayload struct {
	MatchID   string    `json:"match_id"`
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	ExpiredAt time.Time `json:"expired_at"`
}

type MatchQuietPayload struct {
	MatchID string    `json:"match_id"`
	UserA   string    `json:"user_a"`
	UserB   string    `json:"user_b"`
	QuietAt time.Time `json:"quiet_at"`
}

type MatchClosedPayload struct {
	MatchID  string    `json:"match_id"`
	ClosedBy string    `json:"closed_by"`
	UserA    string    `json:"user_a"`
	UserB    string    `json:"user_b"`
	ClosedAt time.Time `json:"closed_at"`
}

func (p *Producer) PublishMatchFormed(ctx context.Context, matchID, userA, userB, conversationID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingMatchFormed, &userA, MatchFormedPayload{
		MatchID: matchID.String(), UserA: userA.String(), UserB: userB.String(),
		ConversationID: conversationID.String(), FormedAt: time.Now(),
	})
}

func (p *Producer) PublishMatchFirstMessage(ctx context.Context, matchID, actorID, recipientID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingMatchFirstMessage, &actorID, MatchFirstMessagePayload{
		MatchID: matchID.String(), ActorID: actorID.String(), RecipientID: recipientID.String(),
		FirstSentAt: time.Now(),
	})
}

func (p *Producer) PublishMatchExpired(ctx context.Context, matchID, userA, userB uuid.UUID) error {
	return p.publish(ctx, events.EventDatingMatchExpired, nil, MatchExpiredPayload{
		MatchID: matchID.String(), UserA: userA.String(), UserB: userB.String(),
		ExpiredAt: time.Now(),
	})
}

func (p *Producer) PublishMatchQuiet(ctx context.Context, matchID, userA, userB uuid.UUID) error {
	return p.publish(ctx, events.EventDatingMatchQuiet, nil, MatchQuietPayload{
		MatchID: matchID.String(), UserA: userA.String(), UserB: userB.String(),
		QuietAt: time.Now(),
	})
}

func (p *Producer) PublishMatchClosed(ctx context.Context, matchID, closedBy, userA, userB uuid.UUID) error {
	return p.publish(ctx, events.EventDatingMatchClosed, &closedBy, MatchClosedPayload{
		MatchID: matchID.String(), ClosedBy: closedBy.String(),
		UserA: userA.String(), UserB: userB.String(), ClosedAt: time.Now(),
	})
}

// --- Vouch events (Sprint 4) -----------------------------------------------

type VouchRequestedPayload struct {
	VouchID      string    `json:"vouch_id"`
	VoucherID    string    `json:"voucher_id"`
	VoucheeID    string    `json:"vouchee_id"`
	Relationship string    `json:"relationship,omitempty"`
	CommunityID  string    `json:"community_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type VouchDecisionPayload struct {
	VouchID   string    `json:"vouch_id"`
	VoucherID string    `json:"voucher_id"`
	VoucheeID string    `json:"vouchee_id"`
	DecidedAt time.Time `json:"decided_at"`
}

type VouchRevokedPayload struct {
	VouchID   string    `json:"vouch_id"`
	VoucherID string    `json:"voucher_id"`
	VoucheeID string    `json:"vouchee_id"`
	RevokedAt time.Time `json:"revoked_at"`
}

func (p *Producer) PublishVouchRequested(ctx context.Context, vouchID, voucherID, voucheeID uuid.UUID, relationship string, communityID *uuid.UUID) error {
	payload := VouchRequestedPayload{
		VouchID: vouchID.String(), VoucherID: voucherID.String(), VoucheeID: voucheeID.String(),
		Relationship: relationship, CreatedAt: time.Now(),
	}
	if communityID != nil {
		payload.CommunityID = communityID.String()
	}
	return p.publish(ctx, events.EventDatingVouchRequested, &voucherID, payload)
}

func (p *Producer) PublishVouchAccepted(ctx context.Context, vouchID, voucherID, voucheeID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingVouchAccepted, &voucheeID, VouchDecisionPayload{
		VouchID: vouchID.String(), VoucherID: voucherID.String(), VoucheeID: voucheeID.String(),
		DecidedAt: time.Now(),
	})
}

func (p *Producer) PublishVouchDeclined(ctx context.Context, vouchID, voucherID, voucheeID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingVouchDeclined, &voucheeID, VouchDecisionPayload{
		VouchID: vouchID.String(), VoucherID: voucherID.String(), VoucheeID: voucheeID.String(),
		DecidedAt: time.Now(),
	})
}

func (p *Producer) PublishVouchRevoked(ctx context.Context, vouchID, voucherID, voucheeID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingVouchRevoked, &voucherID, VouchRevokedPayload{
		VouchID: vouchID.String(), VoucherID: voucherID.String(), VoucheeID: voucheeID.String(),
		RevokedAt: time.Now(),
	})
}

// --- Verification events (Sprint 4) ----------------------------------------
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8

type VerificationSubmittedPayload struct {
	UserID      string    `json:"user_id"`
	Kind        string    `json:"kind"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type VerificationCompletedPayload struct {
	UserID      string    `json:"user_id"`
	Kind        string    `json:"kind"`
	TrustTier   string    `json:"trust_tier"`
	CompletedAt time.Time `json:"completed_at"`
}

func (p *Producer) PublishVerificationSubmitted(ctx context.Context, userID uuid.UUID, kind string) error {
	return p.publish(ctx, events.EventDatingVerificationSubmitted, &userID, VerificationSubmittedPayload{
		UserID: userID.String(), Kind: kind, SubmittedAt: time.Now(),
	})
}

func (p *Producer) PublishVerificationCompleted(ctx context.Context, userID uuid.UUID, kind, trustTier string) error {
	return p.publish(ctx, events.EventDatingVerificationCompleted, &userID, VerificationCompletedPayload{
		UserID: userID.String(), Kind: kind, TrustTier: trustTier, CompletedAt: time.Now(),
	})
}

// --- Safety events (Sprint 4) ----------------------------------------------

type SafetyPanicPayload struct {
	UserID    string         `json:"user_id"`
	Latitude  *float64       `json:"latitude,omitempty"`
	Longitude *float64       `json:"longitude,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
	FiredAt   time.Time      `json:"fired_at"`
}

type SafetyLocationSharedPayload struct {
	UserID    string    `json:"user_id"`
	ContactID string    `json:"contact_id"`
	ShareID   string    `json:"share_id"`
	ExpiresAt time.Time `json:"expires_at"`
	StartedAt time.Time `json:"started_at"`
}

type SafetyMeetScheduledPayload struct {
	MeetID      string    `json:"meet_id"`
	UserID      string    `json:"user_id"`
	WithUserID  string    `json:"with_user_id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	Venue       string    `json:"venue,omitempty"`
}

type SafetyMeetCheckinPayload struct {
	MeetID    string    `json:"meet_id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
}

type SafetyMeetNoShowPayload struct {
	MeetID     string    `json:"meet_id"`
	UserID     string    `json:"user_id"`
	WithUserID string    `json:"with_user_id"`
	DetectedAt time.Time `json:"detected_at"`
}

type ReportCreatedPayload struct {
	ReportID   string    `json:"report_id"`
	ReporterID string    `json:"reporter_id"`
	TargetID   string    `json:"target_id"`
	Category   string    `json:"category"`
	Details    string    `json:"details,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type BlockCreatedPayload struct {
	UserID    string    `json:"user_id"`
	BlockedID string    `json:"blocked_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (p *Producer) PublishSafetyPanic(ctx context.Context, userID uuid.UUID, lat, lng *float64, contextMeta map[string]any) error {
	return p.publish(ctx, events.EventDatingSafetyPanic, &userID, SafetyPanicPayload{
		UserID: userID.String(), Latitude: lat, Longitude: lng,
		Context: contextMeta, FiredAt: time.Now(),
	})
}

func (p *Producer) PublishSafetyLocationShared(ctx context.Context, userID, contactID, shareID uuid.UUID, expiresAt time.Time) error {
	return p.publish(ctx, events.EventDatingSafetyLocationShared, &userID, SafetyLocationSharedPayload{
		UserID: userID.String(), ContactID: contactID.String(), ShareID: shareID.String(),
		ExpiresAt: expiresAt, StartedAt: time.Now(),
	})
}

func (p *Producer) PublishSafetyMeetScheduled(ctx context.Context, meetID, userID, withUserID uuid.UUID, scheduledAt time.Time, venue string) error {
	return p.publish(ctx, events.EventDatingSafetyMeetScheduled, &userID, SafetyMeetScheduledPayload{
		MeetID: meetID.String(), UserID: userID.String(), WithUserID: withUserID.String(),
		ScheduledAt: scheduledAt, Venue: venue,
	})
}

func (p *Producer) PublishSafetyMeetCheckin(ctx context.Context, meetID, userID uuid.UUID, status string) error {
	return p.publish(ctx, events.EventDatingSafetyMeetCheckin, &userID, SafetyMeetCheckinPayload{
		MeetID: meetID.String(), UserID: userID.String(), Status: status, CheckedAt: time.Now(),
	})
}

func (p *Producer) PublishSafetyMeetNoShow(ctx context.Context, meetID, userID, withUserID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingSafetyMeetNoShow, &userID, SafetyMeetNoShowPayload{
		MeetID: meetID.String(), UserID: userID.String(), WithUserID: withUserID.String(),
		DetectedAt: time.Now(),
	})
}

func (p *Producer) PublishReportCreated(ctx context.Context, reportID, reporterID, targetID uuid.UUID, category, details string) error {
	return p.publish(ctx, events.EventDatingReportCreated, &reporterID, ReportCreatedPayload{
		ReportID: reportID.String(), ReporterID: reporterID.String(), TargetID: targetID.String(),
		Category: category, Details: details, CreatedAt: time.Now(),
	})
}

func (p *Producer) PublishBlockCreated(ctx context.Context, userID, blockedID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingBlockCreated, &userID, BlockCreatedPayload{
		UserID: userID.String(), BlockedID: blockedID.String(), CreatedAt: time.Now(),
	})
}

// --- Moderation events (Sprint 4) ------------------------------------------
//
// SHADOW MODE FOR v1: callers MUST pass action_taken="shadow" until the
// pulse_moderation_strict feature flag is flipped on.

type ModerationLayer1ResultPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id,omitempty"`
	Confidence     float64   `json:"confidence"`
	Patterns       []string  `json:"patterns"`
	ActionTaken    string    `json:"action_taken"`
	Suggestion     string    `json:"suggestion,omitempty"`
	ScannedAt      time.Time `json:"scanned_at"`
}

type ModerationLayer2RequestedPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id,omitempty"`
	Snippet        string    `json:"snippet"`
	RequestedAt    time.Time `json:"requested_at"`
}

type ModerationLayer2ResultPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	Confidence     float64   `json:"confidence"`
	Tonality       float64   `json:"tonality"`
	ActionTaken    string    `json:"action_taken"`
	ScannedAt      time.Time `json:"scanned_at"`
}

func (p *Producer) PublishModerationLayer1Result(ctx context.Context, payload ModerationLayer1ResultPayload) error {
	return p.publish(ctx, events.EventDatingModerationLayer1Result, nil, payload)
}

func (p *Producer) PublishModerationLayer2Requested(ctx context.Context, payload ModerationLayer2RequestedPayload) error {
	return p.publish(ctx, events.EventDatingModerationLayer2Requested, nil, payload)
}

func (p *Producer) PublishModerationLayer2Result(ctx context.Context, payload ModerationLayer2ResultPayload) error {
	return p.publish(ctx, events.EventDatingModerationLayer2Result, nil, payload)
}

// --- Premium events (Sprint 5) ---------------------------------------------
//
// PremiumSubscribed fires after a successful Razorpay payment.captured /
// subscription.charged that we have already deduped via
// dating_payment_events. PremiumExpired fires when subscription.completed
// or subscription.halted arrives.

type PremiumSubscribedPayload struct {
	UserID       string    `json:"user_id"`
	PlanID       string    `json:"plan_id"`
	ExpiresAt    time.Time `json:"expires_at"`
	Source       string    `json:"source,omitempty"`
	SubscribedAt time.Time `json:"subscribed_at"`
}

type PremiumExpiredPayload struct {
	UserID    string    `json:"user_id"`
	PlanID    string    `json:"plan_id"`
	ExpiredAt time.Time `json:"expired_at"`
}

func (p *Producer) PublishPremiumSubscribed(ctx context.Context, userID uuid.UUID, planID string, expiresAt time.Time, source string) error {
	return p.publish(ctx, events.EventDatingPremiumSubscribed, &userID, PremiumSubscribedPayload{
		UserID:       userID.String(),
		PlanID:       planID,
		ExpiresAt:    expiresAt,
		Source:       source,
		SubscribedAt: time.Now(),
	})
}

func (p *Producer) PublishPremiumExpired(ctx context.Context, userID uuid.UUID, planID string) error {
	return p.publish(ctx, events.EventDatingPremiumExpired, &userID, PremiumExpiredPayload{
		UserID:    userID.String(),
		PlanID:    planID,
		ExpiredAt: time.Now(),
	})
}

// --- DPDP data-export events (Sprint 5) ------------------------------------
//
// DataExportRequested is consumed by cmd/data-exporter; DataExportReady is
// produced by the exporter once the JSON blob + signed URL are ready.
// Notification-service surfaces both.

type DataExportRequestedPayload struct {
	ExportID    string    `json:"export_id"`
	UserID      string    `json:"user_id"`
	RequestedAt time.Time `json:"requested_at"`
}

type DataExportReadyPayload struct {
	ExportID          string    `json:"export_id"`
	UserID            string    `json:"user_id"`
	DownloadURL       string    `json:"download_url"`
	DownloadExpiresAt time.Time `json:"download_expires_at"`
	CompletedAt       time.Time `json:"completed_at"`
}

func (p *Producer) PublishDataExportRequested(ctx context.Context, exportID, userID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingDataExportRequested, &userID, DataExportRequestedPayload{
		ExportID:    exportID.String(),
		UserID:      userID.String(),
		RequestedAt: time.Now(),
	})
}

func (p *Producer) PublishDataExportReady(ctx context.Context, exportID, userID uuid.UUID, downloadURL string, downloadExpires time.Time) error {
	return p.publish(ctx, events.EventDatingDataExportReady, &userID, DataExportReadyPayload{
		ExportID:          exportID.String(),
		UserID:            userID.String(),
		DownloadURL:       downloadURL,
		DownloadExpiresAt: downloadExpires,
		CompletedAt:       time.Now(),
	})
}

// --- Profile purge (Sprint 5) ----------------------------------------------
//
// ProfilePurged fires from cmd/data-purger after the 30-day grace deletion
// completes. Payload carries no PII — only the user_id (so downstream
// services can drop derived state) and a reason discriminator.

type ProfilePurgedPayload struct {
	UserID    string    `json:"user_id"`
	Reason    string    `json:"reason"`
	PurgedAt  time.Time `json:"purged_at"`
}

func (p *Producer) PublishProfilePurged(ctx context.Context, userID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventDatingProfilePurged, &userID, ProfilePurgedPayload{
		UserID:   userID.String(),
		Reason:   reason,
		PurgedAt: time.Now(),
	})
}

// --- Phase 1 notification events (§P1-6) -----------------------------------
//
// Each event corresponds to a notification-surface contract in
// dating/PRODUCTION_GAP_ANALYSIS.md §17. Payloads are deliberately narrow
// — notification-service does the user-facing lookup via the profile
// preview endpoint.

type MatchQuietNotifyPayload struct {
	MatchID    string    `json:"match_id"`
	UserA      string    `json:"user_a"`
	UserB      string    `json:"user_b"`
	DetectedAt time.Time `json:"detected_at"`
}

type SafeMeetReminderPayload struct {
	MeetID      string    `json:"meet_id"`
	UserID      string    `json:"user_id"`
	WithUserID  string    `json:"with_user_id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	Venue       string    `json:"venue,omitempty"`
	FiredAt     time.Time `json:"fired_at"`
}

type SafeMeetMissedCheckInPayload struct {
	MeetID            string    `json:"meet_id"`
	UserID            string    `json:"user_id"`
	WithUserID        string    `json:"with_user_id"`
	ScheduledAt       time.Time `json:"scheduled_at"`
	ExpectedCheckInAt time.Time `json:"expected_check_in_at"`
	DetectedAt        time.Time `json:"detected_at"`
}

type SafetyPanicAcknowledgedPayload struct {
	UserID         string    `json:"user_id"`
	AcknowledgedBy string    `json:"acknowledged_by,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

type ReportStatusUpdatedPayload struct {
	ReportID   string    `json:"report_id"`
	ReporterID string    `json:"reporter_id"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type VerificationRejectedPayload struct {
	UserID     string    `json:"user_id"`
	Kind       string    `json:"kind"`
	Reason     string    `json:"reason,omitempty"`
	RejectedAt time.Time `json:"rejected_at"`
}

type PhotoModerationRejectedPayload struct {
	UserID     string    `json:"user_id"`
	PhotoID    string    `json:"photo_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	RejectedAt time.Time `json:"rejected_at"`
}

type PremiumPaymentFailurePayload struct {
	UserID     string    `json:"user_id"`
	PlanID     string    `json:"plan_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type UserBlockedPayload struct {
	BlockerID string    `json:"blocker_id"`
	BlockedID string    `json:"blocked_id"`
	BlockedAt time.Time `json:"blocked_at"`
}

func (p *Producer) PublishMatchQuietNotify(ctx context.Context, matchID, userA, userB uuid.UUID) error {
	return p.publish(ctx, events.EventDatingMatchQuietNotify, &userA, MatchQuietNotifyPayload{
		MatchID: matchID.String(), UserA: userA.String(), UserB: userB.String(),
		DetectedAt: time.Now(),
	})
}

func (p *Producer) PublishSafeMeetReminder(ctx context.Context, meetID, userID, withUserID uuid.UUID, scheduledAt time.Time, venue string) error {
	return p.publish(ctx, events.EventDatingSafeMeetReminder, &userID, SafeMeetReminderPayload{
		MeetID: meetID.String(), UserID: userID.String(), WithUserID: withUserID.String(),
		ScheduledAt: scheduledAt, Venue: venue, FiredAt: time.Now(),
	})
}

func (p *Producer) PublishSafeMeetMissedCheckIn(ctx context.Context, meetID, userID, withUserID uuid.UUID, scheduledAt, expectedCheckInAt time.Time) error {
	return p.publish(ctx, events.EventDatingSafeMeetMissedCheckIn, &userID, SafeMeetMissedCheckInPayload{
		MeetID: meetID.String(), UserID: userID.String(), WithUserID: withUserID.String(),
		ScheduledAt: scheduledAt, ExpectedCheckInAt: expectedCheckInAt, DetectedAt: time.Now(),
	})
}

func (p *Producer) PublishSafetyPanicAcknowledged(ctx context.Context, userID uuid.UUID, ackBy string) error {
	return p.publish(ctx, events.EventDatingSafetyPanicAcknowledged, &userID, SafetyPanicAcknowledgedPayload{
		UserID: userID.String(), AcknowledgedBy: ackBy, AcknowledgedAt: time.Now(),
	})
}

func (p *Producer) PublishReportStatusUpdated(ctx context.Context, reportID, reporterID uuid.UUID, status string) error {
	return p.publish(ctx, events.EventDatingReportStatusUpdated, &reporterID, ReportStatusUpdatedPayload{
		ReportID: reportID.String(), ReporterID: reporterID.String(), Status: status,
		UpdatedAt: time.Now(),
	})
}

func (p *Producer) PublishVerificationRejected(ctx context.Context, userID uuid.UUID, kind string) error {
	return p.publish(ctx, events.EventDatingVerificationRejected, &userID, VerificationRejectedPayload{
		UserID: userID.String(), Kind: kind, RejectedAt: time.Now(),
	})
}

func (p *Producer) PublishPhotoModerationRejected(ctx context.Context, userID uuid.UUID, photoID, reason string) error {
	return p.publish(ctx, events.EventDatingPhotoModerationRejected, &userID, PhotoModerationRejectedPayload{
		UserID: userID.String(), PhotoID: photoID, Reason: reason, RejectedAt: time.Now(),
	})
}

func (p *Producer) PublishPremiumPaymentFailure(ctx context.Context, userID uuid.UUID, planID, reason string) error {
	return p.publish(ctx, events.EventDatingPremiumPaymentFailure, &userID, PremiumPaymentFailurePayload{
		UserID: userID.String(), PlanID: planID, Reason: reason, OccurredAt: time.Now(),
	})
}

func (p *Producer) PublishUserBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	return p.publish(ctx, events.EventDatingUserBlocked, &blockerID, UserBlockedPayload{
		BlockerID: blockerID.String(), BlockedID: blockedID.String(), BlockedAt: time.Now(),
	})
}

// --- Telemetry north-star (Sprint 5) ---------------------------------------
//
// Emitted nightly by cmd/north-star with the spec §17 KPIs.

type TelemetryNorthStarPayload struct {
	WindowDays               int       `json:"window_days"`
	OffAppMeetRate           float64   `json:"off_app_meet_rate"`
	ConversationQualityScore float64   `json:"conversation_quality_score"`
	DAU                      int       `json:"dau"`
	SparksPerDay             int       `json:"sparks_per_day"`
	MatchesPerDay            int       `json:"matches_per_day"`
	PremiumConversionRate    float64   `json:"premium_conversion_rate"`
	GeneratedAt              time.Time `json:"generated_at"`
}

func (p *Producer) PublishTelemetryNorthStar(ctx context.Context, payload TelemetryNorthStarPayload) error {
	if payload.GeneratedAt.IsZero() {
		payload.GeneratedAt = time.Now()
	}
	return p.publish(ctx, events.EventDatingTelemetryNorthStar, nil, payload)
}

// publish marshals the payload, wraps it in the shared envelope and writes
// to Kafka. If the writer is nil (e.g. tests), it is a no-op.
func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
	if p == nil || p.writer == nil {
		return nil
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}
	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}
