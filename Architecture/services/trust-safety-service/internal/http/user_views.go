package http

// Reporter-safe views (audit TS-M1, 7 Nov 2026). A reporter, an appellant,
// a complainant and a verification applicant read their own cases through
// these projections, never the stored structs: the stored rows carry the
// handling staff member's user id (assigned_to, reviewed_by), the
// moderator's internal notes (resolution_notes, resolution_note), the
// provenance of the evidence snapshot (evidence.source) and post-service
// decision ids. Shown to the case's own user, a staff id is the first step
// to finding and harassing the moderator.
//
// Each view is an allowlist: a field reaches the user only when it is named
// here. The outcome is a fixed message per status, never the moderator's
// words. The admin-token routes and the legacy admin-scope reads keep the
// full structs.

import (
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// GrievanceOfficerRole is how a complainant sees who handles their
// grievance: the role the IT Rules name, never the officer's user id.
const GrievanceOfficerRole = "grievance_officer"

// userReport is a report as its reporter sees it.
type userReport struct {
	ID         uuid.UUID  `json:"id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	EntityKey  string     `json:"entity_key,omitempty"`
	Reason     string     `json:"reason"`
	Details    string     `json:"details"`
	Status     string     `json:"status"`
	Outcome    string     `json:"outcome"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// reportOutcomes is the user-facing message per report status.
var reportOutcomes = map[string]string{
	"open":      "We received your report and will review it.",
	"reviewing": "Your report is being reviewed.",
	"resolved":  "We reviewed your report and took action where our rules were broken.",
	"dismissed": "We reviewed your report and found no violation of our rules.",
}

const unknownOutcome = "We are looking into this."

func outcomeFor(messages map[string]string, status string) string {
	if m, ok := messages[status]; ok {
		return m
	}
	return unknownOutcome
}

func toUserReport(r postgres.Report) userReport {
	return userReport{
		ID: r.ID, ReporterID: r.ReporterID, EntityType: r.EntityType, EntityID: r.EntityID,
		EntityKey: r.EntityKey, Reason: r.Reason, Details: r.Details, Status: r.Status,
		Outcome: outcomeFor(reportOutcomes, r.Status), ResolvedAt: r.ResolvedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func toUserReports(in []postgres.Report) []userReport {
	out := make([]userReport, 0, len(in))
	for _, r := range in {
		out = append(out, toUserReport(r))
	}
	return out
}

// userAppeal is an appeal as its appellant sees it.
type userAppeal struct {
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	ContentType  string     `json:"content_type"`
	ContentID    uuid.UUID  `json:"content_id"`
	ActionTaken  string     `json:"action_taken"`
	AppealReason string     `json:"appeal_reason"`
	Status       string     `json:"status"`
	Outcome      string     `json:"outcome"`
	SubmittedAt  time.Time  `json:"submitted_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

// appealOutcomes is the user-facing message per appeal status.
var appealOutcomes = map[string]string{
	postgres.AppealOpen:        "We received your appeal and will review it.",
	postgres.AppealOverturning: "Your appeal was accepted. Your content is being restored.",
	postgres.AppealOverturned:  "Your appeal was accepted and your content was restored.",
	postgres.AppealUpheld:      "We reviewed your appeal and the original decision stands.",
	postgres.AppealSuperseded:  "A newer decision on this content replaced the one you appealed, so this appeal is closed.",
	postgres.AppealExpired:     "This appeal closed before it was reviewed.",
}

func toUserAppeal(a postgres.ContentAppeal) userAppeal {
	return userAppeal{
		ID: a.ID, UserID: a.UserID, ContentType: a.ContentType, ContentID: a.ContentID,
		ActionTaken: a.ActionTaken, AppealReason: a.AppealReason, Status: a.Status,
		Outcome: outcomeFor(appealOutcomes, a.Status), SubmittedAt: a.SubmittedAt, ResolvedAt: a.ResolvedAt,
	}
}

func toUserAppeals(in []postgres.ContentAppeal) []userAppeal {
	out := make([]userAppeal, 0, len(in))
	for _, a := range in {
		out = append(out, toUserAppeal(a))
	}
	return out
}

// userGrievance is a grievance as its complainant sees it. HandledBy is the
// role label once an officer holds it; the officer's id never leaves.
type userGrievance struct {
	ID              uuid.UUID  `json:"id"`
	ComplainantID   uuid.UUID  `json:"complainant_id"`
	Subject         string     `json:"subject"`
	AboutEntityType *string    `json:"about_entity_type,omitempty"`
	AboutEntityID   *uuid.UUID `json:"about_entity_id,omitempty"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Outcome         string     `json:"outcome"`
	HandledBy       string     `json:"handled_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	DueAt           time.Time  `json:"due_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// grievanceOutcomes is the user-facing message per grievance status.
var grievanceOutcomes = map[string]string{
	"open":         "We received your grievance. The grievance officer will acknowledge it and resolve it by the due date.",
	"acknowledged": "The grievance officer acknowledged your grievance and will resolve it by the due date.",
	"resolved":     "The grievance officer resolved your grievance.",
	"rejected":     "The grievance officer closed your grievance without action.",
}

func toUserGrievance(g postgres.Grievance) userGrievance {
	v := userGrievance{
		ID: g.ID, ComplainantID: g.ComplainantID, Subject: g.Subject,
		AboutEntityType: g.AboutEntityType, AboutEntityID: g.AboutEntityID,
		Description: g.Description, Status: g.Status, Outcome: outcomeFor(grievanceOutcomes, g.Status),
		CreatedAt: g.CreatedAt, AcknowledgedAt: g.AcknowledgedAt, ResolvedAt: g.ResolvedAt,
		DueAt: g.DueAt, UpdatedAt: g.UpdatedAt,
	}
	if g.AssignedTo != nil {
		v.HandledBy = GrievanceOfficerRole
	}
	return v
}

func toUserGrievances(in []postgres.Grievance) []userGrievance {
	out := make([]userGrievance, 0, len(in))
	for _, g := range in {
		out = append(out, toUserGrievance(g))
	}
	return out
}

// userVerification is a verification request as its applicant sees it
// (audit TS-L3). Reason is the reviewer's message to the applicant (why it
// was rejected, or what more is needed), which is written for them; the
// reviewer's id and the submitted documents are not returned.
type userVerification struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Outcome   string    `json:"outcome"`
	Reason    *string   `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var verificationOutcomes = map[string]string{
	"pending":          "Your verification request is waiting for review.",
	"approved":         "Your verification request was approved.",
	"rejected":         "Your verification request was not approved.",
	"more_info_needed": "We need more information to finish reviewing your verification request.",
}

func toUserVerification(r postgres.VerificationRequest) userVerification {
	v := userVerification{
		ID: r.ID, UserID: r.UserID, Type: r.Type, Status: r.Status,
		Outcome: outcomeFor(verificationOutcomes, r.Status), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if (r.Status == "rejected" || r.Status == "more_info_needed") && r.RejectionReason != nil && *r.RejectionReason != "" {
		reason := *r.RejectionReason
		v.Reason = &reason
	}
	return v
}
