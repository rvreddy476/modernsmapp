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

// ListOverdueGrievances is the 15-day breach queue: unresolved grievances
// past due_at, most overdue first.
func (s *Service) ListOverdueGrievances(ctx context.Context, limit, offset int) ([]postgres.Grievance, error) {
	return s.store.ListOverdueGrievances(ctx, limit, offset)
}

// GrievanceHistory returns every audited change to a grievance, oldest
// first: who filed or changed it, status, officer hand-overs and notes.
func (s *Service) GrievanceHistory(ctx context.Context, id uuid.UUID) ([]postgres.AuditEntry, error) {
	return s.store.ListAudit(ctx, postgres.AuditTargetGrievance, id)
}

// GrievanceChange is one officer change: a new status, new notes, a new
// assigned officer, or any combination. Empty/nil fields are kept.
type GrievanceChange struct {
	Status     string
	Notes      *string
	AssignedTo *uuid.UUID
}

// UpdateGrievance applies an officer's change, enforcing the status state
// machine. The update and its audit row share one transaction.
func (s *Service) UpdateGrievance(ctx context.Context, id uuid.UUID, change GrievanceChange, meta postgres.AuditMeta) (*postgres.Grievance, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, err
	}
	if change.Status != "" {
		if _, known := validGrievanceTransitions[change.Status]; !known {
			return nil, fmt.Errorf("invalid status: %s", change.Status)
		}
	}
	return s.store.UpdateGrievance(ctx, id, postgres.GrievanceUpdate{
		Status:     change.Status,
		Notes:      change.Notes,
		AssignedTo: change.AssignedTo,
		Allow: func(from, to string) bool {
			for _, a := range validGrievanceTransitions[from] {
				if a == to {
					return true
				}
			}
			return false
		},
	}, meta)
}
