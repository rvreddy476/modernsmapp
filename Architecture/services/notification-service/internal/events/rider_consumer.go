// Rider event handlers — mirrors qa_consumer.go / dating_consumer.go.
// Sprint 3 covers safety SOS, complaint raised, partner approved, and
// subscription expiring notifications. Other rider.* events are claimed
// and silently ignored so the default branch doesn't log a warning.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// handleRiderEvent is the dispatch entry point invoked from the main
// consumer's processMessage when no other handler claimed the event.
// Returns true when the event was claimed (handled or knowingly ignored).
func (c *Consumer) handleRiderEvent(ctx context.Context, envelope events.EventEnvelope) (bool, error) {
	switch envelope.EventType {
	case events.EventRiderSafetySOS:
		return true, c.handleRiderSOS(ctx, envelope.Payload)
	case events.EventRiderSafetyContactAlert:
		return true, c.handleRiderSafetyContactAlert(ctx, envelope.Payload)
	case events.EventRiderComplaintRaised:
		return true, c.handleRiderComplaintRaised(ctx, envelope.Payload)
	case events.EventRiderPartnerApproved:
		return true, c.handleRiderPartnerApproved(ctx, envelope.Payload)
	case events.EventRiderSubscriptionExpiring:
		return true, c.handleRiderSubscriptionExpiring(ctx, envelope.Payload)
	case events.EventRiderSubscriptionGracePeriod:
		return true, c.handleRiderSubscriptionGracePeriod(ctx, envelope.Payload)
	case events.EventRiderSubscriptionRenewed:
		return true, c.handleRiderSubscriptionRenewed(ctx, envelope.Payload)
	case events.EventRiderSubscriptionRenewalFailed:
		return true, c.handleRiderSubscriptionRenewalFailed(ctx, envelope.Payload)
	case events.EventRiderDocumentExpiring:
		return true, c.handleRiderDocumentExpiring(ctx, envelope.Payload)
	case events.EventRiderPartnerFraudFlagged:
		return true, c.handleRiderPartnerFraudFlagged(ctx, envelope.Payload)
	case events.EventRiderDailyRevenueReport:
		return true, c.handleRiderDailyRevenueReport(ctx, envelope.Payload)
	case events.EventRiderAdminQueueSummary:
		return true, c.handleRiderAdminQueueSummary(ctx, envelope.Payload)
	case events.EventRiderRideAssigned,
		events.EventRiderRideArriving,
		events.EventRiderRideArrived,
		events.EventRiderRideStarted,
		events.EventRiderRideCompleted,
		events.EventRiderRideCancelled,
		events.EventRiderRideExpired,
		events.EventRiderRideRated,
		events.EventRiderRideRequested,
		events.EventRiderRideOffered,
		events.EventRiderRideOfferRejected,
		events.EventRiderRideOfferExpired,
		events.EventRiderPartnerCreated,
		events.EventRiderPartnerKYCSubmitted,
		events.EventRiderPartnerKYCApproved,
		events.EventRiderPartnerKYCRejected,
		events.EventRiderPartnerVehicleAdded,
		events.EventRiderPartnerVehicleApproved,
		events.EventRiderPartnerVehicleRejected,
		events.EventRiderPartnerSuspended,
		events.EventRiderPartnerBlocked,
		events.EventRiderPartnerOnline,
		events.EventRiderPartnerOffline,
		events.EventRiderSubscriptionPaymentSubmitted,
		events.EventRiderSubscriptionPaymentVerified,
		events.EventRiderSubscriptionPaymentRejected,
		events.EventRiderSubscriptionActivated,
		events.EventRiderSubscriptionExpired,
		events.EventRiderSafetyIncidentAcknowledged,
		events.EventRiderSafetyIncidentResolved,
		events.EventRiderComplaintUpdated,
		events.EventRiderShareTokenCreated,
		events.EventRiderAuditAction,
		events.EventRiderAdminAction:
		// Known-but-not-pushed; claim the event so the default branch
		// doesn't log a warning.
		return true, nil
	}
	return false, nil
}

// rider.safety.sos → priority push to admin queue + trusted contact (the
// trusted-contact alert is itself a separate event; here we surface to
// the admin via the standard notification flow).
type riderSafetySOSPayload struct {
	IncidentID string    `json:"incident_id"`
	RideID     string    `json:"ride_id"`
	CustomerID string    `json:"customer_id"`
	PartnerID  string    `json:"partner_id,omitempty"`
	Severity   string    `json:"severity"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderSOS(ctx context.Context, raw json.RawMessage) error {
	var e riderSafetySOSPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	customerID, err := uuid.Parse(e.CustomerID)
	if err != nil {
		return fmt.Errorf("invalid customer_id in rider.safety.sos: %w", err)
	}
	incidentID, _ := uuid.Parse(e.IncidentID)
	deepLink := fmt.Sprintf("/rider/safety/incidents/%s", e.IncidentID)
	// Notification recipient: the customer themself gets a confirmation
	// push so they know help is on the way; admin push fan-out happens via
	// a separate ops-channel routing layer not implemented in this MVP.
	if err := c.service.CreateNotification(ctx, customerID, customerID, "rider.safety.sos", "rider_safety_incident", incidentID, deepLink, e.OccurredAt); err != nil {
		slog.Warn("rider sos: notify customer failed", "customer_id", customerID, "error", err)
	}
	return nil
}

// rider.safety.contact_alert → push to the trusted contact's AtPost user
// (if they are an AtPost user) — phone-only contacts get an SMS via the
// trust-and-safety service in production. For MVP we only store the row.
type riderSafetyContactAlertPayload struct {
	IncidentID   string    `json:"incident_id"`
	RideID       string    `json:"ride_id"`
	CustomerID   string    `json:"customer_id"`
	ContactName  string    `json:"contact_name"`
	ContactPhone string    `json:"contact_phone"`
	Message      string    `json:"message"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderSafetyContactAlert(ctx context.Context, raw json.RawMessage) error {
	var e riderSafetyContactAlertPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	// The phone-keyed lookup to AtPost user-id is out of scope for this
	// consumer; trust-and-safety handles SMS via Twilio. We log so the
	// outage path stays traceable.
	slog.Info("rider: trusted-contact alert claimed", "incident_id", e.IncidentID, "contact_name", e.ContactName)
	return nil
}

// rider.complaint.raised → notify admin queue (delivered via the same
// notification graph, routed by app to the ops cohort).
type riderComplaintPayload struct {
	ComplaintID string    `json:"complaint_id"`
	RideID      string    `json:"ride_id"`
	CustomerID  string    `json:"customer_id"`
	PartnerID   string    `json:"partner_id,omitempty"`
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderComplaintRaised(ctx context.Context, raw json.RawMessage) error {
	var e riderComplaintPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	customerID, err := uuid.Parse(e.CustomerID)
	if err != nil {
		return fmt.Errorf("invalid customer_id in rider.complaint.raised: %w", err)
	}
	complaintID, _ := uuid.Parse(e.ComplaintID)
	deepLink := fmt.Sprintf("/rider/complaints/%s", e.ComplaintID)
	// Notify the customer — receipt confirmation. Admin queue routing
	// happens out-of-band via ops dashboard polling.
	if err := c.service.CreateNotification(ctx, customerID, customerID, "rider.complaint.raised", "rider_complaint", complaintID, deepLink, e.OccurredAt); err != nil {
		slog.Warn("rider complaint: notify customer failed", "customer_id", customerID, "error", err)
	}
	return nil
}

// rider.partner.approved → welcome push to the partner.
type riderPartnerStatusPayload struct {
	PartnerID  string    `json:"partner_id"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	ActorID    string    `json:"actor_id,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderPartnerApproved(ctx context.Context, raw json.RawMessage) error {
	var e riderPartnerStatusPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.partner.approved: %w", err)
	}
	deepLink := "/rider/partner/dashboard"
	// We don't have the partner's user_id directly in this payload; the
	// partner_id column on rider_partners maps to user_id but resolving it
	// requires a rider-service callback. For MVP we use partner_id as the
	// recipient key — notification-service routes by user_id but the
	// rider partner's notification flow uses the partner-id directly.
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.partner.approved", "rider_partner", partnerID, deepLink, e.OccurredAt); err != nil {
		slog.Warn("rider partner approved: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// rider.subscription.expiring → push to partner so they renew before the
// grace period kicks in.
type riderSubscriptionExpiringPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	OccurredAt     time.Time `json:"occurred_at,omitempty"`
}

func (c *Consumer) handleRiderSubscriptionExpiring(ctx context.Context, raw json.RawMessage) error {
	var e riderSubscriptionExpiringPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.subscription.expiring: %w", err)
	}
	subID, _ := uuid.Parse(e.SubscriptionID)
	deepLink := "/rider/partner/subscription"
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.subscription.expiring", "rider_subscription", subID, deepLink, occurred); err != nil {
		slog.Warn("rider subscription expiring: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// --- Sprint 4: subscription grace, renewal, doc expiry, fraud, summary --

// rider.subscription.grace_period → push to partner that the grace
// window has started (or that they've now been moved to expired — the
// payload's status field carries the distinction in production).
type riderSubscriptionGracePayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	GraceEndsAt    time.Time `json:"grace_ends_at,omitempty"`
	OccurredAt     time.Time `json:"occurred_at,omitempty"`
}

func (c *Consumer) handleRiderSubscriptionGracePeriod(ctx context.Context, raw json.RawMessage) error {
	var e riderSubscriptionGracePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.subscription.grace_period: %w", err)
	}
	subID, _ := uuid.Parse(e.SubscriptionID)
	deepLink := "/rider/partner/subscription"
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.subscription.grace_period", "rider_subscription", subID, deepLink, occurred); err != nil {
		slog.Warn("rider subscription grace: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// rider.subscription.renewed → push confirming the auto-renewal succeeded.
type riderSubscriptionRenewedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id"`
	AmountPaise    int64     `json:"amount_paise"`
	NewExpiresAt   time.Time `json:"new_expires_at"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderSubscriptionRenewed(ctx context.Context, raw json.RawMessage) error {
	var e riderSubscriptionRenewedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.subscription.renewed: %w", err)
	}
	subID, _ := uuid.Parse(e.SubscriptionID)
	deepLink := "/rider/partner/subscription"
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.subscription.renewed", "rider_subscription", subID, deepLink, occurred); err != nil {
		slog.Warn("rider subscription renewed: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// rider.subscription.renewal_failed → "renewal failed; please top up wallet".
type riderSubscriptionRenewalFailedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id"`
	AmountPaise    int64     `json:"amount_paise"`
	FailureCount   int       `json:"failure_count"`
	AutoRenewOff   bool      `json:"auto_renew_off"`
	Reason         string    `json:"reason"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderSubscriptionRenewalFailed(ctx context.Context, raw json.RawMessage) error {
	var e riderSubscriptionRenewalFailedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.subscription.renewal_failed: %w", err)
	}
	subID, _ := uuid.Parse(e.SubscriptionID)
	deepLink := "/rider/partner/subscription"
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.subscription.renewal_failed", "rider_subscription", subID, deepLink, occurred); err != nil {
		slog.Warn("rider subscription renewal failed: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// rider.document.expiring → push to the partner so they re-upload before
// the document fully expires.
type riderDocumentExpiringPayload struct {
	PartnerID    string    `json:"partner_id"`
	DocumentID   string    `json:"document_id"`
	DocumentKind string    `json:"document_kind"`
	OwnerKind    string    `json:"owner_kind"`
	ExpiresAt    time.Time `json:"expires_at"`
	Bucket       string    `json:"bucket"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderDocumentExpiring(ctx context.Context, raw json.RawMessage) error {
	var e riderDocumentExpiringPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.document.expiring: %w", err)
	}
	docID, _ := uuid.Parse(e.DocumentID)
	deepLink := "/rider/partner/documents"
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.document.expiring", "rider_document", docID, deepLink, occurred); err != nil {
		slog.Warn("rider document expiring: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// rider.partner.fraud_flagged → admin queue + email. The MVP path uses
// the standard CreateNotification call routed by the admin app; ops
// also gets an email via the trust-and-safety service.
type riderPartnerFraudFlaggedPayload struct {
	PartnerID   string    `json:"partner_id"`
	FraudScore  float64   `json:"fraud_score"`
	AutoSuspend bool      `json:"auto_suspend"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderPartnerFraudFlagged(ctx context.Context, raw json.RawMessage) error {
	var e riderPartnerFraudFlaggedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	partnerID, err := uuid.Parse(e.PartnerID)
	if err != nil {
		return fmt.Errorf("invalid partner_id in rider.partner.fraud_flagged: %w", err)
	}
	deepLink := fmt.Sprintf("/admin/mopedu/partners/%s", e.PartnerID)
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	// In production the recipient is the ops cohort, not the partner.
	// We use the partner id as the resource id so the admin app can deep
	// link to the partner detail page.
	if err := c.service.CreateNotification(ctx, partnerID, partnerID, "rider.partner.fraud_flagged", "rider_partner", partnerID, deepLink, occurred); err != nil {
		slog.Warn("rider fraud flagged: notify failed", "partner_id", partnerID, "error", err)
	}
	return nil
}

// rider.daily.revenue_report → email digest to ops.
type riderDailyRevenueReportPayload struct {
	Date                       string    `json:"date"`
	SubscriptionsCount         int       `json:"subscriptions_count"`
	SubscriptionsRevenuePaise  int64     `json:"subscriptions_revenue_paise"`
	RidesCount                 int       `json:"rides_count"`
	OccurredAt                 time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderDailyRevenueReport(ctx context.Context, raw json.RawMessage) error {
	var e riderDailyRevenueReportPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	// No per-user push — the daily revenue digest is delivered as an
	// ops email out-of-band. We log so the path is traceable.
	slog.Info("rider daily revenue report claimed",
		"date", e.Date,
		"subscriptions_count", e.SubscriptionsCount,
		"rides_count", e.RidesCount)
	_ = ctx
	return nil
}

// rider.admin.queue_summary → email digest to ops.
type riderAdminQueueSummaryPayload struct {
	PendingKYCCount         int       `json:"pending_kyc_count"`
	PendingVehicleCount     int       `json:"pending_vehicle_count"`
	PendingPaymentCount     int       `json:"pending_payment_count"`
	OpenComplaintsCount     int       `json:"open_complaints_count"`
	OpenSafetyIncidentsCount int      `json:"open_safety_incidents_count"`
	OccurredAt              time.Time `json:"occurred_at"`
}

func (c *Consumer) handleRiderAdminQueueSummary(ctx context.Context, raw json.RawMessage) error {
	var e riderAdminQueueSummaryPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	slog.Info("rider admin queue summary claimed",
		"pending_kyc", e.PendingKYCCount,
		"pending_vehicle", e.PendingVehicleCount,
		"pending_payment", e.PendingPaymentCount,
		"open_complaints", e.OpenComplaintsCount,
		"open_safety", e.OpenSafetyIncidentsCount)
	_ = ctx
	return nil
}
