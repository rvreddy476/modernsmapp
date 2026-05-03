// Package service holds the business-logic layer for rider-service (Mopedu).
//
// The Service struct is the single composition root: it depends on the store
// (Postgres), the wallet client (for partner subscription debits), and the
// events producer (Kafka). Sub-files split methods by aggregate (partner,
// subscription, vehicles, documents, cities, fares) so each surface can be
// reasoned about in isolation while sharing the same struct.
package service

import (
	"context"
	"time"

	"github.com/atpost/rider-service/internal/digilocker"
	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Service is the rider-service business-logic layer.
type Service struct {
	store            *store.Store
	wallet           wallet.Client
	digilockerClient digilocker.Client
	rdb              *redis.Client
	producer         EventPublisher
	cfg              Config
}

// Config tunes service-level constants.
type Config struct {
	// DefaultGracePeriodDays is the grace window applied when a plan does
	// not specify one (defensive default; seeded plans set their own).
	DefaultGracePeriodDays int
	// WindingFactor is multiplied against straight-line haversine distance
	// to approximate road distance for v1 fare estimates. Mopedu spec §3.5
	// notes this is a stand-in for the real Google Maps integration in S2.
	WindingFactor float64
	// AverageSpeedKMPH is used to estimate ride duration alongside the
	// winding factor. India city averages tend to 18–25 km/h.
	AverageSpeedKMPH float64
}

// EventPublisher is the subset of *events.Producer the service needs. Stays
// an interface so unit tests can inject a no-op implementation.
type EventPublisher interface {
	PublishPartnerCreated(ctx context.Context, partnerID, userID uuid.UUID, partnerType, cityID string) error
	PublishPartnerKYCSubmitted(ctx context.Context, partnerID, documentID uuid.UUID, documentType string) error
	PublishPartnerVehicleAdded(ctx context.Context, partnerID, vehicleID uuid.UUID, vehicleType, registration string) error
	PublishSubscriptionPaymentSubmitted(ctx context.Context, paymentID, partnerID, planID uuid.UUID, amount float64, currency, method string) error
	PublishSubscriptionPaymentVerified(ctx context.Context, paymentID, partnerID, planID uuid.UUID, amount float64, currency, method string) error
	PublishSubscriptionActivated(ctx context.Context, subscriptionID, partnerID, planID uuid.UUID, status string, startsAt, expiresAt time.Time) error
	PublishRideRequested(ctx context.Context, rideID, customerID uuid.UUID, vehicleType, cityID string) error

	// S2: ride lifecycle + offer + partner online/offline.
	PublishRideOffered(ctx context.Context, rideID, offerID, partnerID uuid.UUID, score float64, expiresAt time.Time) error
	PublishRideOfferRejected(ctx context.Context, rideID, offerID, partnerID uuid.UUID, reason string) error
	PublishRideOfferExpired(ctx context.Context, rideID, offerID, partnerID uuid.UUID) error
	PublishRideAssigned(ctx context.Context, rideID, partnerID, vehicleID, offerID uuid.UUID) error
	PublishRideArriving(ctx context.Context, rideID, partnerID uuid.UUID) error
	PublishRideArrived(ctx context.Context, rideID, partnerID uuid.UUID) error
	PublishRideStarted(ctx context.Context, rideID, partnerID uuid.UUID) error
	PublishRideCompleted(ctx context.Context, payload events.RideCompletedPayload) error
	PublishRideCancelled(ctx context.Context, payload events.RideCancelledPayload) error
	PublishRideExpired(ctx context.Context, rideID uuid.UUID) error
	PublishRideRated(ctx context.Context, rideID, partnerID uuid.UUID, rating int16) error
	PublishPartnerOnline(ctx context.Context, partnerID uuid.UUID) error
	PublishPartnerOffline(ctx context.Context, partnerID uuid.UUID) error

	// S3: safety, complaints, admin.
	PublishSafetySOS(ctx context.Context, payload events.SafetySOSPayload) error
	PublishSafetyContactAlert(ctx context.Context, payload events.SafetyContactAlertPayload) error
	PublishSafetyIncidentAcknowledged(ctx context.Context, payload events.SafetyIncidentLifecyclePayload) error
	PublishSafetyIncidentResolved(ctx context.Context, payload events.SafetyIncidentLifecyclePayload) error
	PublishComplaintRaised(ctx context.Context, payload events.ComplaintPayload) error
	PublishComplaintUpdated(ctx context.Context, payload events.ComplaintPayload, adminID uuid.UUID) error
	PublishShareTokenCreated(ctx context.Context, payload events.ShareTokenCreatedPayload) error
	PublishAdminAction(ctx context.Context, payload events.AdminActionPayload) error
	PublishPartnerStatusChange(ctx context.Context, eventType string, partnerID uuid.UUID, status, reason string, actorID uuid.UUID) error

	// S4: background-job events.
	PublishSubscriptionGracePeriod(ctx context.Context, payload events.SubscriptionGracePayload) error
	PublishSubscriptionExpired(ctx context.Context, payload events.SubscriptionGracePayload) error
	PublishSubscriptionRenewed(ctx context.Context, payload events.SubscriptionRenewedPayload) error
	PublishSubscriptionRenewalFailed(ctx context.Context, payload events.SubscriptionRenewalFailedPayload) error
	PublishDocumentExpiring(ctx context.Context, payload events.DocumentExpiringPayload) error
	PublishPartnerFraudFlagged(ctx context.Context, payload events.PartnerFraudFlaggedPayload) error
	PublishDailyRevenueReport(ctx context.Context, payload events.DailyRevenueReportPayload) error
	PublishAdminQueueSummary(ctx context.Context, payload events.AdminQueueSummaryPayload) error
}

// noopPublisher is the default. Replaced via SetProducer in main.go.
type noopPublisher struct{}

func (noopPublisher) PublishPartnerCreated(_ context.Context, _, _ uuid.UUID, _, _ string) error {
	return nil
}
func (noopPublisher) PublishPartnerKYCSubmitted(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}
func (noopPublisher) PublishPartnerVehicleAdded(_ context.Context, _, _ uuid.UUID, _, _ string) error {
	return nil
}
func (noopPublisher) PublishSubscriptionPaymentSubmitted(_ context.Context, _, _, _ uuid.UUID, _ float64, _, _ string) error {
	return nil
}
func (noopPublisher) PublishSubscriptionPaymentVerified(_ context.Context, _, _, _ uuid.UUID, _ float64, _, _ string) error {
	return nil
}
func (noopPublisher) PublishSubscriptionActivated(_ context.Context, _, _, _ uuid.UUID, _ string, _, _ time.Time) error {
	return nil
}
func (noopPublisher) PublishRideRequested(_ context.Context, _, _ uuid.UUID, _, _ string) error {
	return nil
}
func (noopPublisher) PublishRideOffered(_ context.Context, _, _, _ uuid.UUID, _ float64, _ time.Time) error {
	return nil
}
func (noopPublisher) PublishRideOfferRejected(_ context.Context, _, _, _ uuid.UUID, _ string) error {
	return nil
}
func (noopPublisher) PublishRideOfferExpired(_ context.Context, _, _, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishRideAssigned(_ context.Context, _, _, _, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishRideArriving(_ context.Context, _, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishRideArrived(_ context.Context, _, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishRideStarted(_ context.Context, _, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishRideCompleted(_ context.Context, _ events.RideCompletedPayload) error {
	return nil
}
func (noopPublisher) PublishRideCancelled(_ context.Context, _ events.RideCancelledPayload) error {
	return nil
}
func (noopPublisher) PublishRideExpired(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishRideRated(_ context.Context, _, _ uuid.UUID, _ int16) error {
	return nil
}
func (noopPublisher) PublishPartnerOnline(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishPartnerOffline(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishSafetySOS(_ context.Context, _ events.SafetySOSPayload) error {
	return nil
}
func (noopPublisher) PublishSafetyContactAlert(_ context.Context, _ events.SafetyContactAlertPayload) error {
	return nil
}
func (noopPublisher) PublishSafetyIncidentAcknowledged(_ context.Context, _ events.SafetyIncidentLifecyclePayload) error {
	return nil
}
func (noopPublisher) PublishSafetyIncidentResolved(_ context.Context, _ events.SafetyIncidentLifecyclePayload) error {
	return nil
}
func (noopPublisher) PublishComplaintRaised(_ context.Context, _ events.ComplaintPayload) error {
	return nil
}
func (noopPublisher) PublishComplaintUpdated(_ context.Context, _ events.ComplaintPayload, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishShareTokenCreated(_ context.Context, _ events.ShareTokenCreatedPayload) error {
	return nil
}
func (noopPublisher) PublishAdminAction(_ context.Context, _ events.AdminActionPayload) error {
	return nil
}
func (noopPublisher) PublishPartnerStatusChange(_ context.Context, _ string, _ uuid.UUID, _, _ string, _ uuid.UUID) error {
	return nil
}
func (noopPublisher) PublishSubscriptionGracePeriod(_ context.Context, _ events.SubscriptionGracePayload) error {
	return nil
}
func (noopPublisher) PublishSubscriptionExpired(_ context.Context, _ events.SubscriptionGracePayload) error {
	return nil
}
func (noopPublisher) PublishSubscriptionRenewed(_ context.Context, _ events.SubscriptionRenewedPayload) error {
	return nil
}
func (noopPublisher) PublishSubscriptionRenewalFailed(_ context.Context, _ events.SubscriptionRenewalFailedPayload) error {
	return nil
}
func (noopPublisher) PublishDocumentExpiring(_ context.Context, _ events.DocumentExpiringPayload) error {
	return nil
}
func (noopPublisher) PublishPartnerFraudFlagged(_ context.Context, _ events.PartnerFraudFlaggedPayload) error {
	return nil
}
func (noopPublisher) PublishDailyRevenueReport(_ context.Context, _ events.DailyRevenueReportPayload) error {
	return nil
}
func (noopPublisher) PublishAdminQueueSummary(_ context.Context, _ events.AdminQueueSummaryPayload) error {
	return nil
}

// New returns a Service wired to the given store and wallet client. Producer
// defaults to a no-op; main.go calls SetProducer with the real Kafka publisher.
func New(s *store.Store, w wallet.Client, cfg Config) *Service {
	if cfg.WindingFactor <= 0 {
		cfg.WindingFactor = 1.4
	}
	if cfg.AverageSpeedKMPH <= 0 {
		cfg.AverageSpeedKMPH = 22.0
	}
	if cfg.DefaultGracePeriodDays <= 0 {
		cfg.DefaultGracePeriodDays = 3
	}
	return &Service{
		store:    s,
		wallet:   w,
		producer: noopPublisher{},
		cfg:      cfg,
	}
}

// SetProducer swaps in a real event publisher.
func (s *Service) SetProducer(p EventPublisher) { s.producer = p }

// SetDigiLockerClient injects the partner client. main.go selects HTTP vs
// Mock via DIGILOCKER_MODE.
func (s *Service) SetDigiLockerClient(c digilocker.Client) { s.digilockerClient = c }

// SetRedis injects a Redis client used for the DigiLocker PKCE state cache.
func (s *Service) SetRedis(c *redis.Client) { s.rdb = c }

// Cfg returns a copy of the active config — handy in tests.
func (s *Service) Cfg() Config { return s.cfg }

// Store returns the underlying store. Exposed so handlers can run thin
// read-only fan-outs without forcing every query through the service layer.
func (s *Service) Store() *store.Store { return s.store }
