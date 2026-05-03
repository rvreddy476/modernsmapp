// Package events wraps Kafka publishing for rider-service.
//
// Every state transition that crosses a service boundary emits exactly one
// event. Downstream consumers (notification-service, analytics-service,
// trust-safety-service) listen on the rider-events topic and switch on
// event_type. Events are audit-only — they never mutate the source-of-truth
// rows — so a duplicate publish has no destructive effect.
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

// --- Partner lifecycle ----------------------------------------------------

// PartnerCreatedPayload mirrors EventRiderPartnerCreated.
type PartnerCreatedPayload struct {
	PartnerID   string    `json:"partner_id"`
	UserID      string    `json:"user_id"`
	PartnerType string    `json:"partner_type"`
	CityID      string    `json:"city_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// PartnerKYCSubmittedPayload mirrors EventRiderPartnerKYCSubmitted.
type PartnerKYCSubmittedPayload struct {
	PartnerID    string    `json:"partner_id"`
	DocumentID   string    `json:"document_id"`
	DocumentType string    `json:"document_type"`
	SubmittedAt  time.Time `json:"submitted_at"`
}

// PartnerVehicleAddedPayload mirrors EventRiderPartnerVehicleAdded.
type PartnerVehicleAddedPayload struct {
	PartnerID          string    `json:"partner_id"`
	VehicleID          string    `json:"vehicle_id"`
	VehicleType        string    `json:"vehicle_type"`
	RegistrationNumber string    `json:"registration_number"`
	CreatedAt          time.Time `json:"created_at"`
}

// PartnerStatusChangePayload covers approved / suspended / blocked events.
type PartnerStatusChangePayload struct {
	PartnerID string    `json:"partner_id"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	ActorID   string    `json:"actor_id,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishPartnerCreated(ctx context.Context, partnerID, userID uuid.UUID, partnerType, cityID string) error {
	return p.publish(ctx, events.EventRiderPartnerCreated, &userID, PartnerCreatedPayload{
		PartnerID:   partnerID.String(),
		UserID:      userID.String(),
		PartnerType: partnerType,
		CityID:      cityID,
		CreatedAt:   time.Now(),
	})
}

func (p *Producer) PublishPartnerKYCSubmitted(ctx context.Context, partnerID, documentID uuid.UUID, documentType string) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderPartnerKYCSubmitted, &id, PartnerKYCSubmittedPayload{
		PartnerID:    partnerID.String(),
		DocumentID:   documentID.String(),
		DocumentType: documentType,
		SubmittedAt:  time.Now(),
	})
}

func (p *Producer) PublishPartnerVehicleAdded(ctx context.Context, partnerID, vehicleID uuid.UUID, vehicleType, registration string) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderPartnerVehicleAdded, &id, PartnerVehicleAddedPayload{
		PartnerID:          partnerID.String(),
		VehicleID:          vehicleID.String(),
		VehicleType:        vehicleType,
		RegistrationNumber: registration,
		CreatedAt:          time.Now(),
	})
}

// --- Subscription lifecycle ----------------------------------------------

// SubscriptionPaymentPayload covers submitted / verified / rejected events.
type SubscriptionPaymentPayload struct {
	PaymentID     string    `json:"payment_id"`
	PartnerID     string    `json:"partner_id"`
	PlanID        string    `json:"plan_id"`
	Amount        float64   `json:"amount"`
	CurrencyCode  string    `json:"currency_code"`
	PaymentMethod string    `json:"payment_method"`
	Status        string    `json:"status"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// SubscriptionActivatedPayload mirrors EventRiderSubscriptionActivated.
type SubscriptionActivatedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id"`
	Status         string    `json:"status"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func (p *Producer) PublishSubscriptionPaymentSubmitted(ctx context.Context, paymentID, partnerID, planID uuid.UUID, amount float64, currency, method string) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderSubscriptionPaymentSubmitted, &id, SubscriptionPaymentPayload{
		PaymentID:     paymentID.String(),
		PartnerID:     partnerID.String(),
		PlanID:        planID.String(),
		Amount:        amount,
		CurrencyCode:  currency,
		PaymentMethod: method,
		Status:        "pending",
		OccurredAt:    time.Now(),
	})
}

func (p *Producer) PublishSubscriptionPaymentVerified(ctx context.Context, paymentID, partnerID, planID uuid.UUID, amount float64, currency, method string) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderSubscriptionPaymentVerified, &id, SubscriptionPaymentPayload{
		PaymentID:     paymentID.String(),
		PartnerID:     partnerID.String(),
		PlanID:        planID.String(),
		Amount:        amount,
		CurrencyCode:  currency,
		PaymentMethod: method,
		Status:        "verified",
		OccurredAt:    time.Now(),
	})
}

func (p *Producer) PublishSubscriptionActivated(ctx context.Context, subscriptionID, partnerID, planID uuid.UUID, status string, startsAt, expiresAt time.Time) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderSubscriptionActivated, &id, SubscriptionActivatedPayload{
		SubscriptionID: subscriptionID.String(),
		PartnerID:      partnerID.String(),
		PlanID:         planID.String(),
		Status:         status,
		StartsAt:       startsAt,
		ExpiresAt:      expiresAt,
	})
}

// --- Ride lifecycle ------------------------------------------------------

// RideRequestedPayload mirrors EventRiderRideRequested.
type RideRequestedPayload struct {
	RideID         string    `json:"ride_id"`
	CustomerUserID string    `json:"customer_user_id"`
	VehicleType    string    `json:"vehicle_type"`
	CityID         string    `json:"city_id,omitempty"`
	RequestedAt    time.Time `json:"requested_at"`
}

func (p *Producer) PublishRideRequested(ctx context.Context, rideID, customerID uuid.UUID, vehicleType, cityID string) error {
	id := customerID
	return p.publish(ctx, events.EventRiderRideRequested, &id, RideRequestedPayload{
		RideID:         rideID.String(),
		CustomerUserID: customerID.String(),
		VehicleType:    vehicleType,
		CityID:         cityID,
		RequestedAt:    time.Now(),
	})
}

// RideOfferedPayload mirrors EventRiderRideOffered. One event per partner
// the matcher offered to in this batch.
type RideOfferedPayload struct {
	RideID    string    `json:"ride_id"`
	OfferID   string    `json:"offer_id"`
	PartnerID string    `json:"partner_id"`
	Score     float64   `json:"score"`
	ExpiresAt time.Time `json:"expires_at"`
	OfferedAt time.Time `json:"offered_at"`
}

func (p *Producer) PublishRideOffered(ctx context.Context, rideID, offerID, partnerID uuid.UUID, score float64, expiresAt time.Time) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideOffered, &id, RideOfferedPayload{
		RideID:    rideID.String(),
		OfferID:   offerID.String(),
		PartnerID: partnerID.String(),
		Score:     score,
		ExpiresAt: expiresAt,
		OfferedAt: time.Now(),
	})
}

// RideOfferRejectedPayload mirrors EventRiderRideOfferRejected.
type RideOfferRejectedPayload struct {
	RideID     string    `json:"ride_id"`
	OfferID    string    `json:"offer_id"`
	PartnerID  string    `json:"partner_id"`
	Reason     string    `json:"reason,omitempty"`
	RejectedAt time.Time `json:"rejected_at"`
}

func (p *Producer) PublishRideOfferRejected(ctx context.Context, rideID, offerID, partnerID uuid.UUID, reason string) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideOfferRejected, &id, RideOfferRejectedPayload{
		RideID:     rideID.String(),
		OfferID:    offerID.String(),
		PartnerID:  partnerID.String(),
		Reason:     reason,
		RejectedAt: time.Now(),
	})
}

// RideAssignedPayload mirrors EventRiderRideAssigned. Sent when a partner
// accepts an offer and the ride is bound to them.
type RideAssignedPayload struct {
	RideID     string    `json:"ride_id"`
	PartnerID  string    `json:"partner_id"`
	VehicleID  string    `json:"vehicle_id,omitempty"`
	OfferID    string    `json:"offer_id"`
	AssignedAt time.Time `json:"assigned_at"`
}

func (p *Producer) PublishRideAssigned(ctx context.Context, rideID, partnerID, vehicleID, offerID uuid.UUID) error {
	id := partnerID
	vehStr := ""
	if vehicleID != uuid.Nil {
		vehStr = vehicleID.String()
	}
	return p.publish(ctx, events.EventRiderRideAssigned, &id, RideAssignedPayload{
		RideID:     rideID.String(),
		PartnerID:  partnerID.String(),
		VehicleID:  vehStr,
		OfferID:    offerID.String(),
		AssignedAt: time.Now(),
	})
}

// RideStatusPayload is the shared shape for arriving / arrived / started.
type RideStatusPayload struct {
	RideID     string    `json:"ride_id"`
	PartnerID  string    `json:"partner_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishRideArriving(ctx context.Context, rideID, partnerID uuid.UUID) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideArriving, &id, RideStatusPayload{
		RideID: rideID.String(), PartnerID: partnerID.String(), OccurredAt: time.Now(),
	})
}

func (p *Producer) PublishRideArrived(ctx context.Context, rideID, partnerID uuid.UUID) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideArrived, &id, RideStatusPayload{
		RideID: rideID.String(), PartnerID: partnerID.String(), OccurredAt: time.Now(),
	})
}

func (p *Producer) PublishRideStarted(ctx context.Context, rideID, partnerID uuid.UUID) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideStarted, &id, RideStatusPayload{
		RideID: rideID.String(), PartnerID: partnerID.String(), OccurredAt: time.Now(),
	})
}

// RideCompletedPayload mirrors EventRiderRideCompleted.
type RideCompletedPayload struct {
	RideID           string    `json:"ride_id"`
	PartnerID        string    `json:"partner_id"`
	FinalDistanceKM  float64   `json:"final_distance_km"`
	FinalDurationMin int       `json:"final_duration_min"`
	FinalFarePaise   int64     `json:"final_fare_paise"`
	PaymentMethod    string    `json:"payment_method"`
	PaymentStatus    string    `json:"payment_status"`
	FlaggedForReview bool      `json:"flagged_for_review"`
	CompletedAt      time.Time `json:"completed_at"`
}

func (p *Producer) PublishRideCompleted(ctx context.Context, payload RideCompletedPayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderRideCompleted, &id, payload)
}

// RideCancelledPayload mirrors EventRiderRideCancelled.
type RideCancelledPayload struct {
	RideID               string    `json:"ride_id"`
	CancelledByKind      string    `json:"cancelled_by_kind"`
	CancelledByUserID    string    `json:"cancelled_by_user_id,omitempty"`
	Reason               string    `json:"reason,omitempty"`
	CancellationFeePaise int64     `json:"cancellation_fee_paise"`
	CancelledAt          time.Time `json:"cancelled_at"`
}

func (p *Producer) PublishRideCancelled(ctx context.Context, payload RideCancelledPayload) error {
	var id *uuid.UUID
	if u, err := uuid.Parse(payload.CancelledByUserID); err == nil {
		id = &u
	}
	return p.publish(ctx, events.EventRiderRideCancelled, id, payload)
}

// RideExpiredPayload mirrors EventRiderRideExpired.
type RideExpiredPayload struct {
	RideID    string    `json:"ride_id"`
	ExpiredAt time.Time `json:"expired_at"`
}

func (p *Producer) PublishRideExpired(ctx context.Context, rideID uuid.UUID) error {
	return p.publish(ctx, events.EventRiderRideExpired, nil, RideExpiredPayload{
		RideID: rideID.String(), ExpiredAt: time.Now(),
	})
}

// RideOfferExpiredPayload mirrors EventRiderRideOfferExpired.
type RideOfferExpiredPayload struct {
	OfferID   string    `json:"offer_id"`
	RideID    string    `json:"ride_id"`
	PartnerID string    `json:"partner_id"`
	ExpiredAt time.Time `json:"expired_at"`
}

func (p *Producer) PublishRideOfferExpired(ctx context.Context, rideID, offerID, partnerID uuid.UUID) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideOfferExpired, &id, RideOfferExpiredPayload{
		OfferID: offerID.String(), RideID: rideID.String(), PartnerID: partnerID.String(), ExpiredAt: time.Now(),
	})
}

// RideRatedPayload mirrors EventRiderRideRated.
type RideRatedPayload struct {
	RideID    string    `json:"ride_id"`
	PartnerID string    `json:"partner_id"`
	Rating    int16     `json:"rating"`
	RatedAt   time.Time `json:"rated_at"`
}

func (p *Producer) PublishRideRated(ctx context.Context, rideID, partnerID uuid.UUID, rating int16) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderRideRated, &id, RideRatedPayload{
		RideID: rideID.String(), PartnerID: partnerID.String(), Rating: rating, RatedAt: time.Now(),
	})
}

// PartnerOnlinePayload mirrors EventRiderPartnerOnline / Offline.
type PartnerOnlinePayload struct {
	PartnerID  string    `json:"partner_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishPartnerOnline(ctx context.Context, partnerID uuid.UUID) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderPartnerOnline, &id, PartnerOnlinePayload{
		PartnerID: partnerID.String(), OccurredAt: time.Now(),
	})
}

func (p *Producer) PublishPartnerOffline(ctx context.Context, partnerID uuid.UUID) error {
	id := partnerID
	return p.publish(ctx, events.EventRiderPartnerOffline, &id, PartnerOnlinePayload{
		PartnerID: partnerID.String(), OccurredAt: time.Now(),
	})
}

// --- Sprint 3: safety + complaints + admin --------------------------------

// SafetySOSPayload mirrors EventRiderSafetySOS.
type SafetySOSPayload struct {
	IncidentID string    `json:"incident_id"`
	RideID     string    `json:"ride_id"`
	CustomerID string    `json:"customer_id"`
	PartnerID  string    `json:"partner_id,omitempty"`
	Severity   string    `json:"severity"`
	Lat        float64   `json:"lat,omitempty"`
	Lng        float64   `json:"lng,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishSafetySOS(ctx context.Context, payload SafetySOSPayload) error {
	id, _ := uuid.Parse(payload.CustomerID)
	return p.publish(ctx, events.EventRiderSafetySOS, &id, payload)
}

// SafetyContactAlertPayload mirrors EventRiderSafetyContactAlert. Sent
// after a successful SOS so notification-service can push to the trusted
// contact (out-of-band SMS or push if they are an AtPost user too).
type SafetyContactAlertPayload struct {
	IncidentID   string    `json:"incident_id"`
	RideID       string    `json:"ride_id"`
	CustomerID   string    `json:"customer_id"`
	ContactName  string    `json:"contact_name"`
	ContactPhone string    `json:"contact_phone"`
	ShareURL     string    `json:"share_url,omitempty"`
	Message      string    `json:"message"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (p *Producer) PublishSafetyContactAlert(ctx context.Context, payload SafetyContactAlertPayload) error {
	id, _ := uuid.Parse(payload.CustomerID)
	return p.publish(ctx, events.EventRiderSafetyContactAlert, &id, payload)
}

// SafetyIncidentLifecyclePayload covers acknowledged + resolved.
type SafetyIncidentLifecyclePayload struct {
	IncidentID string    `json:"incident_id"`
	RideID     string    `json:"ride_id,omitempty"`
	AdminID    string    `json:"admin_id"`
	Note       string    `json:"note,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishSafetyIncidentAcknowledged(ctx context.Context, payload SafetyIncidentLifecyclePayload) error {
	id, _ := uuid.Parse(payload.AdminID)
	return p.publish(ctx, events.EventRiderSafetyIncidentAcknowledged, &id, payload)
}

func (p *Producer) PublishSafetyIncidentResolved(ctx context.Context, payload SafetyIncidentLifecyclePayload) error {
	id, _ := uuid.Parse(payload.AdminID)
	return p.publish(ctx, events.EventRiderSafetyIncidentResolved, &id, payload)
}

// ComplaintPayload mirrors EventRiderComplaintRaised + Updated.
type ComplaintPayload struct {
	ComplaintID string    `json:"complaint_id"`
	RideID      string    `json:"ride_id"`
	CustomerID  string    `json:"customer_id"`
	PartnerID   string    `json:"partner_id,omitempty"`
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func (p *Producer) PublishComplaintRaised(ctx context.Context, payload ComplaintPayload) error {
	id, _ := uuid.Parse(payload.CustomerID)
	return p.publish(ctx, events.EventRiderComplaintRaised, &id, payload)
}

func (p *Producer) PublishComplaintUpdated(ctx context.Context, payload ComplaintPayload, adminID uuid.UUID) error {
	id := adminID
	return p.publish(ctx, events.EventRiderComplaintUpdated, &id, payload)
}

// ShareTokenCreatedPayload mirrors EventRiderShareTokenCreated.
type ShareTokenCreatedPayload struct {
	Token      string    `json:"token"`
	RideID     string    `json:"ride_id"`
	CustomerID string    `json:"customer_id"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (p *Producer) PublishShareTokenCreated(ctx context.Context, payload ShareTokenCreatedPayload) error {
	id, _ := uuid.Parse(payload.CustomerID)
	return p.publish(ctx, events.EventRiderShareTokenCreated, &id, payload)
}

// AdminActionPayload is the generic envelope for admin-driven mutations.
type AdminActionPayload struct {
	AdminID    string    `json:"admin_id"`
	Action     string    `json:"action"`
	TargetKind string    `json:"target_kind"`
	TargetID   string    `json:"target_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (p *Producer) PublishAdminAction(ctx context.Context, payload AdminActionPayload) error {
	id, _ := uuid.Parse(payload.AdminID)
	return p.publish(ctx, events.EventRiderAdminAction, &id, payload)
}

// PartnerStatusChange wraps approve / reject / suspend / block. The status
// field carries the new partner status; reason is admin-supplied free-text.
func (p *Producer) PublishPartnerStatusChange(ctx context.Context, eventType string, partnerID uuid.UUID, status, reason string, actorID uuid.UUID) error {
	id := actorID
	return p.publish(ctx, eventType, &id, PartnerStatusChangePayload{
		PartnerID:  partnerID.String(),
		Status:     status,
		Reason:     reason,
		ActorID:    actorID.String(),
		OccurredAt: time.Now(),
	})
}

// --- Sprint 4: background-jobs, fraud, revenue, admin-summary -------------

// SubscriptionGracePayload mirrors EventRiderSubscriptionGracePeriod. Sent
// when a subscription transitions active -> grace_period.
type SubscriptionGracePayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	GraceEndsAt    time.Time `json:"grace_ends_at"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (p *Producer) PublishSubscriptionGracePeriod(ctx context.Context, payload SubscriptionGracePayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderSubscriptionGracePeriod, &id, payload)
}

// PublishSubscriptionExpired emits EventRiderSubscriptionExpired. Same
// payload shape as the grace-period event; downstream consumers switch
// on the event-type field. Used by RunGracePeriodTransition's second
// arm (grace_period -> expired).
func (p *Producer) PublishSubscriptionExpired(ctx context.Context, payload SubscriptionGracePayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderSubscriptionExpired, &id, payload)
}

// SubscriptionRenewedPayload mirrors EventRiderSubscriptionRenewed.
type SubscriptionRenewedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id"`
	AmountPaise    int64     `json:"amount_paise"`
	NewExpiresAt   time.Time `json:"new_expires_at"`
	WalletTxnID    string    `json:"wallet_txn_id,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (p *Producer) PublishSubscriptionRenewed(ctx context.Context, payload SubscriptionRenewedPayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderSubscriptionRenewed, &id, payload)
}

// SubscriptionRenewalFailedPayload mirrors EventRiderSubscriptionRenewalFailed.
type SubscriptionRenewalFailedPayload struct {
	SubscriptionID string    `json:"subscription_id"`
	PartnerID      string    `json:"partner_id"`
	PlanID         string    `json:"plan_id"`
	AmountPaise    int64     `json:"amount_paise"`
	FailureCount   int       `json:"failure_count"`
	AutoRenewOff   bool      `json:"auto_renew_off"`
	Reason         string    `json:"reason"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (p *Producer) PublishSubscriptionRenewalFailed(ctx context.Context, payload SubscriptionRenewalFailedPayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderSubscriptionRenewalFailed, &id, payload)
}

// DocumentExpiringPayload mirrors EventRiderDocumentExpiring.
type DocumentExpiringPayload struct {
	PartnerID    string    `json:"partner_id"`
	DocumentID   string    `json:"document_id"`
	DocumentKind string    `json:"document_kind"`
	OwnerKind    string    `json:"owner_kind"`
	ExpiresAt    time.Time `json:"expires_at"`
	Bucket       string    `json:"bucket"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (p *Producer) PublishDocumentExpiring(ctx context.Context, payload DocumentExpiringPayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderDocumentExpiring, &id, payload)
}

// PartnerFraudFlaggedPayload mirrors EventRiderPartnerFraudFlagged.
type PartnerFraudFlaggedPayload struct {
	PartnerID    string    `json:"partner_id"`
	FraudScore   float64   `json:"fraud_score"`
	AutoSuspend  bool      `json:"auto_suspend"`
	Reasons      []string  `json:"reasons,omitempty"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (p *Producer) PublishPartnerFraudFlagged(ctx context.Context, payload PartnerFraudFlaggedPayload) error {
	id, _ := uuid.Parse(payload.PartnerID)
	return p.publish(ctx, events.EventRiderPartnerFraudFlagged, &id, payload)
}

// DailyRevenueReportPayload mirrors EventRiderDailyRevenueReport.
type DailyRevenueReportPayload struct {
	Date                       string    `json:"date"`
	SubscriptionsCount         int       `json:"subscriptions_count"`
	SubscriptionsRevenuePaise  int64     `json:"subscriptions_revenue_paise"`
	RidesCount                 int       `json:"rides_count"`
	RidesCompleted             int       `json:"rides_completed"`
	RidesCancelled             int       `json:"rides_cancelled"`
	FareTotalPaise             int64     `json:"fare_total_paise"`
	CancellationFeesPaise      int64     `json:"cancellation_fees_paise"`
	OccurredAt                 time.Time `json:"occurred_at"`
}

func (p *Producer) PublishDailyRevenueReport(ctx context.Context, payload DailyRevenueReportPayload) error {
	return p.publish(ctx, events.EventRiderDailyRevenueReport, nil, payload)
}

// AdminQueueSummaryPayload mirrors EventRiderAdminQueueSummary.
type AdminQueueSummaryPayload struct {
	PendingKYCCount         int       `json:"pending_kyc_count"`
	PendingVehicleCount     int       `json:"pending_vehicle_count"`
	PendingPaymentCount     int       `json:"pending_payment_count"`
	OpenComplaintsCount     int       `json:"open_complaints_count"`
	OpenSafetyIncidentsCount int      `json:"open_safety_incidents_count"`
	OccurredAt              time.Time `json:"occurred_at"`
}

func (p *Producer) PublishAdminQueueSummary(ctx context.Context, payload AdminQueueSummaryPayload) error {
	return p.publish(ctx, events.EventRiderAdminQueueSummary, nil, payload)
}

// --- internal -------------------------------------------------------------

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
