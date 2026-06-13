// Dating event handlers — mirrors the qa_consumer.go pattern. Sprint 3
// covers spark.created, spark.matched, match.first_message, match.expired.
//
// The handler functions consume the typed payloads produced by
// dating-service/internal/events/producer.go.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// datingClient is a small read-only HTTP client over dating-service. We use
// it to look up the recipient's first name for human-readable notification
// titles. Profile-preview lookup is best-effort: a failure falls back to a
// generic title.
type datingClient struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func newDatingClient() *datingClient {
	baseURL := os.Getenv("DATING_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://dating-service:8112"
	}
	return &datingClient{
		baseURL:     baseURL,
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		http:        &http.Client{Timeout: 4 * time.Second},
	}
}

// datingProfilePreview is the minimal shape we need.
type datingProfilePreview struct {
	UserID    string `json:"user_id"`
	FirstName string `json:"first_name"`
}

// getPreview returns the user's first name; empty string on any error.
func (c *datingClient) getFirstName(ctx context.Context, userID string) string {
	if c == nil || userID == "" {
		return ""
	}
	url := fmt.Sprintf("%s/v1/dating/profile/%s/preview", c.baseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	var direct datingProfilePreview
	if err := json.NewDecoder(resp.Body).Decode(&direct); err == nil && direct.FirstName != "" {
		return direct.FirstName
	}
	return ""
}

// handleDatingEvent is the dispatch entry point invoked from the main
// consumer's processMessage when no other handler claimed the event.
// Returns true when the event was claimed (handled or knowingly ignored).
func (c *Consumer) handleDatingEvent(ctx context.Context, envelope events.EventEnvelope) (bool, error) {
	switch envelope.EventType {
	case events.EventDatingSparkCreated:
		return true, c.handleDatingSparkCreated(ctx, envelope.Payload)
	case events.EventDatingSparkMatched:
		return true, c.handleDatingSparkMatched(ctx, envelope.Payload)
	case events.EventDatingMatchFirstMessage:
		return true, c.handleDatingMatchFirstMessage(ctx, envelope.Payload)
	case events.EventDatingMatchExpired:
		return true, c.handleDatingMatchExpired(ctx, envelope.Payload)
	// Phase 1 (§P1-6) — additional dating notification events.
	case events.EventDatingVouchRequested:
		return true, c.handleDatingVouchRequested(ctx, envelope.Payload)
	case events.EventDatingVouchAccepted:
		return true, c.handleDatingVouchAccepted(ctx, envelope.Payload)
	case events.EventDatingMatchQuietNotify:
		return true, c.handleDatingMatchQuietNotify(ctx, envelope.Payload)
	case events.EventDatingSafeMeetReminder:
		return true, c.handleDatingSafeMeetReminder(ctx, envelope.Payload)
	case events.EventDatingSafeMeetMissedCheckIn:
		return true, c.handleDatingSafeMeetMissedCheckIn(ctx, envelope.Payload)
	case events.EventDatingSafetyPanicAcknowledged:
		return true, c.handleDatingSafetyPanicAcknowledged(ctx, envelope.Payload)
	case events.EventDatingReportStatusUpdated:
		return true, c.handleDatingReportStatusUpdated(ctx, envelope.Payload)
	case events.EventDatingVerificationCompleted:
		return true, c.handleDatingVerificationCompleted(ctx, envelope.Payload)
	case events.EventDatingVerificationRejected:
		return true, c.handleDatingVerificationRejected(ctx, envelope.Payload)
	case events.EventDatingPhotoModerationRejected:
		return true, c.handleDatingPhotoModerationRejected(ctx, envelope.Payload)
	case events.EventDatingPremiumPaymentFailure:
		return true, c.handleDatingPremiumPaymentFailure(ctx, envelope.Payload)
	case events.EventDatingDataExportReady:
		return true, c.handleDatingDataExportReady(ctx, envelope.Payload)
	case events.EventChatDatingMessageNew:
		return true, c.handleChatDatingMessageNew(ctx, envelope.Payload)
	case events.EventDatingMatchFormed,
		events.EventDatingMatchClosed,
		events.EventDatingMatchQuiet,
		events.EventDatingStashAdded,
		events.EventDatingStashRemoved,
		events.EventDatingStashReactivated,
		events.EventDatingProfileCreated,
		events.EventDatingProfileUpdated,
		events.EventDatingProfilePaused,
		events.EventDatingProfileDeleted,
		events.EventDatingVouchDeclined,
		events.EventDatingVouchRevoked,
		events.EventDatingUserBlocked,
		events.EventDatingVerificationSubmitted,
		events.EventDatingSafetyPanic,
		events.EventDatingSafetyLocationShared,
		events.EventDatingSafetyMeetScheduled,
		events.EventDatingSafetyMeetCheckin,
		events.EventDatingSafetyMeetNoShow,
		events.EventDatingReportCreated,
		events.EventDatingBlockCreated,
		events.EventDatingPremiumSubscribed,
		events.EventDatingPremiumExpired,
		events.EventDatingProfilePurged,
		events.EventDatingTelemetryNorthStar,
		events.EventDatingModerationLayer1Result,
		events.EventDatingModerationLayer2Requested,
		events.EventDatingModerationLayer2Result,
		events.EventDatingDataExportRequested:
		// Known-but-not-pushed; claim the event so the default branch
		// doesn't log a warning.
		return true, nil
	}
	return false, nil
}

// dating.spark.created → notify the recipient.
type datingSparkCreatedPayload struct {
	SparkID    string    `json:"spark_id"`
	FromUserID string    `json:"from_user_id"`
	ToUserID   string    `json:"to_user_id"`
	TargetKind string    `json:"target_kind"`
	TargetRef  string    `json:"target_ref"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (c *Consumer) handleDatingSparkCreated(ctx context.Context, raw json.RawMessage) error {
	var e datingSparkCreatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	to, err := uuid.Parse(e.ToUserID)
	if err != nil {
		return err
	}
	from, _ := uuid.Parse(e.FromUserID)
	sparkID, _ := uuid.Parse(e.SparkID)

	deepLink := fmt.Sprintf("/dating/sparks/incoming?spark_id=%s", e.SparkID)
	return c.service.CreateNotification(ctx, to, from, "dating.spark.created", "dating_spark", sparkID, deepLink, e.CreatedAt)
}

// dating.spark.matched → notify both participants.
type datingSparkMatchedPayload struct {
	MatchID   string    `json:"match_id"`
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	MatchedAt time.Time `json:"matched_at"`
}

func (c *Consumer) handleDatingSparkMatched(ctx context.Context, raw json.RawMessage) error {
	var e datingSparkMatchedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	a, errA := uuid.Parse(e.UserA)
	b, errB := uuid.Parse(e.UserB)
	if errA != nil || errB != nil {
		return fmt.Errorf("invalid user ids in spark.matched: a=%v b=%v", errA, errB)
	}
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)

	// Notify both directions.
	if err := c.service.CreateNotification(ctx, a, b, "dating.match.formed", "dating_match", matchID, deepLink, e.MatchedAt); err != nil {
		slog.Warn("dating: notify a failed", "user", a, "error", err)
	}
	return c.service.CreateNotification(ctx, b, a, "dating.match.formed", "dating_match", matchID, deepLink, e.MatchedAt)
}

// dating.match.first_message → notify the recipient.
type datingMatchFirstMessagePayload struct {
	MatchID     string    `json:"match_id"`
	ActorID     string    `json:"actor_id"`
	RecipientID string    `json:"recipient_id"`
	FirstSentAt time.Time `json:"first_sent_at"`
}

func (c *Consumer) handleDatingMatchFirstMessage(ctx context.Context, raw json.RawMessage) error {
	var e datingMatchFirstMessagePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	recipient, err := uuid.Parse(e.RecipientID)
	if err != nil {
		return err
	}
	actor, _ := uuid.Parse(e.ActorID)
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)
	return c.service.CreateNotification(ctx, recipient, actor, "dating.match.first_message", "dating_match", matchID, deepLink, e.FirstSentAt)
}

// dating.match.expired → notify the user who didn't reply (i.e. the one we
// know didn't trigger first_message — which is what made the match expire).
// Spec: "the user who didn't send the first message". We don't know which
// of UserA/UserB it was at this point because nobody sent one. Notify both.
type datingMatchExpiredPayload struct {
	MatchID   string    `json:"match_id"`
	UserA     string    `json:"user_a"`
	UserB     string    `json:"user_b"`
	ExpiredAt time.Time `json:"expired_at"`
}

func (c *Consumer) handleDatingMatchExpired(ctx context.Context, raw json.RawMessage) error {
	var e datingMatchExpiredPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	a, errA := uuid.Parse(e.UserA)
	b, errB := uuid.Parse(e.UserB)
	if errA != nil || errB != nil {
		return fmt.Errorf("invalid user ids in match.expired: a=%v b=%v", errA, errB)
	}
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)

	// No actor here — the match expired without anyone messaging. We pass
	// the *other* participant as the "actor" for the recipient's notif so
	// the UI can render their name.
	if err := c.service.CreateNotification(ctx, a, b, "dating.match.expired", "dating_match", matchID, deepLink, e.ExpiredAt); err != nil {
		slog.Warn("dating: expired notify a failed", "user", a, "error", err)
	}
	return c.service.CreateNotification(ctx, b, a, "dating.match.expired", "dating_match", matchID, deepLink, e.ExpiredAt)
}

// ---------------------------------------------------------------------------
// Phase 1 §P1-6 — additional dating notification handlers.
//
// Each handler decodes the dating-service producer payload (defined in
// services/dating-service/internal/events/producer.go) and routes a
// single CreateNotification call to the appropriate recipient.
// ---------------------------------------------------------------------------

type datingVouchRequestedPayload struct {
	VouchID   string    `json:"vouch_id"`
	VoucherID string    `json:"voucher_id"`
	VoucheeID string    `json:"vouchee_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Consumer) handleDatingVouchRequested(ctx context.Context, raw json.RawMessage) error {
	var e datingVouchRequestedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	vouchee, err := uuid.Parse(e.VoucheeID)
	if err != nil {
		return err
	}
	voucher, _ := uuid.Parse(e.VoucherID)
	vouchID, _ := uuid.Parse(e.VouchID)
	deepLink := fmt.Sprintf("/dating/vouches?incoming=%s", e.VouchID)
	return c.service.CreateNotification(ctx, vouchee, voucher, "dating.vouch.requested", "dating_vouch", vouchID, deepLink, e.CreatedAt)
}

type datingVouchAcceptedPayload struct {
	VouchID   string    `json:"vouch_id"`
	VoucherID string    `json:"voucher_id"`
	VoucheeID string    `json:"vouchee_id"`
	DecidedAt time.Time `json:"decided_at"`
}

func (c *Consumer) handleDatingVouchAccepted(ctx context.Context, raw json.RawMessage) error {
	var e datingVouchAcceptedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	voucher, err := uuid.Parse(e.VoucherID)
	if err != nil {
		return err
	}
	vouchee, _ := uuid.Parse(e.VoucheeID)
	vouchID, _ := uuid.Parse(e.VouchID)
	deepLink := fmt.Sprintf("/dating/vouches?id=%s", e.VouchID)
	return c.service.CreateNotification(ctx, voucher, vouchee, "dating.vouch.accepted", "dating_vouch", vouchID, deepLink, e.DecidedAt)
}

type datingMatchQuietNotifyPayload struct {
	MatchID    string    `json:"match_id"`
	UserA      string    `json:"user_a"`
	UserB      string    `json:"user_b"`
	DetectedAt time.Time `json:"detected_at"`
}

func (c *Consumer) handleDatingMatchQuietNotify(ctx context.Context, raw json.RawMessage) error {
	var e datingMatchQuietNotifyPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	a, errA := uuid.Parse(e.UserA)
	b, errB := uuid.Parse(e.UserB)
	if errA != nil || errB != nil {
		return fmt.Errorf("invalid user ids in match.quiet_notify: a=%v b=%v", errA, errB)
	}
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)
	if err := c.service.CreateNotification(ctx, a, b, "dating.match.quiet", "dating_match", matchID, deepLink, e.DetectedAt); err != nil {
		slog.Warn("dating: quiet notify a failed", "user", a, "error", err)
	}
	return c.service.CreateNotification(ctx, b, a, "dating.match.quiet", "dating_match", matchID, deepLink, e.DetectedAt)
}

type datingSafeMeetReminderPayload struct {
	MeetID      string    `json:"meet_id"`
	UserID      string    `json:"user_id"`
	WithUserID  string    `json:"with_user_id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	FiredAt     time.Time `json:"fired_at"`
}

func (c *Consumer) handleDatingSafeMeetReminder(ctx context.Context, raw json.RawMessage) error {
	var e datingSafeMeetReminderPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	withUser, _ := uuid.Parse(e.WithUserID)
	meetID, _ := uuid.Parse(e.MeetID)
	deepLink := fmt.Sprintf("/dating/safety/meets/%s", e.MeetID)
	return c.service.CreateNotification(ctx, user, withUser, "dating.safe_meet.reminder", "dating_meet", meetID, deepLink, e.FiredAt)
}

type datingSafeMeetMissedPayload struct {
	MeetID            string    `json:"meet_id"`
	UserID            string    `json:"user_id"`
	WithUserID        string    `json:"with_user_id"`
	ExpectedCheckInAt time.Time `json:"expected_check_in_at"`
	DetectedAt        time.Time `json:"detected_at"`
}

func (c *Consumer) handleDatingSafeMeetMissedCheckIn(ctx context.Context, raw json.RawMessage) error {
	var e datingSafeMeetMissedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	withUser, _ := uuid.Parse(e.WithUserID)
	meetID, _ := uuid.Parse(e.MeetID)
	deepLink := fmt.Sprintf("/dating/safety/meets/%s/check-in", e.MeetID)
	return c.service.CreateNotification(ctx, user, withUser, "dating.safe_meet.missed_check_in", "dating_meet", meetID, deepLink, e.DetectedAt)
}

type datingSafetyPanicAckPayload struct {
	UserID         string    `json:"user_id"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

func (c *Consumer) handleDatingSafetyPanicAcknowledged(ctx context.Context, raw json.RawMessage) error {
	var e datingSafetyPanicAckPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	// Silent confirmation: deep-link to the safety center but no PII
	// about who reviewed the alert.
	deepLink := "/dating/safety/center"
	return c.service.CreateNotification(ctx, user, user, "dating.safety.panic.acknowledged", "dating_safety", user, deepLink, e.AcknowledgedAt)
}

type datingReportStatusUpdatedPayload struct {
	ReportID   string    `json:"report_id"`
	ReporterID string    `json:"reporter_id"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (c *Consumer) handleDatingReportStatusUpdated(ctx context.Context, raw json.RawMessage) error {
	var e datingReportStatusUpdatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	reporter, err := uuid.Parse(e.ReporterID)
	if err != nil {
		return err
	}
	reportID, _ := uuid.Parse(e.ReportID)
	deepLink := fmt.Sprintf("/dating/safety/reports/%s", e.ReportID)
	return c.service.CreateNotification(ctx, reporter, reporter, "dating.report.status_updated", "dating_report", reportID, deepLink, e.UpdatedAt)
}

type datingVerificationCompletedPayload struct {
	UserID      string    `json:"user_id"`
	Kind        string    `json:"kind"`
	TrustTier   string    `json:"trust_tier"`
	CompletedAt time.Time `json:"completed_at"`
}

func (c *Consumer) handleDatingVerificationCompleted(ctx context.Context, raw json.RawMessage) error {
	var e datingVerificationCompletedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	deepLink := "/dating/profile/verification"
	return c.service.CreateNotification(ctx, user, user, "dating.verification.completed", "dating_verification", user, deepLink, e.CompletedAt)
}

type datingVerificationRejectedPayload struct {
	UserID     string    `json:"user_id"`
	Kind       string    `json:"kind"`
	RejectedAt time.Time `json:"rejected_at"`
}

func (c *Consumer) handleDatingVerificationRejected(ctx context.Context, raw json.RawMessage) error {
	var e datingVerificationRejectedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	deepLink := "/dating/profile/verification"
	return c.service.CreateNotification(ctx, user, user, "dating.verification.rejected", "dating_verification", user, deepLink, e.RejectedAt)
}

type datingPhotoRejectedPayload struct {
	UserID     string    `json:"user_id"`
	PhotoID    string    `json:"photo_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	RejectedAt time.Time `json:"rejected_at"`
}

func (c *Consumer) handleDatingPhotoModerationRejected(ctx context.Context, raw json.RawMessage) error {
	var e datingPhotoRejectedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	deepLink := "/dating/profile/photos"
	entity := user
	if e.PhotoID != "" {
		if pid, err := uuid.Parse(e.PhotoID); err == nil {
			entity = pid
		}
	}
	return c.service.CreateNotification(ctx, user, user, "dating.photo.rejected", "dating_photo", entity, deepLink, e.RejectedAt)
}

type datingPremiumPaymentFailurePayload struct {
	UserID     string    `json:"user_id"`
	PlanID     string    `json:"plan_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (c *Consumer) handleDatingPremiumPaymentFailure(ctx context.Context, raw json.RawMessage) error {
	var e datingPremiumPaymentFailurePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	deepLink := "/dating/premium"
	return c.service.CreateNotification(ctx, user, user, "dating.premium.payment_failure", "dating_premium", user, deepLink, e.OccurredAt)
}

type datingDataExportReadyPayload struct {
	ExportID    string    `json:"export_id"`
	UserID      string    `json:"user_id"`
	CompletedAt time.Time `json:"completed_at"`
}

func (c *Consumer) handleDatingDataExportReady(ctx context.Context, raw json.RawMessage) error {
	var e datingDataExportReadyPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return err
	}
	exportID, _ := uuid.Parse(e.ExportID)
	deepLink := fmt.Sprintf("/dating/data-exports/%s", e.ExportID)
	return c.service.CreateNotification(ctx, user, user, "dating.data_export.ready", "dating_data_export", exportID, deepLink, e.CompletedAt)
}

type chatDatingMessageNewPayload struct {
	ConversationID string    `json:"conversation_id"`
	MatchID        string    `json:"match_id"`
	SenderID       string    `json:"sender_id"`
	RecipientID    string    `json:"recipient_id"`
	MessagePreview string    `json:"message_preview,omitempty"`
	SentAt         time.Time `json:"sent_at"`
}

func (c *Consumer) handleChatDatingMessageNew(ctx context.Context, raw json.RawMessage) error {
	var e chatDatingMessageNewPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	recipient, err := uuid.Parse(e.RecipientID)
	if err != nil {
		return err
	}
	sender, _ := uuid.Parse(e.SenderID)
	matchID, _ := uuid.Parse(e.MatchID)
	deepLink := fmt.Sprintf("/dating/matches/%s", e.MatchID)
	return c.service.CreateNotification(ctx, recipient, sender, "dating.match.new_message", "dating_match", matchID, deepLink, e.SentAt)
}
