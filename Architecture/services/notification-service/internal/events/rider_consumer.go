// Rider event handlers — mirrors qa_consumer.go / dating_consumer.go.
// Sprint 3 covers safety SOS and complaint raised through the generic
// notification path. Other rider.* events are claimed and silently ignored
// so the default branch doesn't log a warning.
//
// Mopedu pushes (2026-09-18): the customer's ride lifecycle and payment go
// to their Momentum install as the app's `ride.*` types; the captain's offer
// and payment notice go to their Mopedu Captain install (service.RidePush,
// ride_push.go). Recipients come from payload fields only — this service
// never reads rider-service's database — and an event without its recipient
// is counted and logged with the exact field rider-service must add.
//
// Captain account pushes (2026-09-19): partner approval / review and the
// subscription lifecycle also go to the Mopedu Captain install as the app's
// `captain.*` account types on the captain_account channel, with an inbox row
// (rider_partner / rider_subscription). They used to go through the generic
// CreateNotification path, which is not app-aware, so a captain saw them on
// Momentum, if at all.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ridePushDeliverer is the service seam; *service.Service satisfies it.
type ridePushDeliverer interface {
	DeliverRidePush(ctx context.Context, p service.RidePush) (service.RidePushOutcome, error)
}

var ridePushesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "atpost",
	Subsystem: "notification_service",
	Name:      "ride_pushes_total",
	Help:      "Mopedu pushes by type, app and outcome (missing_recipient, expired and delivery_error included).",
}, []string{"type", "app", "outcome"})

// Offer TTL bounds: an offer expires in ~20 s; a clock skew or a malformed
// expires_at must not turn that into zero or into FCM's four-week default.
const (
	rideOfferDefaultTTL = 20 * time.Second
	rideOfferMinTTL     = 5 * time.Second
	rideOfferMaxTTL     = 60 * time.Second
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
	case events.EventRiderPartnerApproved,
		eventRiderPartnerUnderReview,
		events.EventRiderSubscriptionExpiring,
		events.EventRiderSubscriptionGracePeriod,
		events.EventRiderSubscriptionExpired,
		events.EventRiderSubscriptionRenewed,
		events.EventRiderSubscriptionRenewalFailed:
		// captain.approved / under_review / subscription.* → the partner's
		// Mopedu Captain devices on the captain_account channel.
		return true, c.handleRiderCaptainAccount(ctx, envelope.EventType, envelope.EventID, envelope.Payload)
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
		events.EventRiderRideCancelled:
		// Customer-facing ride lifecycle → the Momentum app's ride.* push.
		// The payload carries customer_user_id (enriched 2026-05-22 for
		// assigned/arriving/arrived/started) so no round-trip to
		// rider-service is needed.
		return true, c.handleRiderRideLifecycle(ctx, envelope.EventType, envelope.EventID, envelope.Payload)
	case events.EventRiderRideOffered:
		// captain.offer → the partner's Mopedu Captain devices, HIGH, short TTL.
		return true, c.handleRiderRideOffered(ctx, envelope.EventID, envelope.Payload)
	case events.EventRiderRidePaymentPaid:
		// ride.payment.paid → customer; "Payment received ₹x" → captain.
		return true, c.handleRiderRidePaymentPaid(ctx, envelope.EventID, envelope.Payload)
	case events.EventRiderRideExpired,
		events.EventRiderRideRated,
		events.EventRiderRideRequested,
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

// --- Mopedu ride pushes (2026-09-18) ----------------------------------------

// riderRideLifecyclePayload covers ride.assigned / arriving / arrived /
// started / completed / cancelled. Only the fields a push may use are
// decoded: never the fare breakdown, never a location, never an OTP.
// customer_user_id was added to assigned/arriving/arrived/started on
// 2026-05-22; completed and cancelled need it too (see the missing-recipient
// log).
type riderRideLifecyclePayload struct {
	RideID         string `json:"ride_id"`
	CustomerUserID string `json:"customer_user_id"`
	PartnerID      string `json:"partner_id"`
	// Cancelled only: customer | partner | admin | system.
	CancelledByKind string `json:"cancelled_by_kind"`

	OccurredAt  time.Time `json:"occurred_at"`
	AssignedAt  time.Time `json:"assigned_at"`
	CompletedAt time.Time `json:"completed_at"`
	CancelledAt time.Time `json:"cancelled_at"`
}

func (p riderRideLifecyclePayload) occurred(now time.Time) time.Time {
	for _, t := range []time.Time{p.OccurredAt, p.AssignedAt, p.CompletedAt, p.CancelledAt} {
		if !t.IsZero() {
			return t.UTC()
		}
	}
	return now
}

type rideCopy struct{ title, body string }

// rideCustomerPushes maps each lifecycle event to the Momentum app's push
// type (PushDestinations.RIDE_TYPES) and its copy. No OTP, no fare, no
// captain name or number: the ride screen shows those once opened.
var rideCustomerPushes = map[string]struct {
	pushType string
	text     rideCopy
}{
	events.EventRiderRideAssigned:  {service.RideTypeAssigned, rideCopy{"Captain assigned", "Your captain is on the way."}},
	events.EventRiderRideArriving:  {service.RideTypeArriving, rideCopy{"Captain arriving", "Your captain is almost at your pickup point."}},
	events.EventRiderRideArrived:   {service.RideTypeArrived, rideCopy{"Captain has arrived", "Share your OTP from the app to start the ride."}},
	events.EventRiderRideStarted:   {service.RideTypeStarted, rideCopy{"Ride started", "Enjoy your ride. You can share your trip from the app."}},
	events.EventRiderRideCompleted: {service.RideTypeCompleted, rideCopy{"Ride completed", "You have reached your destination. Tap to see your receipt."}},
	events.EventRiderRideCancelled: {service.RideTypeCancelled, rideCopy{"Ride cancelled", "Your ride was cancelled. Tap for details."}},
}

// rideCancelledCopy refines the cancelled push by who cancelled. A customer
// who cancelled their own ride is not told about it (nil).
func rideCancelledCopy(byKind string) *rideCopy {
	switch strings.ToLower(strings.TrimSpace(byKind)) {
	case "customer":
		return nil
	case "partner", "captain":
		return &rideCopy{"Ride cancelled", "Your captain cancelled the ride. Tap to book again."}
	default:
		c := rideCustomerPushes[events.EventRiderRideCancelled].text
		return &c
	}
}

// rideCustomerDeepLink is where a customer ride push lands. The Momentum app
// opens its ride screen for every ride.* type and asks the server for the
// ride, so the link is informational; keep it stable for the inbox row.
func rideCustomerDeepLink(rideID uuid.UUID) string { return "/mopedu/booking/" + rideID.String() }

// rideDedupKey keys a push on the envelope's event id, or on the ride and
// event type when a producer sent none.
func rideDedupKey(eventID, eventType string, rideID uuid.UUID) string {
	if eventID != "" {
		return "event:" + eventID + ":" + eventType
	}
	return "ride:" + rideID.String() + ":" + eventType
}

// planRideLifecyclePush renders the customer's push for one lifecycle
// event. ok=false with an empty missing means "deliberately no push";
// missing names the payload field rider-service must add.
func planRideLifecyclePush(eventType, eventID string, raw json.RawMessage, now time.Time) (p service.RidePush, missing string, ok bool, err error) {
	var e riderRideLifecyclePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return service.RidePush{}, "", false, fmt.Errorf("rider lifecycle: decode %s: %w", eventType, err)
	}
	spec, known := rideCustomerPushes[eventType]
	if !known {
		return service.RidePush{}, "", false, fmt.Errorf("rider lifecycle: %s is not a customer lifecycle event", eventType)
	}
	rideID, rerr := uuid.Parse(strings.TrimSpace(e.RideID))
	if rerr != nil || rideID == uuid.Nil {
		return service.RidePush{}, "", false, fmt.Errorf("rider lifecycle: invalid ride_id in %s: %w", eventType, rerr)
	}
	customer, cok := parseRecipient(e.CustomerUserID)
	if !cok {
		return service.RidePush{}, "customer_user_id", false, nil
	}
	text := spec.text
	if eventType == events.EventRiderRideCancelled {
		refined := rideCancelledCopy(e.CancelledByKind)
		if refined == nil {
			return service.RidePush{}, "", false, nil
		}
		text = *refined
	}
	return service.RidePush{
		DedupKey:       rideDedupKey(eventID, eventType, rideID),
		RecipientID:    customer,
		App:            service.AppMomentum,
		Type:           spec.pushType,
		AndroidChannel: service.RideChannelUpdates,
		Title:          text.title,
		Body:           text.body,
		DeepLink:       rideCustomerDeepLink(rideID),
		EntityID:       rideID,
		RideID:         rideID,
		CreatedAt:      e.occurred(now),
	}, "", true, nil
}

// handleRiderRideLifecycle pushes the customer's ride-status notification.
func (c *Consumer) handleRiderRideLifecycle(ctx context.Context, eventType, eventID string, raw json.RawMessage) error {
	p, missing, ok, err := planRideLifecyclePush(eventType, eventID, raw, time.Now().UTC())
	if err != nil {
		return err
	}
	if missing != "" {
		ridePushesTotal.WithLabelValues(eventType, service.AppMomentum, "missing_recipient").Inc()
		slog.Warn("rider lifecycle: event lacks its push recipient; rider-service must add the field",
			"event", eventType, "field", missing)
		return nil
	}
	if !ok {
		return nil
	}
	c.deliverRidePush(ctx, eventType, p)
	return nil
}

// riderRideOfferedPayload mirrors rider-service RideOfferedPayload. The
// captain push's recipient is the partner's USER id (what the Captain app
// registered its device under) — partner_user_id when the producer carries
// it, else partner_id, which reaches a device only where the two coincide.
type riderRideOfferedPayload struct {
	RideID        string    `json:"ride_id"`
	OfferID       string    `json:"offer_id"`
	PartnerID     string    `json:"partner_id"`
	PartnerUserID string    `json:"partner_user_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	OfferedAt     time.Time `json:"offered_at"`
}

// captainRecipient picks the captain push's user id; the bool says whether
// it came from partner_user_id (false: the partner_id fallback was used).
func captainRecipient(partnerUserID, partnerID string) (uuid.UUID, bool, bool) {
	if id, ok := parseRecipient(partnerUserID); ok {
		return id, true, true
	}
	if id, ok := parseRecipient(partnerID); ok {
		return id, false, true
	}
	return uuid.Nil, false, false
}

// rideOfferTTL is how long FCM may hold the offer: until it expires, within
// [rideOfferMinTTL, rideOfferMaxTTL]; the default when expires_at is unset.
// expired=true when the offer had already lapsed by the time it was read.
func rideOfferTTL(expiresAt, now time.Time) (ttl time.Duration, expired bool) {
	if expiresAt.IsZero() {
		return rideOfferDefaultTTL, false
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0, true
	}
	if remaining < rideOfferMinTTL {
		return rideOfferMinTTL, false
	}
	if remaining > rideOfferMaxTTL {
		return rideOfferMaxTTL, false
	}
	return remaining.Round(time.Second), false
}

// planCaptainOfferPush renders captain.offer for one ride.offered event.
func planCaptainOfferPush(raw json.RawMessage, now time.Time) (p service.RidePush, fallback bool, missing string, expired bool, err error) {
	var e riderRideOfferedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return service.RidePush{}, false, "", false, fmt.Errorf("rider offer: decode: %w", err)
	}
	rideID, rerr := uuid.Parse(strings.TrimSpace(e.RideID))
	if rerr != nil || rideID == uuid.Nil {
		return service.RidePush{}, false, "", false, fmt.Errorf("rider offer: invalid ride_id: %w", rerr)
	}
	offerID, oerr := uuid.Parse(strings.TrimSpace(e.OfferID))
	if oerr != nil || offerID == uuid.Nil {
		return service.RidePush{}, false, "", false, fmt.Errorf("rider offer: invalid offer_id: %w", oerr)
	}
	captain, fromUserID, ok := captainRecipient(e.PartnerUserID, e.PartnerID)
	if !ok {
		return service.RidePush{}, false, "partner_user_id", false, nil
	}
	ttl, expired := rideOfferTTL(e.ExpiresAt, now)
	if expired {
		return service.RidePush{}, !fromUserID, "", true, nil
	}
	created := e.OfferedAt
	if created.IsZero() {
		created = now
	}
	return service.RidePush{
		DedupKey:       "ride_offer:" + offerID.String(),
		RecipientID:    captain,
		App:            service.AppMopeduCaptain,
		Type:           service.CaptainTypeOffer,
		AndroidChannel: service.CaptainChannelOffer,
		Title:          "New ride offer",
		Body:           "A ride is waiting. Accept before the offer expires.",
		DeepLink:       "/captain/offers/" + offerID.String(),
		EntityID:       offerID,
		RideID:         rideID,
		TTL:            ttl,
		CreatedAt:      created.UTC(),
	}, !fromUserID, "", false, nil
}

func (c *Consumer) handleRiderRideOffered(ctx context.Context, _ string, raw json.RawMessage) error {
	p, fallback, missing, expired, err := planCaptainOfferPush(raw, time.Now().UTC())
	if err != nil {
		return err
	}
	if missing != "" {
		ridePushesTotal.WithLabelValues(service.CaptainTypeOffer, service.AppMopeduCaptain, "missing_recipient").Inc()
		slog.Warn("rider offer: event lacks its push recipient; rider-service must add the field",
			"event", events.EventRiderRideOffered, "field", missing)
		return nil
	}
	if expired {
		ridePushesTotal.WithLabelValues(service.CaptainTypeOffer, service.AppMopeduCaptain, "expired").Inc()
		return nil
	}
	if fallback {
		slog.Warn("rider offer: no partner_user_id on the event; pushing to partner_id, which reaches a device only if it is the captain's user id",
			"event", events.EventRiderRideOffered)
	}
	c.deliverRidePush(ctx, events.EventRiderRideOffered, p)
	return nil
}

// rupees renders paise as "₹1,234.50" / "₹120" for push copy.
func rupees(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	whole, frac := paise/100, paise%100
	digits := fmt.Sprint(whole)
	// Indian grouping: the last three, then twos.
	if len(digits) > 3 {
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var groups []string
		for len(head) > 2 {
			groups = append([]string{head[len(head)-2:]}, groups...)
			head = head[:len(head)-2]
		}
		groups = append([]string{head}, groups...)
		digits = strings.Join(groups, ",") + "," + tail
	}
	out := "₹" + digits
	if frac != 0 {
		out += fmt.Sprintf(".%02d", frac)
	}
	if neg {
		out = "-" + out
	}
	return out
}

func paymentMethodLabel(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "upi":
		return "by UPI"
	case "card":
		return "by card"
	case "cash":
		return "in cash"
	case "wallet":
		return "from your wallet"
	case "":
		return ""
	default:
		return "by " + strings.ToLower(strings.TrimSpace(method))
	}
}

// planPaymentPaidPushes renders the customer's ride.payment.paid and the
// captain's payment notice. Either recipient can be missing independently.
func planPaymentPaidPushes(eventID string, raw json.RawMessage, now time.Time) (pushes []service.RidePush, fallback bool, missing []string, err error) {
	var e events.RiderRidePaymentPaidPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, false, nil, fmt.Errorf("rider payment: decode: %w", err)
	}
	rideID, rerr := uuid.Parse(strings.TrimSpace(e.RideID))
	if rerr != nil || rideID == uuid.Nil {
		return nil, false, nil, fmt.Errorf("rider payment: invalid ride_id: %w", rerr)
	}
	created := e.PaidAt
	if created.IsZero() {
		created = now
	}
	created = created.UTC()
	amount := rupees(e.AmountPaise)

	if customer, ok := parseRecipient(e.CustomerUserID); ok {
		body := amount + " paid for your ride. Tap to see your receipt."
		if label := paymentMethodLabel(e.Method); label != "" {
			body = amount + " paid " + label + " for your ride. Tap to see your receipt."
		}
		pushes = append(pushes, service.RidePush{
			DedupKey:       rideDedupKey(eventID, events.EventRiderRidePaymentPaid, rideID),
			RecipientID:    customer,
			App:            service.AppMomentum,
			Type:           service.RideTypePaymentPaid,
			AndroidChannel: service.RideChannelUpdates,
			Title:          "Payment received",
			Body:           body,
			DeepLink:       rideCustomerDeepLink(rideID),
			EntityID:       rideID,
			RideID:         rideID,
			CreatedAt:      created,
		})
	} else {
		missing = append(missing, "customer_user_id")
	}

	captain, fromUserID, ok := captainRecipient(e.PartnerUserID, e.PartnerID)
	if !ok {
		missing = append(missing, "partner_user_id")
	} else {
		fallback = !fromUserID
		pushes = append(pushes, service.RidePush{
			DedupKey:       rideDedupKey(eventID, events.EventRiderRidePaymentPaid, rideID),
			RecipientID:    captain,
			App:            service.AppMopeduCaptain,
			Type:           service.CaptainTypePaymentReceived,
			AndroidChannel: service.CaptainChannelEarnings,
			Title:          "Payment received " + amount,
			Body:           amount + " received for your last ride.",
			DeepLink:       "/captain/rides/" + rideID.String(),
			EntityID:       rideID,
			RideID:         rideID,
			CreatedAt:      created,
		})
	}
	return pushes, fallback, missing, nil
}

func (c *Consumer) handleRiderRidePaymentPaid(ctx context.Context, eventID string, raw json.RawMessage) error {
	pushes, fallback, missing, err := planPaymentPaidPushes(eventID, raw, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, field := range missing {
		app := service.AppMomentum
		if field == "partner_user_id" {
			app = service.AppMopeduCaptain
		}
		ridePushesTotal.WithLabelValues(events.EventRiderRidePaymentPaid, app, "missing_recipient").Inc()
		slog.Warn("rider payment: event lacks a push recipient; rider-service must add the field",
			"event", events.EventRiderRidePaymentPaid, "field", field)
	}
	if fallback {
		slog.Warn("rider payment: no partner_user_id on the event; pushing to partner_id, which reaches a device only if it is the captain's user id",
			"event", events.EventRiderRidePaymentPaid)
	}
	for _, p := range pushes {
		c.deliverRidePush(ctx, events.EventRiderRidePaymentPaid, p)
	}
	return nil
}

// deliverRidePush hands one push to the service and counts the outcome. A
// delivery failure is logged, never returned: the event is claimed either
// way, because retrying "your captain has arrived" minutes later helps
// nobody and would stall the partition.
func (c *Consumer) deliverRidePush(ctx context.Context, eventType string, p service.RidePush) {
	if c.ridePush == nil {
		ridePushesTotal.WithLabelValues(p.Type, p.App, "not_wired").Inc()
		slog.Error("ride push not wired: NOTHING IS SENT", "event", eventType, "type", p.Type, "app", p.App)
		return
	}
	outcome, err := c.ridePush.DeliverRidePush(ctx, p)
	if err != nil {
		ridePushesTotal.WithLabelValues(p.Type, p.App, "delivery_error").Inc()
		slog.Warn("ride push: delivery failed", "event", eventType, "type", p.Type, "app", p.App, "error", err)
		return
	}
	ridePushesTotal.WithLabelValues(p.Type, p.App, string(outcome)).Inc()
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

// --- Captain account pushes (2026-09-19) -----------------------------------

// eventRiderPartnerUnderReview is matched by its string: shared/events has no
// constant for it yet (payload {partner_id, partner_user_id, pending
// []string}). When EventRiderPartnerUnderReview lands there, switch to it.
const eventRiderPartnerUnderReview = "rider.partner.under_review"

// riderCaptainAccountPayload is the union of rider-service's partner-status
// and subscription payloads, decoded to the fields the copy may use: never
// the admin's reason, never a wallet transaction id. The recipient is the
// partner's USER id (partner_user_id), with the partner_id fallback the offer
// path uses — rider_partners.id is not a user id.
type riderCaptainAccountPayload struct {
	PartnerID      string    `json:"partner_id"`
	PartnerUserID  string    `json:"partner_user_id"`
	Pending        []string  `json:"pending"`
	SubscriptionID string    `json:"subscription_id"`
	AmountPaise    int64     `json:"amount_paise"`
	AutoRenewOff   bool      `json:"auto_renew_off"`
	ExpiresAt      time.Time `json:"expires_at"`
	GraceEndsAt    time.Time `json:"grace_ends_at"`
	NewExpiresAt   time.Time `json:"new_expires_at"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// captainAccountTypes maps each account event to the captain app's push
// type. grace_period and expired are both "expired" to the captain — the
// plan has lapsed either way; the grace copy names the renew-by date.
var captainAccountTypes = map[string]string{
	events.EventRiderPartnerApproved:           service.CaptainTypeApproved,
	eventRiderPartnerUnderReview:               service.CaptainTypeUnderReview,
	events.EventRiderSubscriptionExpiring:      service.CaptainTypeSubscriptionExpiring,
	events.EventRiderSubscriptionGracePeriod:   service.CaptainTypeSubscriptionExpired,
	events.EventRiderSubscriptionExpired:       service.CaptainTypeSubscriptionExpired,
	events.EventRiderSubscriptionRenewed:       service.CaptainTypeSubscriptionRenewed,
	events.EventRiderSubscriptionRenewalFailed: service.CaptainTypeSubscriptionPaymentFailed,
}

// istZone renders plan dates the way the captain reads them.
var istZone = time.FixedZone("IST", 5*60*60+30*60)

func captainDate(t time.Time) string { return t.In(istZone).Format("2 Jan") }

// cleanPending keeps the non-empty pending items, trimmed, in order.
func cleanPending(items []string) []string {
	var out []string
	for _, item := range items {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// captainAccountCopy is the push text: plain, every action "in the app", no
// amount beyond "₹x due", never a link to pay anywhere else.
func captainAccountCopy(eventType string, e riderCaptainAccountPayload) rideCopy {
	switch eventType {
	case events.EventRiderPartnerApproved:
		return rideCopy{"You're approved", "Your captain account is approved. Go online in the app to start receiving ride offers."}
	case eventRiderPartnerUnderReview:
		body := "We're reviewing your documents and will let you know when it's done."
		if pending := cleanPending(e.Pending); len(pending) > 0 {
			body = "We're reviewing your documents. Still needed: " + strings.Join(pending, ", ") + ". Complete them in the app."
		}
		return rideCopy{"Account under review", body}
	case events.EventRiderSubscriptionExpiring:
		body := "Your captain plan expires soon. Renew in the app to keep getting ride offers."
		if !e.ExpiresAt.IsZero() {
			body = "Your captain plan expires on " + captainDate(e.ExpiresAt) + ". Renew in the app to keep getting ride offers."
		}
		return rideCopy{"Plan expiring soon", body}
	case events.EventRiderSubscriptionGracePeriod:
		body := "Your captain plan has expired. Renew in the app to keep getting ride offers."
		if !e.GraceEndsAt.IsZero() {
			body = "Your captain plan has expired. Renew in the app by " + captainDate(e.GraceEndsAt) + " to keep getting ride offers."
		}
		return rideCopy{"Plan expired", body}
	case events.EventRiderSubscriptionExpired:
		return rideCopy{"Plan expired", "Your captain plan has expired. Renew in the app to get ride offers again."}
	case events.EventRiderSubscriptionRenewed:
		body := "Your captain plan is renewed."
		if !e.NewExpiresAt.IsZero() {
			body = "Your captain plan is renewed until " + captainDate(e.NewExpiresAt) + "."
		}
		return rideCopy{"Plan renewed", body}
	case events.EventRiderSubscriptionRenewalFailed:
		body := "We couldn't renew your captain plan. Pay in the app to keep getting ride offers."
		if e.AmountPaise > 0 {
			body = "We couldn't renew your captain plan. " + rupees(e.AmountPaise) + " due. Pay in the app to keep getting ride offers."
		}
		if e.AutoRenewOff {
			body += " Auto-renew is now off."
		}
		return rideCopy{"Plan payment failed", body}
	}
	return rideCopy{}
}

// planCaptainAccountPush renders one captain account push. missing names the
// payload field rider-service must add; fallback says partner_id stood in
// for partner_user_id. The entity is the partner (approval / review) or the
// subscription (plan state); there is no ride.
func planCaptainAccountPush(eventType, eventID string, raw json.RawMessage, now time.Time) (p service.RidePush, fallback bool, missing string, err error) {
	var e riderCaptainAccountPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return service.RidePush{}, false, "", fmt.Errorf("captain account: decode %s: %w", eventType, err)
	}
	pushType, known := captainAccountTypes[eventType]
	if !known {
		return service.RidePush{}, false, "", fmt.Errorf("captain account: %s is not a captain account event", eventType)
	}
	entityType, _ := service.CaptainAccountInboxEntityFor(pushType)
	var (
		entityID uuid.UUID
		deepLink string
	)
	switch entityType {
	case service.CaptainPartnerInboxEntityType:
		id, perr := uuid.Parse(strings.TrimSpace(e.PartnerID))
		if perr != nil || id == uuid.Nil {
			return service.RidePush{}, false, "", fmt.Errorf("captain account: invalid partner_id in %s: %w", eventType, perr)
		}
		entityID, deepLink = id, "/captain/account"
	default:
		id, serr := uuid.Parse(strings.TrimSpace(e.SubscriptionID))
		if serr != nil || id == uuid.Nil {
			return service.RidePush{}, false, "", fmt.Errorf("captain account: invalid subscription_id in %s: %w", eventType, serr)
		}
		entityID, deepLink = id, "/captain/subscription"
	}
	captain, fromUserID, ok := captainRecipient(e.PartnerUserID, e.PartnerID)
	if !ok {
		return service.RidePush{}, false, "partner_user_id", nil
	}
	created := e.OccurredAt
	if created.IsZero() {
		created = now
	}
	dedup := "event:" + eventID + ":" + eventType
	if eventID == "" {
		dedup = entityType + ":" + entityID.String() + ":" + eventType
	}
	text := captainAccountCopy(eventType, e)
	return service.RidePush{
		DedupKey:       dedup,
		RecipientID:    captain,
		App:            service.AppMopeduCaptain,
		Type:           pushType,
		AndroidChannel: service.CaptainChannelAccount,
		Title:          text.title,
		Body:           text.body,
		DeepLink:       deepLink,
		EntityID:       entityID,
		CreatedAt:      created.UTC(),
	}, !fromUserID, "", nil
}

func (c *Consumer) handleRiderCaptainAccount(ctx context.Context, eventType, eventID string, raw json.RawMessage) error {
	p, fallback, missing, err := planCaptainAccountPush(eventType, eventID, raw, time.Now().UTC())
	if err != nil {
		return err
	}
	if missing != "" {
		ridePushesTotal.WithLabelValues(captainAccountTypes[eventType], service.AppMopeduCaptain, "missing_recipient").Inc()
		slog.Warn("captain account: event lacks its push recipient; rider-service must add the field",
			"event", eventType, "field", missing)
		return nil
	}
	if fallback {
		slog.Warn("captain account: no partner_user_id on the event; pushing to partner_id, which reaches a device only if it is the captain's user id",
			"event", eventType)
	}
	c.deliverRidePush(ctx, eventType, p)
	return nil
}

// --- Sprint 4: doc expiry, fraud, summary ---------------------------------

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
	Date                      string    `json:"date"`
	SubscriptionsCount        int       `json:"subscriptions_count"`
	SubscriptionsRevenuePaise int64     `json:"subscriptions_revenue_paise"`
	RidesCount                int       `json:"rides_count"`
	OccurredAt                time.Time `json:"occurred_at"`
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
	PendingKYCCount          int       `json:"pending_kyc_count"`
	PendingVehicleCount      int       `json:"pending_vehicle_count"`
	PendingPaymentCount      int       `json:"pending_payment_count"`
	OpenComplaintsCount      int       `json:"open_complaints_count"`
	OpenSafetyIncidentsCount int       `json:"open_safety_incidents_count"`
	OccurredAt               time.Time `json:"occurred_at"`
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
