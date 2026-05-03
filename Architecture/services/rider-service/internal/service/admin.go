// Admin surface for Sprint 3.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §17 (Admin endpoints).
//
// All methods on this surface are gated by the http audit middleware which
// writes one rider_admin_audit_logs row per request. The service layer also
// emits EventRiderAdminAction so downstream services (analytics, trust-and-
// safety) get a Kafka stream of admin mutations independent of the DB row.
//
// Admin actor identity is whatever the middleware extracts from the
// X-User-ID header (production: JWT claim with rider:admin role). The
// middleware sets c.Set("admin_user_id", id) so handlers can pass it down.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Dashboard returns aggregated counts for the admin home page.
func (s *Service) Dashboard(ctx context.Context) (*store.AdminDashboardCounts, error) {
	return s.store.AdminDashboardCounts(ctx)
}

// ListPartnersForAdmin returns partners filtered by status / free-text query.
func (s *Service) ListPartnersForAdmin(ctx context.Context, status, query string, limit, offset int) ([]store.Partner, error) {
	return s.store.ListPartners(ctx, store.PartnerListFilter{
		Status: status,
		Query:  strings.TrimSpace(query),
		Limit:  limit,
		Offset: offset,
	})
}

// GetPartnerForAdmin reads a single partner row by id (no ownership gate).
func (s *Service) GetPartnerForAdmin(ctx context.Context, partnerID uuid.UUID) (*store.Partner, error) {
	p, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	return p, nil
}

// ApprovePartner moves the partner to status=approved + kyc_status=verified.
// Emits EventRiderPartnerApproved + EventRiderAdminAction.
func (s *Service) ApprovePartner(ctx context.Context, partnerID, adminID uuid.UUID) (*store.Partner, error) {
	if err := s.store.SetPartnerApprovedAt(ctx, partnerID); err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	p, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		return nil, err
	}
	if perr := s.producer.PublishPartnerStatusChange(ctx, sharedevents.EventRiderPartnerApproved, partnerID, "approved", "", adminID); perr != nil {
		slog.Warn("rider: publish partner.approved failed", "partner_id", partnerID, "error", perr)
	}
	s.emitAdminAction(ctx, adminID, "partner.approve", "partner", partnerID, "")
	return p, nil
}

// RejectPartner sets status=rejected and stores the admin reason.
func (s *Service) RejectPartner(ctx context.Context, partnerID, adminID uuid.UUID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("invalid: reason required")
	}
	if err := s.store.UpdatePartnerStatus(ctx, partnerID, "rejected"); err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	if perr := s.producer.PublishPartnerStatusChange(ctx, sharedevents.EventRiderPartnerKYCRejected, partnerID, "rejected", reason, adminID); perr != nil {
		slog.Warn("rider: publish partner.rejected failed", "partner_id", partnerID, "error", perr)
	}
	s.emitAdminAction(ctx, adminID, "partner.reject", "partner", partnerID, reason)
	return nil
}

// SuspendPartner moves the partner to status=suspended.
func (s *Service) SuspendPartner(ctx context.Context, partnerID, adminID uuid.UUID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("invalid: reason required")
	}
	if err := s.store.UpdatePartnerStatus(ctx, partnerID, "suspended"); err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	if perr := s.producer.PublishPartnerStatusChange(ctx, sharedevents.EventRiderPartnerSuspended, partnerID, "suspended", reason, adminID); perr != nil {
		slog.Warn("rider: publish partner.suspended failed", "partner_id", partnerID, "error", perr)
	}
	s.emitAdminAction(ctx, adminID, "partner.suspend", "partner", partnerID, reason)
	return nil
}

// BlockPartner moves the partner to status=blocked.
func (s *Service) BlockPartner(ctx context.Context, partnerID, adminID uuid.UUID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("invalid: reason required")
	}
	if err := s.store.UpdatePartnerStatus(ctx, partnerID, "blocked"); err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	if perr := s.producer.PublishPartnerStatusChange(ctx, sharedevents.EventRiderPartnerBlocked, partnerID, "blocked", reason, adminID); perr != nil {
		slog.Warn("rider: publish partner.blocked failed", "partner_id", partnerID, "error", perr)
	}
	s.emitAdminAction(ctx, adminID, "partner.block", "partner", partnerID, reason)
	return nil
}

// --- Documents -------------------------------------------------------------

// ListPartnerDocumentsForAdmin returns documents in the given status.
func (s *Service) ListPartnerDocumentsForAdmin(ctx context.Context, status string, limit, offset int) ([]store.PartnerDocument, error) {
	return s.store.ListPartnerDocumentsByStatus(ctx, status, limit, offset)
}

// VerifyDocument moves a document to status=approved.
func (s *Service) VerifyDocument(ctx context.Context, docID, adminID uuid.UUID) (*store.PartnerDocument, error) {
	d, err := s.store.SetPartnerDocumentStatus(ctx, docID, "approved", nil)
	if err != nil {
		if errors.Is(err, store.ErrDocumentNotFound) {
			return nil, fmt.Errorf("not_found: document")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "document.verify", "document", docID, "")
	return d, nil
}

// RejectDocument moves a document to status=rejected with reason.
func (s *Service) RejectDocument(ctx context.Context, docID, adminID uuid.UUID, reason string) (*store.PartnerDocument, error) {
	r := strings.TrimSpace(reason)
	if r == "" {
		return nil, fmt.Errorf("invalid: reason required")
	}
	d, err := s.store.SetPartnerDocumentStatus(ctx, docID, "rejected", &r)
	if err != nil {
		if errors.Is(err, store.ErrDocumentNotFound) {
			return nil, fmt.Errorf("not_found: document")
		}
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "document.reject", "document", docID, r)
	return d, nil
}

// --- Vehicles --------------------------------------------------------------

// ListVehiclesForAdmin returns vehicles filtered by status.
func (s *Service) ListVehiclesForAdmin(ctx context.Context, status string, limit, offset int) ([]store.Vehicle, error) {
	return s.store.ListVehiclesByStatus(ctx, status, limit, offset)
}

// VerifyVehicle moves a vehicle to status=approved.
func (s *Service) VerifyVehicle(ctx context.Context, vehicleID, adminID uuid.UUID) error {
	if err := s.store.SetVehicleStatus(ctx, vehicleID, "approved"); err != nil {
		if errors.Is(err, store.ErrVehicleNotFoundAdmin) {
			return fmt.Errorf("not_found: vehicle")
		}
		return err
	}
	s.emitAdminAction(ctx, adminID, "vehicle.verify", "vehicle", vehicleID, "")
	return nil
}

// RejectVehicle moves a vehicle to status=rejected with reason.
func (s *Service) RejectVehicle(ctx context.Context, vehicleID, adminID uuid.UUID, reason string) error {
	r := strings.TrimSpace(reason)
	if r == "" {
		return fmt.Errorf("invalid: reason required")
	}
	if err := s.store.SetVehicleStatus(ctx, vehicleID, "rejected"); err != nil {
		if errors.Is(err, store.ErrVehicleNotFoundAdmin) {
			return fmt.Errorf("not_found: vehicle")
		}
		return err
	}
	s.emitAdminAction(ctx, adminID, "vehicle.reject", "vehicle", vehicleID, r)
	return nil
}

// --- Subscription payments -------------------------------------------------

// ListSubscriptionPaymentsForAdmin returns payments filtered by status.
func (s *Service) ListSubscriptionPaymentsForAdmin(ctx context.Context, status string, limit, offset int) ([]store.SubscriptionPayment, error) {
	return s.store.ListSubscriptionPaymentsByStatus(ctx, status, limit, offset)
}

// VerifySubscriptionPayment activates the subscription tied to the payment.
// Reuses the shared activateSubscriptionWithPlan path so wallet + admin
// activations land identically in the database.
func (s *Service) VerifySubscriptionPayment(ctx context.Context, paymentID, adminID uuid.UUID) (*store.PartnerSubscription, error) {
	sub, err := s.ActivateSubscription(ctx, paymentID, adminID)
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "payment.verify", "payment", paymentID, "")
	return sub, nil
}

// RejectSubscriptionPayment marks a payment failed.
func (s *Service) RejectSubscriptionPayment(ctx context.Context, paymentID, adminID uuid.UUID, reason string) error {
	r := strings.TrimSpace(reason)
	if r == "" {
		return fmt.Errorf("invalid: reason required")
	}
	pay, err := s.store.GetSubscriptionPayment(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("not_found: payment")
	}
	if err := s.store.MarkPaymentFailed(ctx, paymentID, r); err != nil {
		return err
	}
	if perr := s.producer.PublishSubscriptionPaymentSubmitted(ctx, paymentID, pay.PartnerID, pay.PlanID, pay.Amount, pay.CurrencyCode, pay.PaymentMethod); perr != nil {
		slog.Warn("rider: publish payment rejection failed", "payment_id", paymentID, "error", perr)
	}
	s.emitAdminAction(ctx, adminID, "payment.reject", "payment", paymentID, r)
	return nil
}

// --- Rides admin ----------------------------------------------------------

// ListRidesForAdmin returns rides matching the filter.
func (s *Service) ListRidesForAdmin(ctx context.Context, status, query string, since *time.Time, limit, offset int) ([]store.Ride, error) {
	return s.store.ListRidesAdmin(ctx, store.RideListFilter{
		Status: status,
		Query:  strings.TrimSpace(query),
		Since:  since,
		Limit:  limit,
		Offset: offset,
	})
}

// ListLiveRidesForAdmin returns currently in-progress rides.
func (s *Service) ListLiveRidesForAdmin(ctx context.Context, limit int) ([]store.Ride, error) {
	return s.store.ListLiveRides(ctx, limit)
}

// AdminCancelRide is the admin override on the customer/partner cancel flow.
// Reuses the existing CancelRide service so the state machine + audit history
// stay consistent.
func (s *Service) AdminCancelRide(ctx context.Context, rideID, adminID uuid.UUID, reason string) (*store.Ride, error) {
	r := strings.TrimSpace(reason)
	if r == "" {
		return nil, fmt.Errorf("invalid: reason required")
	}
	out, err := s.CancelRide(ctx, adminID, rideID, "admin", CancelRideRequest{Reason: r})
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "ride.cancel", "ride", rideID, r)
	return out, nil
}

// --- Audit logs -----------------------------------------------------------

// ListAuditLogs returns audit rows filtered by actor / action / target.
func (s *Service) ListAuditLogs(ctx context.Context, f store.AuditFilter) ([]store.AuditLog, error) {
	return s.store.ListAuditLogs(ctx, f)
}

// TransitionRideForJob is the cron-friendly wrapper around the internal
// transitionRide helper. Jobs (e.g. RunStaleRideCleanup) call this to move
// a ride to a terminal state. Reuses the state-machine guard so a ride
// already in a terminal status is a no-op (returns nil).
func (s *Service) TransitionRideForJob(ctx context.Context, rideID uuid.UUID, target, reason string) error {
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return fmt.Errorf("not_found: ride")
		}
		return err
	}
	r := reason
	rp := &r
	if reason == "" {
		rp = nil
	}
	if err := s.transitionRide(ctx, ride, target, "system", nil, rp); err != nil {
		return err
	}
	return nil
}

// emitAdminAction publishes a generic admin-action event. Errors are logged
// but never bubble up — admin work cannot stall waiting on Kafka.
func (s *Service) emitAdminAction(ctx context.Context, adminID uuid.UUID, action, targetKind string, targetID uuid.UUID, reason string) {
	payload := events.AdminActionPayload{
		AdminID:    adminID.String(),
		Action:     action,
		TargetKind: targetKind,
		TargetID:   targetID.String(),
		Reason:     reason,
		OccurredAt: time.Now().UTC(),
	}
	if perr := s.producer.PublishAdminAction(ctx, payload); perr != nil {
		slog.Warn("rider: publish admin.action failed", "action", action, "target", targetID, "error", perr)
	}
}
