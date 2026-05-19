package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// grievanceResolutionDays is the IT Rules 2021 resolution SLA — a
// grievance's due_at is set this many days after it is lodged.
const grievanceResolutionDays = 15

var validGrievanceSubjects = map[string]bool{
	"content_complaint":     true,
	"privacy":               true,
	"account":               true,
	"intellectual_property": true,
	"other":                 true,
}

// validGrievanceTransitions is the grievance status state machine.
var validGrievanceTransitions = map[string][]string{
	"open":         {"acknowledged", "resolved", "rejected"},
	"acknowledged": {"resolved", "rejected"},
	"resolved":     {},
	"rejected":     {},
}

// FileGrievance lodges a new grievance and stamps its resolution deadline.
func (s *Service) FileGrievance(ctx context.Context, complainantID uuid.UUID, subject, aboutType, aboutID, description string) (*postgres.Grievance, error) {
	if !validGrievanceSubjects[subject] {
		return nil, fmt.Errorf("invalid subject: %s", subject)
	}
	if strings.TrimSpace(description) == "" {
		return nil, fmt.Errorf("description is required")
	}

	g := &postgres.Grievance{
		ID:            uuid.New(),
		ComplainantID: complainantID,
		Subject:       subject,
		Description:   strings.TrimSpace(description),
		Status:        "open",
		DueAt:         time.Now().AddDate(0, 0, grievanceResolutionDays),
	}
	if aboutType != "" {
		g.AboutEntityType = &aboutType
	}
	if aboutID != "" {
		if id, err := uuid.Parse(aboutID); err == nil {
			g.AboutEntityID = &id
		}
	}

	if err := s.store.CreateGrievance(ctx, g); err != nil {
		return nil, err
	}
	return s.store.GetGrievance(ctx, g.ID)
}

// GetGrievance fetches a single grievance.
func (s *Service) GetGrievance(ctx context.Context, id uuid.UUID) (*postgres.Grievance, error) {
	return s.store.GetGrievance(ctx, id)
}

// ListGrievances returns the officer queue, optionally filtered by status.
func (s *Service) ListGrievances(ctx context.Context, status string, limit, offset int) ([]postgres.Grievance, error) {
	return s.store.ListGrievances(ctx, status, limit, offset)
}

// ListMyGrievances returns the grievances a given user has filed.
func (s *Service) ListMyGrievances(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Grievance, error) {
	return s.store.ListGrievancesByUser(ctx, userID, limit, offset)
}

// UpdateGrievance applies an officer's verdict, enforcing the status
// state machine.
func (s *Service) UpdateGrievance(ctx context.Context, id uuid.UUID, newStatus, notes string, assignedTo *uuid.UUID) (*postgres.Grievance, error) {
	current, err := s.store.GetGrievance(ctx, id)
	if err != nil {
		return nil, err
	}
	allowed := validGrievanceTransitions[current.Status]
	ok := false
	for _, a := range allowed {
		if a == newStatus {
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("invalid status transition: %s -> %s", current.Status, newStatus)
	}
	return s.store.UpdateGrievance(ctx, id, newStatus, notes, assignedTo)
}
