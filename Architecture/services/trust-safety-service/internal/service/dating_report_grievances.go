package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrInvalidDatingReport marks a dating report link request that can never
// succeed (missing ids, bad reason). The handler answers 400.
var ErrInvalidDatingReport = errors.New("invalid dating report grievance request")

// DatingReportGrievanceInput is one dating report handed over by
// dating-service.
type DatingReportGrievanceInput struct {
	ReportID   uuid.UUID
	ReporterID uuid.UUID
	TargetID   uuid.UUID
	Reason     string
	Details    string
	ReportedAt time.Time
}

// maxDatingReportDetails bounds the description copied from the report
// (dating caps details at 500 characters; this leaves headroom).
const maxDatingReportDetails = 2000

// datingReportSubject maps a dating reason code to a grievance subject:
// identity problems are account complaints, everything else concerns
// behaviour or content.
func datingReportSubject(reason string) string {
	switch reason {
	case "fake_profile", "underage":
		return "account"
	default:
		return "content_complaint"
	}
}

// buildDatingReportGrievance validates the input and builds the grievance.
// due_at runs from when the user reported, not when the link landed, so a
// trust-safety outage never extends the 15-day resolution window.
func buildDatingReportGrievance(in DatingReportGrievanceInput, now time.Time) (*postgres.Grievance, error) {
	if in.ReportID == uuid.Nil || in.ReporterID == uuid.Nil || in.TargetID == uuid.Nil {
		return nil, fmt.Errorf("%w: report_id, reporter_id and target_id are required", ErrInvalidDatingReport)
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len(reason) > 64 {
		return nil, fmt.Errorf("%w: reason is required (at most 64 characters)", ErrInvalidDatingReport)
	}
	details := strings.TrimSpace(in.Details)
	if utf8.RuneCountInString(details) > maxDatingReportDetails {
		return nil, fmt.Errorf("%w: details must be at most %d characters", ErrInvalidDatingReport, maxDatingReportDetails)
	}
	reportedAt := in.ReportedAt
	if reportedAt.IsZero() || reportedAt.After(now.Add(5*time.Minute)) {
		reportedAt = now
	}
	description := fmt.Sprintf("Dating report %s (reason: %s) against user %s.", in.ReportID, reason, in.TargetID)
	if details != "" {
		description += "\n\n" + details
	}
	return &postgres.Grievance{
		ID:            uuid.New(),
		ComplainantID: in.ReporterID,
		Subject:       datingReportSubject(reason),
		Description:   description,
		Status:        "open",
		DueAt:         reportedAt.AddDate(0, 0, grievanceResolutionDays),
	}, nil
}

// DatingServiceActor is the audit actor for grievances dating-service opens
// through the service-only link route. No human is involved, so the change
// is recorded as this named service.
const DatingServiceActor = "dating-service"

// LinkDatingReportGrievance opens (or returns the existing) grievance for a
// dating report. Idempotent on the report id. A new grievance is audited as
// created by dating-service in the same transaction.
func (s *Service) LinkDatingReportGrievance(ctx context.Context, in DatingReportGrievanceInput, requestID string) (*postgres.Grievance, bool, error) {
	g, err := buildDatingReportGrievance(in, time.Now())
	if err != nil {
		return nil, false, err
	}
	return s.store.UpsertDatingReportGrievance(ctx, g, in.ReportID, postgres.AuditMeta{
		Actor:     postgres.ServiceActor(DatingServiceActor),
		Reason:    "dating report " + in.ReportID.String(),
		RequestID: requestID,
	})
}
