package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/google/uuid"
)

// recordingPublisher captures every Publish* call. Used by unit tests to
// assert the right events fire on each service action without needing a
// live Kafka broker.
type recordingPublisher struct {
	mu                sync.Mutex
	sosCalls          []events.SafetySOSPayload
	contactAlerts     []events.SafetyContactAlertPayload
	ackCalls          []events.SafetyIncidentLifecyclePayload
	resolveCalls      []events.SafetyIncidentLifecyclePayload
	complaintRaised   []events.ComplaintPayload
	complaintUpdated  []events.ComplaintPayload
	shareTokenCreated []events.ShareTokenCreatedPayload
	adminActions      []events.AdminActionPayload
	partnerStatus     []string
}

// noopPublisher methods we don't care about in this file. We embed it
// to inherit no-op stubs for ride / partner / subscription events.
type noopPublisherForAdminTest struct{ noopPublisher }

func (r *recordingPublisher) PublishPartnerCreated(ctx context.Context, partnerID, userID uuid.UUID, partnerType, cityID string) error {
	return nil
}
func (r *recordingPublisher) PublishPartnerKYCSubmitted(ctx context.Context, partnerID, documentID uuid.UUID, documentType string) error {
	return nil
}
func (r *recordingPublisher) PublishPartnerVehicleAdded(ctx context.Context, partnerID, vehicleID uuid.UUID, vehicleType, registration string) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionPaymentSubmitted(ctx context.Context, paymentID, partnerID, planID uuid.UUID, amount float64, currency, method string) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionPaymentVerified(ctx context.Context, paymentID, partnerID, planID uuid.UUID, amount float64, currency, method string) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionActivated(ctx context.Context, subscriptionID, partnerID, planID uuid.UUID, status string, startsAt, expiresAt time.Time) error {
	return nil
}
func (r *recordingPublisher) PublishRideRequested(ctx context.Context, rideID, customerID uuid.UUID, vehicleType, cityID string) error {
	return nil
}
func (r *recordingPublisher) PublishRideOffered(ctx context.Context, rideID, offerID, partnerID uuid.UUID, score float64, expiresAt time.Time) error {
	return nil
}
func (r *recordingPublisher) PublishRideOfferRejected(ctx context.Context, rideID, offerID, partnerID uuid.UUID, reason string) error {
	return nil
}
func (r *recordingPublisher) PublishRideOfferExpired(ctx context.Context, rideID, offerID, partnerID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishRideAssigned(ctx context.Context, rideID, partnerID, vehicleID, offerID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishRideArriving(ctx context.Context, rideID, partnerID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishRideArrived(ctx context.Context, rideID, partnerID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishRideStarted(ctx context.Context, rideID, partnerID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishRideCompleted(ctx context.Context, p events.RideCompletedPayload) error {
	return nil
}
func (r *recordingPublisher) PublishRideCancelled(ctx context.Context, p events.RideCancelledPayload) error {
	return nil
}
func (r *recordingPublisher) PublishRideExpired(ctx context.Context, rideID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishRideRated(ctx context.Context, rideID, partnerID uuid.UUID, rating int16) error {
	return nil
}
func (r *recordingPublisher) PublishPartnerOnline(ctx context.Context, partnerID uuid.UUID) error {
	return nil
}
func (r *recordingPublisher) PublishPartnerOffline(ctx context.Context, partnerID uuid.UUID) error {
	return nil
}

func (r *recordingPublisher) PublishSafetySOS(_ context.Context, p events.SafetySOSPayload) error {
	r.mu.Lock()
	r.sosCalls = append(r.sosCalls, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishSafetyContactAlert(_ context.Context, p events.SafetyContactAlertPayload) error {
	r.mu.Lock()
	r.contactAlerts = append(r.contactAlerts, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishSafetyIncidentAcknowledged(_ context.Context, p events.SafetyIncidentLifecyclePayload) error {
	r.mu.Lock()
	r.ackCalls = append(r.ackCalls, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishSafetyIncidentResolved(_ context.Context, p events.SafetyIncidentLifecyclePayload) error {
	r.mu.Lock()
	r.resolveCalls = append(r.resolveCalls, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishComplaintRaised(_ context.Context, p events.ComplaintPayload) error {
	r.mu.Lock()
	r.complaintRaised = append(r.complaintRaised, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishComplaintUpdated(_ context.Context, p events.ComplaintPayload, _ uuid.UUID) error {
	r.mu.Lock()
	r.complaintUpdated = append(r.complaintUpdated, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishShareTokenCreated(_ context.Context, p events.ShareTokenCreatedPayload) error {
	r.mu.Lock()
	r.shareTokenCreated = append(r.shareTokenCreated, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishAdminAction(_ context.Context, p events.AdminActionPayload) error {
	r.mu.Lock()
	r.adminActions = append(r.adminActions, p)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishPartnerStatusChange(_ context.Context, eventType string, _ uuid.UUID, _, _ string, _ uuid.UUID) error {
	r.mu.Lock()
	r.partnerStatus = append(r.partnerStatus, eventType)
	r.mu.Unlock()
	return nil
}
func (r *recordingPublisher) PublishAdminQueueSummary(_ context.Context, _ events.AdminQueueSummaryPayload) error {
	return nil
}
func (r *recordingPublisher) PublishDailyRevenueReport(_ context.Context, _ events.DailyRevenueReportPayload) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionGracePeriod(_ context.Context, _ events.SubscriptionGracePayload) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionExpired(_ context.Context, _ events.SubscriptionGracePayload) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionRenewed(_ context.Context, _ events.SubscriptionRenewedPayload) error {
	return nil
}
func (r *recordingPublisher) PublishSubscriptionRenewalFailed(_ context.Context, _ events.SubscriptionRenewalFailedPayload) error {
	return nil
}
func (r *recordingPublisher) PublishDocumentExpiring(_ context.Context, _ events.DocumentExpiringPayload) error {
	return nil
}
func (r *recordingPublisher) PublishPartnerFraudFlagged(_ context.Context, _ events.PartnerFraudFlaggedPayload) error {
	return nil
}

// TestRecordingPublisherFulfillsInterface — compile-time check that the
// recording publisher satisfies EventPublisher. If a new publish method
// is added to the interface, this test will refuse to compile until the
// recording stub implements it.
func TestRecordingPublisherFulfillsInterface(t *testing.T) {
	var _ EventPublisher = (*recordingPublisher)(nil)
}
