// Complaint surface for Sprint 3.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §12 (Customer complaints) + §17.
//
// Lifecycle: open -> under_review -> resolved | dismissed. The customer
// can raise + read; the admin can read all and update status. Every status
// change emits EventRiderComplaintUpdated; the initial raise emits
// EventRiderComplaintRaised.
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
	"github.com/google/uuid"
)

// CreateComplaintRequest is the input shape for CreateComplaint.
type CreateComplaintRequest struct {
	Category    string
	Description string
}

// CreateComplaint records a customer-raised complaint against a specific
// ride. Validates ownership + category, attaches the partner reference if
// the ride had one assigned, emits EventRiderComplaintRaised.
func (s *Service) CreateComplaint(ctx context.Context, customerID, rideID uuid.UUID, req CreateComplaintRequest) (*store.Complaint, error) {
	if customerID == uuid.Nil || rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer_id and ride_id required")
	}
	if !store.IsValidComplaintCategory(req.Category) {
		return nil, fmt.Errorf("invalid: category must be one of driver_behavior, vehicle_condition, route_deviation, fare_dispute, safety, other")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if ride.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}
	var desc *string
	if d := strings.TrimSpace(req.Description); d != "" {
		desc = &d
	}
	c, err := s.store.CreateComplaint(ctx, store.CreateComplaintInput{
		RideID:      rideID,
		CustomerID:  customerID,
		PartnerID:   ride.PartnerID,
		Category:    req.Category,
		Description: desc,
	})
	if err != nil {
		return nil, fmt.Errorf("create complaint: %w", err)
	}
	payload := events.ComplaintPayload{
		ComplaintID: c.ID.String(),
		RideID:      rideID.String(),
		CustomerID:  customerID.String(),
		Category:    c.Category,
		Status:      c.Status,
		OccurredAt:  c.CreatedAt,
	}
	if c.PartnerID != nil {
		payload.PartnerID = c.PartnerID.String()
	}
	if perr := s.producer.PublishComplaintRaised(ctx, payload); perr != nil {
		slog.Warn("rider: publish complaint.raised failed", "complaint_id", c.ID, "error", perr)
	}
	return c, nil
}

// ListMyComplaints returns the customer's complaints, newest first.
func (s *Service) ListMyComplaints(ctx context.Context, customerID uuid.UUID, limit int) ([]store.Complaint, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: customer_id required")
	}
	return s.store.ListComplaintsByCustomer(ctx, customerID, limit)
}

// GetComplaint returns a complaint. The customer can read their own; an
// admin (passing isAdmin=true) can read any.
func (s *Service) GetComplaint(ctx context.Context, complaintID, viewerID uuid.UUID, isAdmin bool) (*store.Complaint, error) {
	c, err := s.store.GetComplaint(ctx, complaintID)
	if err != nil {
		if errors.Is(err, store.ErrComplaintNotFound) {
			return nil, fmt.Errorf("not_found: complaint")
		}
		return nil, err
	}
	if !isAdmin && c.CustomerID != viewerID {
		return nil, fmt.Errorf("forbidden: complaint does not belong to user")
	}
	return c, nil
}

// UpdateComplaintStatusRequest is the input shape for UpdateComplaintStatus.
type UpdateComplaintStatusRequest struct {
	Status string
	Note   string
}

// UpdateComplaintStatus is admin-only. Status moves are validated against
// the closed enum (open / under_review / resolved / dismissed). Emits
// EventRiderComplaintUpdated with the admin id.
func (s *Service) UpdateComplaintStatus(ctx context.Context, complaintID, adminID uuid.UUID, req UpdateComplaintStatusRequest) (*store.Complaint, error) {
	if adminID == uuid.Nil {
		return nil, fmt.Errorf("forbidden: admin id required")
	}
	if !store.IsValidComplaintStatus(req.Status) {
		return nil, fmt.Errorf("invalid: status must be one of open, under_review, resolved, dismissed")
	}
	var notePtr *string
	if n := strings.TrimSpace(req.Note); n != "" {
		notePtr = &n
	}
	c, err := s.store.UpdateComplaintStatus(ctx, store.UpdateComplaintStatusInput{
		ComplaintID: complaintID,
		Status:      req.Status,
		Note:        notePtr,
		ResolvedBy:  adminID,
	})
	if err != nil {
		if errors.Is(err, store.ErrComplaintNotFound) {
			return nil, fmt.Errorf("not_found: complaint")
		}
		return nil, err
	}
	payload := events.ComplaintPayload{
		ComplaintID: c.ID.String(),
		RideID:      c.RideID.String(),
		CustomerID:  c.CustomerID.String(),
		Category:    c.Category,
		Status:      c.Status,
		OccurredAt:  time.Now().UTC(),
	}
	if c.PartnerID != nil {
		payload.PartnerID = c.PartnerID.String()
	}
	if perr := s.producer.PublishComplaintUpdated(ctx, payload, adminID); perr != nil {
		slog.Warn("rider: publish complaint.updated failed", "complaint_id", c.ID, "error", perr)
	}
	return c, nil
}

// ListComplaintsForAdmin returns complaints filtered by status. Admin only.
func (s *Service) ListComplaintsForAdmin(ctx context.Context, status string, limit, offset int) ([]store.Complaint, error) {
	return s.store.ListComplaints(ctx, status, limit, offset)
}
