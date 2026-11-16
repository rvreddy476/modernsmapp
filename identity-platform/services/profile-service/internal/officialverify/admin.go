package officialverify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// The admin console's side of the two marks
// (docs/designs/official-marks-console-contract.md section 2): stats, the
// application queue and detail, the registration document's media id, the
// invitation list and the revoke. Review and create live in service.go.

// Page sizes.
const (
	DefaultPageSize = 20
	MaxPageSize     = 50
	maxReasonLen    = 500
	// StatsWindow is the "last 30 days" of the stats strip.
	StatsWindow = 30 * 24 * time.Hour
)

var (
	errInvalidStatus = refusal(http.StatusBadRequest, "INVALID_REQUEST", "Unknown status")
	errInvalidLimit  = refusal(http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1 to 50")
	errInvalidCursor = refusal(http.StatusBadRequest, "INVALID_REQUEST", "Invalid cursor")
)

// applicationStatuses are the statuses the console may list. superseded is
// a dead row nobody reviews.
var applicationStatuses = map[string]bool{
	store.OrgAppInReview: true, store.OrgAppApproved: true, store.OrgAppRejected: true,
	store.OrgAppRefused: true, store.OrgAppEmailPending: true,
}

var invitationStatuses = map[string]bool{
	store.InvitationPending: true, store.InvitationAccepted: true,
	store.InvitationRevoked: true, store.InvitationExpired: true,
}

// MarksStats is the console's stats strip.
type MarksStats struct {
	InReview           int `json:"in_review"`
	Approved30d        int `json:"approved_30d"`
	Rejected30d        int `json:"rejected_30d"`
	InvitationsPending int `json:"invitations_pending"`
	OfficialAccounts   int `json:"official_accounts"`
	GovernmentAccounts int `json:"government_accounts"`
}

// Applicant is the applicant as a queue row shows them.
type Applicant struct {
	Username          string `json:"username"`
	DisplayName       string `json:"display_name"`
	VerificationLevel string `json:"verification_level"`
	BadgeFlags        int    `json:"badge_flags"`
}

// ApplicantDetail adds the person-mark bit the detail needs (Approve is
// disabled without it).
type ApplicantDetail struct {
	Applicant
	IsPersonVerified bool `json:"is_person_verified"`
}

// ApplicationSummary is one queue row.
type ApplicationSummary struct {
	ApplicationID     uuid.UUID `json:"application_id"`
	UserID            uuid.UUID `json:"user_id"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	LegalName         string    `json:"legal_name"`
	RegistrationKind  string    `json:"registration_kind"`
	OfficialDomain    string    `json:"official_domain"`
	RegistryConfirmed bool      `json:"registry_confirmed"`
	HasDocument       bool      `json:"has_document"`
	Applicant         Applicant `json:"applicant"`
}

// Application is the detail: the summary plus the registration and review
// fields. Its Applicant shadows the summary's (Go's shallower-field rule), so
// the JSON carries one "applicant" object with is_person_verified.
type Application struct {
	ApplicationSummary
	RegistrationNumber string          `json:"registration_number"`
	SignatoryEmail     string          `json:"signatory_email"`
	RegistryName       string          `json:"registry_name"`
	RegistryProvider   string          `json:"registry_provider"`
	RefusalCode        string          `json:"refusal_code,omitempty"`
	ReviewerID         *uuid.UUID      `json:"reviewer_id,omitempty"`
	ReviewReason       string          `json:"review_reason,omitempty"`
	ReviewedAt         *time.Time      `json:"reviewed_at,omitempty"`
	Applicant          ApplicantDetail `json:"applicant"`
}

// ApplicationPage is one page of the queue.
type ApplicationPage struct {
	Items      []ApplicationSummary `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

// Invitee is the invited account.
type Invitee struct {
	Username          string `json:"username"`
	DisplayName       string `json:"display_name"`
	VerificationLevel string `json:"verification_level"`
}

// Invitation is a Government invitation as the console sees it. Never the
// code or its hash.
type Invitation struct {
	InvitationID uuid.UUID    `json:"invitation_id"`
	UserID       uuid.UUID    `json:"user_id"`
	OfficialName string       `json:"official_name"`
	Status       string       `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	ExpiresAt    time.Time    `json:"expires_at"`
	AcceptedAt   *time.Time   `json:"accepted_at,omitempty"`
	ApproverIDs  [2]uuid.UUID `json:"approver_ids"`
	Reason       string       `json:"reason"`
	Invitee      Invitee      `json:"invitee"`
}

// InvitationPage is one page of invitations.
type InvitationPage struct {
	Items      []Invitation `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

// ListQuery is a list request as the HTTP layer parsed it. Status "" and
// Limit 0 take the defaults.
type ListQuery struct {
	Status string
	Limit  int
	Cursor string
}

func toSummary(a *store.AdminOrgApplication) ApplicationSummary {
	return ApplicationSummary{
		ApplicationID: a.ID, UserID: a.UserID, Status: a.Status, CreatedAt: a.CreatedAt.UTC(),
		LegalName: a.LegalName, RegistrationKind: a.RegistrationKind, OfficialDomain: a.OfficialDomain,
		RegistryConfirmed: a.RegistryConfirmed, HasDocument: a.DocumentMediaID != nil,
		Applicant: Applicant{
			Username: a.Applicant.Username, DisplayName: a.Applicant.DisplayName,
			VerificationLevel: a.Applicant.VerificationLevel, BadgeFlags: a.Applicant.BadgeFlags,
		},
	}
}

func toApplication(a *store.AdminOrgApplication) *Application {
	sum := toSummary(a)
	out := &Application{
		ApplicationSummary: sum,
		RegistrationNumber: a.RegistrationNumber, SignatoryEmail: a.SignatoryEmail,
		RegistryName: a.RegistryName, RegistryProvider: a.RegistryProvider,
		RefusalCode: a.RefusalCode, ReviewerID: a.ReviewerID, ReviewReason: a.ReviewReason,
		Applicant: ApplicantDetail{Applicant: sum.Applicant, IsPersonVerified: a.Applicant.IsPersonVerified()},
	}
	// reviewed_at is when a reviewer closed it; an automatic approval or
	// refusal has no reviewer and no reviewed_at.
	if a.ReviewerID != nil && a.CompletedAt != nil {
		t := a.CompletedAt.UTC()
		out.ReviewedAt = &t
	}
	return out
}

func toInvitation(g *store.AdminGovInvitation) Invitation {
	out := Invitation{
		InvitationID: g.ID, UserID: g.UserID, OfficialName: g.OfficialName, Status: g.Status,
		CreatedAt: g.CreatedAt.UTC(), ExpiresAt: g.ExpiresAt.UTC(),
		ApproverIDs: [2]uuid.UUID{g.ApproverOne, g.ApproverTwo}, Reason: g.Reason,
		Invitee: Invitee{Username: g.Invitee.Username, DisplayName: g.Invitee.DisplayName, VerificationLevel: g.Invitee.VerificationLevel},
	}
	if g.AcceptedAt != nil {
		t := g.AcceptedAt.UTC()
		out.AcceptedAt = &t
	}
	return out
}

// cursor is the opaque position: the status it was issued for (a cursor
// from another status's list is refused, not silently misapplied), the last
// row's created_at and id.
type cursor struct {
	S  string    `json:"s"`
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"i"`
}

func encodeCursor(status string, createdAt time.Time, id uuid.UUID) *string {
	b, _ := json.Marshal(cursor{S: status, T: createdAt.UTC(), ID: id})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(raw, status string) (*store.Keyset, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 256 {
		return nil, errInvalidCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil || c.S != status || c.ID == uuid.Nil || c.T.IsZero() {
		return nil, errInvalidCursor
	}
	return &store.Keyset{CreatedAt: c.T, ID: c.ID}, nil
}

func pageQuery(q ListQuery, def string, allowed map[string]bool) (string, int, *store.Keyset, error) {
	status := q.Status
	if status == "" {
		status = def
	}
	if !allowed[status] {
		return "", 0, nil, errInvalidStatus
	}
	limit := q.Limit
	if limit == 0 {
		limit = DefaultPageSize
	}
	if limit < 1 || limit > MaxPageSize {
		return "", 0, nil, errInvalidLimit
	}
	after, err := decodeCursor(q.Cursor, status)
	if err != nil {
		return "", 0, nil, err
	}
	return status, limit, after, nil
}

// validReason trims a reason and accepts 1..500 runes.
func validReason(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < 1 || n > maxReasonLen {
		return "", false
	}
	return s, true
}

// Stats is the stats strip. Served while either mark is on.
func (s *Service) Stats(ctx context.Context) (*MarksStats, error) {
	if !s.OrgEnabled() && !s.GovEnabled() {
		return nil, ErrOrgDisabled
	}
	now := s.now().UTC()
	m, err := s.store.MarksStats(ctx, now.Add(-StatsWindow), now)
	if err != nil {
		return nil, err
	}
	return &MarksStats{
		InReview: m.InReview, Approved30d: m.ApprovedSince, Rejected30d: m.RejectedSince,
		InvitationsPending: m.InvitationsPending, OfficialAccounts: m.OfficialAccounts, GovernmentAccounts: m.GovernmentAccounts,
	}, nil
}

// ListApplications pages the queue: status default in_review (oldest first);
// any other status newest first.
func (s *Service) ListApplications(ctx context.Context, q ListQuery) (*ApplicationPage, error) {
	if !s.OrgEnabled() {
		return nil, ErrOrgDisabled
	}
	status, limit, after, err := pageQuery(q, store.OrgAppInReview, applicationStatuses)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListOrgApplicationsAdmin(ctx, status, limit, after)
	if err != nil {
		return nil, err
	}
	page := &ApplicationPage{Items: []ApplicationSummary{}}
	for i := range rows {
		if i == limit {
			last := rows[limit-1]
			page.NextCursor = encodeCursor(status, last.CreatedAt, last.ID)
			break
		}
		page.Items = append(page.Items, toSummary(&rows[i]))
	}
	return page, nil
}

// GetApplication is the detail.
func (s *Service) GetApplication(ctx context.Context, id uuid.UUID) (*Application, error) {
	if !s.OrgEnabled() {
		return nil, ErrOrgDisabled
	}
	a, err := s.store.GetOrgApplicationAdmin(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, errApplicationNotFound
	}
	return toApplication(a), nil
}

// ApplicationDocument is the registration document's media id, for
// admin-service to fetch the bytes from media-service.
func (s *Service) ApplicationDocument(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	if !s.OrgEnabled() {
		return uuid.Nil, ErrOrgDisabled
	}
	a, err := s.store.GetOrgApplication(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if a == nil {
		return uuid.Nil, errApplicationNotFound
	}
	if a.DocumentMediaID == nil || *a.DocumentMediaID == uuid.Nil {
		return uuid.Nil, errDocumentNotFound
	}
	return *a.DocumentMediaID, nil
}

// ListInvitations pages invitations: status default pending (unexpired),
// newest first.
func (s *Service) ListInvitations(ctx context.Context, q ListQuery) (*InvitationPage, error) {
	if !s.GovEnabled() {
		return nil, ErrGovDisabled
	}
	status, limit, after, err := pageQuery(q, store.InvitationPending, invitationStatuses)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListGovInvitationsAdmin(ctx, status, limit, after, s.now().UTC())
	if err != nil {
		return nil, err
	}
	page := &InvitationPage{Items: []Invitation{}}
	for i := range rows {
		if i == limit {
			last := rows[limit-1]
			page.NextCursor = encodeCursor(status, last.CreatedAt, last.ID)
			break
		}
		page.Items = append(page.Items, toInvitation(&rows[i]))
	}
	return page, nil
}

// RevokeInvitation closes a pending invitation. actor is the signed act
// claim; reason is required.
func (s *Service) RevokeInvitation(ctx context.Context, id, actor uuid.UUID, reason string) (*Invitation, error) {
	if !s.GovEnabled() {
		return nil, ErrGovDisabled
	}
	reason, ok := validReason(reason)
	if !ok {
		return nil, errReasonRequired
	}
	if actor == uuid.Nil {
		// Unreachable behind the HTTP guard, which requires act; a revoke is
		// never recorded against nobody.
		return nil, errors.New("officialverify: revoke needs the acting admin")
	}
	now := s.now().UTC()
	switch err := s.store.RevokeGovInvitation(ctx, id, actor, reason, now); {
	case errors.Is(err, store.ErrInvitationNotFound):
		return nil, errInvitationNotFound
	case errors.Is(err, store.ErrInvitationNotPending):
		return nil, errInvitationNotPend
	case err != nil:
		return nil, err
	}
	g, err := s.store.GetGovInvitationAdmin(ctx, id, now)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, errInvitationNotFound
	}
	s.log.Info("government invitation revoked", "user_id", g.UserID, "invitation_id", g.ID, "revoked_by", actor)
	out := toInvitation(g)
	return &out, nil
}
