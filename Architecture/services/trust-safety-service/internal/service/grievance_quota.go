package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit TS-L1 (7 Nov 2026): every grievance starts the IT Rules 15-day
// clock and lands in the officer's overdue queue, yet POST /v1/grievances
// had no bound. A complainant may lodge GrievanceLimit grievances per
// GrievanceWindow through the user-facing route, and only one open
// (open or acknowledged) grievance about the same item at a time.
//
// Both checks are preflight reads, as the reporter quota is
// (reporter_quota.go): two racing requests can each pass. That bounds
// volume, which is the point; an exact duplicate rule would need a partial
// unique index, and a racing pair is two grievances, not a flood.
//
// Admin-created grievances (the admin-token route) and the dating-service
// link (one grievance per dating report, bounded by dating's own report
// rules) do not go through here.
const (
	GrievanceLimit  = 5
	GrievanceWindow = 24 * time.Hour
)

var (
	// ErrGrievanceRateLimited: the complainant used their quota (429).
	ErrGrievanceRateLimited = errors.New("grievance rate limit exceeded")
	// ErrActiveGrievanceExists: the complainant already has an open
	// grievance about the same item (409).
	ErrActiveGrievanceExists = errors.New("an open grievance about this item already exists")
)

// grievanceVolume answers the two preflight reads (*postgres.ReportStore in
// production; a fake in tests).
type grievanceVolume interface {
	CountGrievancesByComplainantSince(ctx context.Context, complainantID uuid.UUID, since time.Time) (int, error)
	HasActiveGrievanceAbout(ctx context.Context, complainantID, aboutEntityID uuid.UUID) (bool, error)
}

// WithGrievanceVolume overrides the reads the grievance quota uses (tests).
func (s *Service) WithGrievanceVolume(v grievanceVolume) *Service {
	s.grievanceVolume = v
	return s
}

// FileUserGrievance is FileGrievance behind the complainant's quota and the
// one-open-grievance-per-item rule. A failed read refuses: an unanswered
// "how many?" is not a "few".
func (s *Service) FileUserGrievance(ctx context.Context, complainantID uuid.UUID, subject, aboutType, aboutID, description string) (*postgres.Grievance, error) {
	if !validGrievanceSubjects[subject] {
		return nil, fmt.Errorf("invalid subject: %s", subject)
	}
	var vol grievanceVolume = s.store
	if s.grievanceVolume != nil {
		vol = s.grievanceVolume
	}
	n, err := vol.CountGrievancesByComplainantSince(ctx, complainantID, time.Now().Add(-GrievanceWindow))
	if err != nil {
		return nil, fmt.Errorf("grievance quota: %w", err)
	}
	if n >= GrievanceLimit {
		return nil, ErrGrievanceRateLimited
	}
	if about, err := uuid.Parse(aboutID); err == nil && about != uuid.Nil {
		active, err := vol.HasActiveGrievanceAbout(ctx, complainantID, about)
		if err != nil {
			return nil, fmt.Errorf("grievance duplicate check: %w", err)
		}
		if active {
			return nil, ErrActiveGrievanceExists
		}
	}
	return s.FileGrievance(ctx, complainantID, subject, aboutType, aboutID, description)
}
