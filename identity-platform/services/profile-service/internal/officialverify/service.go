// Package officialverify is the Official (organisation) and Government
// verification marks (docs/designs/short-posts-design.md section 3.4).
//
// Official, applied for by the account itself:
//
//	submit   → the applicant already holds the person mark (they are the
//	           authorised signatory); the GSTIN or CIN is well formed and a
//	           registry provider confirms it is active under the claimed
//	           legal name; the official domain is not a free-mail provider
//	           and the signatory address is on exactly that domain. A code
//	           is sent to the signatory address.
//	confirm  → the code proves the domain. Every check machine-verified:
//	           the mark is set automatically (founder rule 19 Sep). An
//	           application that carried a hand-uploaded document instead of a
//	           registry confirmation goes to in_review, the single human
//	           fallback, decided in the admin console (Review).
//
// Government, invitation-only: the admin console creates an invitation for a
// user with two distinct approvers (two-person rule: the admin who asked and
// the admin who approved); the invited user accepts it with the code.
//
// The console reaches this service through admin-service's signed token
// (internal/http/official_verification_admin.go). The token's scope is the
// authority: admin-service checked the permission against identity before it
// signed, so this package no longer asks auth-service for an admin role. The
// rules it keeps are about the decision itself: a reason, a reviewer who is
// not the applicant, two distinct approvers neither of whom is the invitee.
//
// Never logged: registration numbers, signatory addresses, codes and code
// hashes. Log lines carry user, application and invitation ids and a code.
package officialverify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/identity-profile-service/internal/store"
	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

// Timing and limits.
const (
	// CodeTTL is how long a domain code is valid.
	CodeTTL = 15 * time.Minute
	// MaxCodeAttempts ends an application after this many wrong codes.
	MaxCodeAttempts = 5
	// MaxApplicationsPerDay caps submissions per user per rolling 24 hours.
	MaxApplicationsPerDay = 3
	// InvitationTTL is how long a Government invitation can be accepted.
	InvitationTTL = 7 * 24 * time.Hour
	maxNameLen    = 200
)

// Error is a refusal the HTTP layer answers with Status and Code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code }

func refusal(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// Refusal codes.
var (
	ErrOrgDisabled = refusal(http.StatusServiceUnavailable, "ORG_VERIFICATION_DISABLED", "Official verification is not available yet")
	ErrGovDisabled = refusal(http.StatusServiceUnavailable, "GOVERNMENT_VERIFICATION_DISABLED", "Government verification is not available yet")

	errProfileNotFound     = refusal(http.StatusNotFound, "PROFILE_NOT_FOUND", "Profile not found")
	errPersonMarkRequired  = refusal(http.StatusForbidden, "PERSON_VERIFICATION_REQUIRED", "Verify yourself first: the applicant must be a verified person")
	errAlreadyOfficial     = refusal(http.StatusConflict, "ALREADY_OFFICIAL", "This account already holds an Official or Government mark")
	errRegistrationInUse   = refusal(http.StatusConflict, "REGISTRATION_IN_USE", "Another account already holds the Official mark for this registration")
	errInReview            = refusal(http.StatusConflict, "APPLICATION_IN_REVIEW", "An application is already being reviewed")
	errTooManyApplications = refusal(http.StatusTooManyRequests, "TOO_MANY_APPLICATIONS", "Too many applications today; try again tomorrow")
	errInvalidLegalName    = refusal(http.StatusBadRequest, "INVALID_LEGAL_NAME", "Enter the organisation's registered legal name")
	errRegistrationChoice  = refusal(http.StatusBadRequest, "REGISTRATION_REQUIRED", "Give exactly one of gstin or cin")
	errInvalidGSTIN        = refusal(http.StatusBadRequest, "INVALID_GSTIN", "This GSTIN is not valid")
	errInvalidCIN          = refusal(http.StatusBadRequest, "INVALID_CIN", "This CIN is not valid")
	errInvalidDomain       = refusal(http.StatusBadRequest, "INVALID_DOMAIN", "Enter the organisation's own domain, for example example.com")
	errInvalidEmail        = refusal(http.StatusBadRequest, "INVALID_EMAIL", "Enter the signatory's e-mail address")
	errFreeMailDomain      = refusal(http.StatusUnprocessableEntity, "FREE_MAIL_DOMAIN", "A free e-mail provider cannot prove an organisation's domain")
	errDomainMismatch      = refusal(http.StatusUnprocessableEntity, "DOMAIN_MISMATCH", "The signatory's e-mail address must be on the official domain")
	errRegistryUnavailable = refusal(http.StatusServiceUnavailable, "REGISTRY_UNAVAILABLE", "The registry could not be reached; try again shortly")
	errRegistryNotFound    = refusal(http.StatusUnprocessableEntity, "REGISTRATION_NOT_FOUND", "The registry has no active registration with this number")
	errLegalNameMismatch   = refusal(http.StatusUnprocessableEntity, "LEGAL_NAME_MISMATCH", "The legal name does not match the registry")
	errEmailUnavailable    = refusal(http.StatusServiceUnavailable, "EMAIL_UNAVAILABLE", "The code could not be sent; try again shortly")
	errNoPendingCode       = refusal(http.StatusConflict, "NO_PENDING_APPLICATION", "No application is waiting for a code; submit again")
	errCodeMismatch        = refusal(http.StatusUnprocessableEntity, "CODE_MISMATCH", "That code is not right")
	errTooManyCodeAttempts = refusal(http.StatusTooManyRequests, "TOO_MANY_CODE_ATTEMPTS", "Too many wrong codes; submit the application again")
	errApplicationNotFound = refusal(http.StatusNotFound, "APPLICATION_NOT_FOUND", "Application not found")
	errNotInReview         = refusal(http.StatusConflict, "APPLICATION_NOT_IN_REVIEW", "This application is not waiting for review")
	errInvalidDecision     = refusal(http.StatusBadRequest, "INVALID_DECISION", "decision must be approve or reject")
	errReasonRequired      = refusal(http.StatusBadRequest, "REASON_REQUIRED", "Give a reason of 1 to 500 characters")
	errReviewerIsApplicant = refusal(http.StatusForbidden, "REVIEWER_IS_APPLICANT", "An applicant cannot review their own application")
	errReviewPersonMark    = refusal(http.StatusUnprocessableEntity, "PERSON_MARK_REQUIRED", "The applicant is no longer a verified person; the application was refused")
	errDocumentNotFound    = refusal(http.StatusNotFound, "DOCUMENT_NOT_FOUND", "This application has no registration document")
	errTwoApprovers        = refusal(http.StatusBadRequest, "TWO_APPROVERS_REQUIRED", "Two distinct approvers are required")
	errApproverIsInvitee   = refusal(http.StatusForbidden, "APPROVER_IS_INVITEE", "An approver cannot invite themselves")
	errInvitationNotFound  = refusal(http.StatusNotFound, "INVITATION_NOT_FOUND", "Invitation not found")
	errInvitationNotPend   = refusal(http.StatusConflict, "INVITATION_NOT_PENDING", "This invitation is no longer pending")
	errInvalidOfficialName = refusal(http.StatusBadRequest, "INVALID_OFFICIAL_NAME", "Enter the government body's official name")
	errAlreadyGovernment   = refusal(http.StatusConflict, "ALREADY_GOVERNMENT", "This account already holds the Government mark")
	errInvalidInvitation   = refusal(http.StatusBadRequest, "INVALID_INVITATION", "This invitation code is invalid or has expired")
)

// Store is the storage the service needs (*store.Store in production).
type Store interface {
	GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error)
	CountOrgApplicationsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	RegistrationHeldByOther(ctx context.Context, userID uuid.UUID, kind, number string) (bool, error)
	CreateOrgApplication(ctx context.Context, a store.OrgApplication) error
	LatestOrgApplication(ctx context.Context, userID uuid.UUID) (*store.OrgApplication, error)
	GetOrgApplication(ctx context.Context, id uuid.UUID) (*store.OrgApplication, error)
	CountOrgCodeAttempt(ctx context.Context, id uuid.UUID, now time.Time) (*int, error)
	RefuseOrgApplication(ctx context.Context, id uuid.UUID, code string, now time.Time) error
	MoveOrgApplicationToReview(ctx context.Context, id uuid.UUID, now time.Time) error
	RejectOrgApplication(ctx context.Context, id, reviewerID uuid.UUID, reason string, now time.Time) error
	ApproveOrganisation(ctx context.Context, in store.ApproveOrganisationInput) (*store.Profile, error)
	CreateGovInvitation(ctx context.Context, g store.GovInvitation) error
	AcceptGovInvitation(ctx context.Context, userID uuid.UUID, codeHash string, now time.Time) (*store.Profile, error)
	// Admin console (admin.go).
	ListOrgApplicationsAdmin(ctx context.Context, status string, limit int, after *store.Keyset) ([]store.AdminOrgApplication, error)
	GetOrgApplicationAdmin(ctx context.Context, id uuid.UUID) (*store.AdminOrgApplication, error)
	MarksStats(ctx context.Context, since, now time.Time) (*store.MarksStats, error)
	ListGovInvitationsAdmin(ctx context.Context, status string, limit int, after *store.Keyset, now time.Time) ([]store.AdminGovInvitation, error)
	GetGovInvitationAdmin(ctx context.Context, id uuid.UUID, now time.Time) (*store.AdminGovInvitation, error)
	RevokeGovInvitation(ctx context.Context, id, revokedBy uuid.UUID, reason string, now time.Time) error
}

// Publisher emits identity.verification.changed (*events.Producer).
type Publisher interface {
	PublishVerificationChanged(ctx context.Context, payload sharedEvents.IdentityVerificationChangedPayload) error
}

// Service runs both marks.
type Service struct {
	cfg        Config
	store      Store
	pub        Publisher
	invalidate func(ctx context.Context, p *store.Profile)
	now        func() time.Time
	log        *slog.Logger
}

// New builds the service. pub and invalidate may be nil.
func New(cfg Config, st Store, pub Publisher, invalidate func(context.Context, *store.Profile), logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, store: st, pub: pub, invalidate: invalidate, now: time.Now, log: logger}
}

// WithClock replaces the clock. For tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// OrgEnabled and GovEnabled report whether each mark serves.
func (s *Service) OrgEnabled() bool { return s != nil && s.cfg.OrgEnabled }
func (s *Service) GovEnabled() bool { return s != nil && s.cfg.GovEnabled }

// OrgApplicationRequest is the applicant's submission.
type OrgApplicationRequest struct {
	LegalName      string `json:"legal_name"`
	GSTIN          string `json:"gstin"`
	CIN            string `json:"cin"`
	OfficialDomain string `json:"official_domain"`
	SignatoryEmail string `json:"signatory_email"`
	// DocumentMediaID is a hand-uploaded registration document. With it the
	// registry is not asked and a proven domain goes to human review.
	DocumentMediaID *uuid.UUID `json:"document_media_id,omitempty"`
}

// OrgStatus is the applicant's view of their newest application.
type OrgStatus struct {
	IsVerified        bool       `json:"is_verified"`
	VerificationLevel string     `json:"verification_level"`
	BadgeFlags        int        `json:"badge_flags"`
	OrganisationName  string     `json:"organisation_name"`
	ApplicationID     *uuid.UUID `json:"application_id,omitempty"`
	Status            string     `json:"status,omitempty"`
	RefusalCode       string     `json:"refusal_code,omitempty"`
	CodeExpiresAt     *time.Time `json:"code_expires_at,omitempty"`
	// Next is the step the client shows: "apply", "email_code", "review" or
	// "" (done).
	Next string `json:"next"`
}

func isMarkedOfficial(p *store.Profile) bool {
	return p.IsVerified && (p.VerificationLevel == verification.LevelOrganisation || p.VerificationLevel == verification.LevelGovernment)
}

// validated is a submission that passed the format checks.
type validated struct {
	legalName, kind, number, domain, email string
}

func (s *Service) validate(req OrgApplicationRequest) (*validated, error) {
	v := &validated{legalName: strings.TrimSpace(req.LegalName)}
	if n := utf8.RuneCountInString(v.legalName); n < 2 || n > maxNameLen || verification.NormaliseOrgName(v.legalName) == "" {
		return nil, errInvalidLegalName
	}
	gstin, cin := strings.TrimSpace(req.GSTIN), strings.TrimSpace(req.CIN)
	switch {
	case (gstin == "") == (cin == ""):
		return nil, errRegistrationChoice
	case gstin != "":
		n, err := verification.ValidateGSTIN(gstin)
		if err != nil {
			return nil, errInvalidGSTIN
		}
		v.kind, v.number = verification.RegistrationGSTIN, n
	default:
		n, err := verification.ValidateCIN(cin, s.now().Year())
		if err != nil {
			return nil, errInvalidCIN
		}
		v.kind, v.number = verification.RegistrationCIN, n
	}
	domain, err := verification.NormaliseDomain(req.OfficialDomain)
	if err != nil {
		return nil, errInvalidDomain
	}
	if verification.IsFreeMailDomain(domain) {
		return nil, errFreeMailDomain
	}
	local, emailDomain, err := verification.SplitEmail(req.SignatoryEmail)
	if err != nil {
		return nil, errInvalidEmail
	}
	if emailDomain != domain {
		return nil, errDomainMismatch
	}
	v.domain, v.email = domain, local+"@"+emailDomain
	return v, nil
}

// SubmitOrganisation checks a submission and sends the domain code.
func (s *Service) SubmitOrganisation(ctx context.Context, userID uuid.UUID, req OrgApplicationRequest) (*OrgStatus, error) {
	if !s.OrgEnabled() {
		return nil, ErrOrgDisabled
	}
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProfileNotFound
	}
	if isMarkedOfficial(p) {
		return nil, errAlreadyOfficial
	}
	if !store.HoldsPersonMark(p.IsVerified, p.VerificationLevel, p.BadgeFlags) {
		return nil, errPersonMarkRequired
	}
	v, err := s.validate(req)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if latest, err := s.store.LatestOrgApplication(ctx, userID); err != nil {
		return nil, err
	} else if latest != nil && latest.Status == store.OrgAppInReview {
		return nil, errInReview
	}
	if held, err := s.store.RegistrationHeldByOther(ctx, userID, v.kind, v.number); err != nil {
		return nil, err
	} else if held {
		return nil, errRegistrationInUse
	}
	n, err := s.store.CountOrgApplicationsSince(ctx, userID, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	if n >= MaxApplicationsPerDay {
		return nil, errTooManyApplications
	}

	app := store.OrgApplication{
		ID: uuid.New(), UserID: userID, LegalName: v.legalName, RegistrationKind: v.kind, RegistrationNumber: v.number,
		OfficialDomain: v.domain, SignatoryEmail: v.email, DocumentMediaID: req.DocumentMediaID, CreatedAt: now,
	}
	if req.DocumentMediaID == nil {
		rec, err := s.cfg.Registry.Lookup(ctx, RegistryQuery{Kind: v.kind, Number: v.number, ClaimedName: v.legalName})
		switch {
		case err != nil || rec == nil:
			s.log.Warn("organisation verification registry unavailable", "user_id", userID)
			return nil, errRegistryUnavailable
		case !rec.Found || !rec.Active:
			return nil, errRegistryNotFound
		case !verification.OrgNamesMatch(v.legalName, rec.LegalName):
			return nil, errLegalNameMismatch
		}
		app.RegistryConfirmed, app.RegistryName, app.RegistryProvider = true, strings.TrimSpace(rec.LegalName), rec.Provider
	}

	code, err := s.newCode()
	if err != nil {
		return nil, err
	}
	app.EmailCodeHash = codeHash(app.ID, code)
	app.EmailCodeExpiresAt = now.Add(CodeTTL)
	if err := s.store.CreateOrgApplication(ctx, app); err != nil {
		return nil, err
	}
	if err := s.cfg.Mailer.SendDomainCode(ctx, app.SignatoryEmail, app.LegalName, code, CodeTTL); err != nil {
		s.log.Warn("organisation verification code delivery failed", "user_id", userID, "application_id", app.ID)
		_ = s.store.RefuseOrgApplication(ctx, app.ID, errEmailUnavailable.Code, now)
		return nil, errEmailUnavailable
	}
	s.log.Info("organisation verification submitted", "user_id", userID, "application_id", app.ID,
		"registry_confirmed", app.RegistryConfirmed)
	exp := app.EmailCodeExpiresAt
	return &OrgStatus{
		IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags,
		ApplicationID: &app.ID, Status: store.OrgAppEmailPending, CodeExpiresAt: &exp, Next: "email_code",
	}, nil
}

// ConfirmOrganisationEmail checks the domain code. A registry-confirmed
// application is approved on the spot; a document one goes to review.
func (s *Service) ConfirmOrganisationEmail(ctx context.Context, userID uuid.UUID, code string) (*OrgStatus, error) {
	if !s.OrgEnabled() {
		return nil, ErrOrgDisabled
	}
	now := s.now().UTC()
	app, err := s.store.LatestOrgApplication(ctx, userID)
	if err != nil {
		return nil, err
	}
	if app == nil || app.Status != store.OrgAppEmailPending || !app.EmailCodeExpiresAt.After(now) {
		return nil, errNoPendingCode
	}
	attempts, err := s.store.CountOrgCodeAttempt(ctx, app.ID, now)
	if err != nil {
		return nil, err
	}
	if attempts == nil {
		return nil, errNoPendingCode
	}
	if *attempts > MaxCodeAttempts {
		return nil, s.refuse(ctx, app, errTooManyCodeAttempts)
	}
	want := codeHash(app.ID, strings.TrimSpace(code))
	if subtle.ConstantTimeCompare([]byte(want), []byte(app.EmailCodeHash)) != 1 {
		if *attempts >= MaxCodeAttempts {
			return nil, s.refuse(ctx, app, errTooManyCodeAttempts)
		}
		return nil, errCodeMismatch
	}

	if !app.RegistryConfirmed {
		if err := s.store.MoveOrgApplicationToReview(ctx, app.ID, now); err != nil {
			if errors.Is(err, store.ErrApplicationGone) {
				return nil, errNoPendingCode
			}
			return nil, err
		}
		s.log.Info("organisation verification sent to review", "user_id", userID, "application_id", app.ID)
		return s.OrganisationStatus(ctx, userID)
	}
	p, err := s.store.ApproveOrganisation(ctx, store.ApproveOrganisationInput{
		ApplicationID: app.ID, UserID: userID, FromStatus: store.OrgAppEmailPending,
		OrganisationName: app.RegistryName, Now: now,
	})
	switch {
	case errors.Is(err, store.ErrPersonMarkRequired):
		return nil, s.refuse(ctx, app, errPersonMarkRequired)
	case errors.Is(err, store.ErrApplicationGone):
		return nil, errNoPendingCode
	case err != nil:
		return nil, err
	}
	s.log.Info("organisation verification approved", "user_id", userID, "application_id", app.ID)
	s.changed(ctx, p, verification.ReasonOrganisationVerified)
	return s.OrganisationStatus(ctx, userID)
}

// OrganisationStatus is the applicant's mark and newest application.
func (s *Service) OrganisationStatus(ctx context.Context, userID uuid.UUID) (*OrgStatus, error) {
	if !s.OrgEnabled() {
		return nil, ErrOrgDisabled
	}
	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProfileNotFound
	}
	out := &OrgStatus{IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags}
	if isMarkedOfficial(p) {
		out.OrganisationName = p.OrganisationName
	}
	app, err := s.store.LatestOrgApplication(ctx, userID)
	if err != nil {
		return nil, err
	}
	if app != nil {
		out.ApplicationID, out.Status, out.RefusalCode = &app.ID, app.Status, app.RefusalCode
		if app.Status == store.OrgAppEmailPending {
			exp := app.EmailCodeExpiresAt
			out.CodeExpiresAt = &exp
		}
	}
	switch {
	case isMarkedOfficial(p):
		out.Next = ""
	case app != nil && app.Status == store.OrgAppEmailPending && app.EmailCodeExpiresAt.After(s.now()):
		out.Next = "email_code"
	case app != nil && app.Status == store.OrgAppInReview:
		out.Next = "review"
	default:
		out.Next = "apply"
	}
	return out, nil
}

// ReviewDecision is a reviewer's answer on an application in review. The
// reviewer is the signed act claim of the admin-service token, never a body
// field.
type ReviewDecision struct {
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	ReviewerID uuid.UUID `json:"-"`
}

// ReviewOrganisation applies a human decision to an in_review application and
// returns the application after it. The reviewer must not be the applicant
// and must give a reason. (Whether the reviewer may review at all is the
// token's scope, checked by the HTTP guard.)
func (s *Service) ReviewOrganisation(ctx context.Context, applicationID uuid.UUID, d ReviewDecision) (*Application, error) {
	if !s.OrgEnabled() {
		return nil, ErrOrgDisabled
	}
	if d.Decision != "approve" && d.Decision != "reject" {
		return nil, errInvalidDecision
	}
	reason, ok := validReason(d.Reason)
	if !ok {
		return nil, errReasonRequired
	}
	app, err := s.store.GetOrgApplication(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, errApplicationNotFound
	}
	if app.Status != store.OrgAppInReview {
		return nil, errNotInReview
	}
	if d.ReviewerID == uuid.Nil || d.ReviewerID == app.UserID {
		return nil, errReviewerIsApplicant
	}
	now := s.now().UTC()
	if d.Decision == "reject" {
		if err := s.store.RejectOrgApplication(ctx, app.ID, d.ReviewerID, reason, now); err != nil {
			if errors.Is(err, store.ErrApplicationGone) {
				return nil, errNotInReview
			}
			return nil, err
		}
		s.log.Info("organisation verification rejected in review", "user_id", app.UserID, "application_id", app.ID, "reviewer_id", d.ReviewerID)
		return s.GetApplication(ctx, app.ID)
	}
	reviewer := d.ReviewerID
	p, err := s.store.ApproveOrganisation(ctx, store.ApproveOrganisationInput{
		ApplicationID: app.ID, UserID: app.UserID, FromStatus: store.OrgAppInReview,
		OrganisationName: app.LegalName, ReviewerID: &reviewer, ReviewReason: reason, Now: now,
	})
	switch {
	case errors.Is(err, store.ErrPersonMarkRequired):
		return nil, s.refuse(ctx, app, errReviewPersonMark)
	case errors.Is(err, store.ErrApplicationGone):
		return nil, errNotInReview
	case err != nil:
		return nil, err
	}
	s.log.Info("organisation verification approved in review", "user_id", app.UserID, "application_id", app.ID, "reviewer_id", d.ReviewerID)
	s.changed(ctx, p, verification.ReasonOrganisationVerified)
	return s.GetApplication(ctx, app.ID)
}

// GovInvitationRequest creates a Government invitation. RequestedBy is the
// admin who asked (admin-service's two-person requester); ApproverID is the
// admin who approved, the signed act claim, never a body field. The two are
// the invitation's approvers.
type GovInvitationRequest struct {
	UserID       uuid.UUID `json:"user_id"`
	OfficialName string    `json:"official_name"`
	Reason       string    `json:"reason"`
	RequestedBy  uuid.UUID `json:"requested_by"`
	ApproverID   uuid.UUID `json:"-"`
}

// GovInvitationResult carries the code, returned once, to be handed to the
// invited body out of band.
type GovInvitationResult struct {
	InvitationID uuid.UUID `json:"invitation_id"`
	InviteCode   string    `json:"invite_code"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// CreateGovInvitation records an invitation approved by two distinct admins:
// approver_ids = [requested_by, act].
func (s *Service) CreateGovInvitation(ctx context.Context, req GovInvitationRequest) (*GovInvitationResult, error) {
	if !s.GovEnabled() {
		return nil, ErrGovDisabled
	}
	approvers := [2]uuid.UUID{req.RequestedBy, req.ApproverID}
	if approvers[0] == uuid.Nil || approvers[1] == uuid.Nil || approvers[0] == approvers[1] {
		return nil, errTwoApprovers
	}
	for _, a := range approvers {
		if a == req.UserID {
			return nil, errApproverIsInvitee
		}
	}
	name := strings.TrimSpace(req.OfficialName)
	if n := utf8.RuneCountInString(name); n < 2 || n > maxNameLen || verification.NormaliseOrgName(name) == "" {
		return nil, errInvalidOfficialName
	}
	reason, ok := validReason(req.Reason)
	if !ok {
		return nil, errReasonRequired
	}
	p, err := s.store.GetProfile(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProfileNotFound
	}
	if p.IsVerified && p.VerificationLevel == verification.LevelGovernment {
		return nil, errAlreadyGovernment
	}
	code, err := newInviteCode()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	g := store.GovInvitation{
		ID: uuid.New(), UserID: req.UserID, OfficialName: name, CodeHash: inviteHash(code),
		ApproverOne: approvers[0], ApproverTwo: approvers[1], Reason: reason,
		ExpiresAt: now.Add(InvitationTTL), CreatedAt: now,
	}
	if err := s.store.CreateGovInvitation(ctx, g); err != nil {
		return nil, err
	}
	s.log.Info("government invitation created", "user_id", req.UserID, "invitation_id", g.ID,
		"approver_one", g.ApproverOne, "approver_two", g.ApproverTwo)
	return &GovInvitationResult{InvitationID: g.ID, InviteCode: code, ExpiresAt: g.ExpiresAt}, nil
}

// AcceptGovInvitation sets the Government mark for the invited account.
func (s *Service) AcceptGovInvitation(ctx context.Context, userID uuid.UUID, code string) (*OrgStatus, error) {
	if !s.GovEnabled() {
		return nil, ErrGovDisabled
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if !validInviteCode(code) {
		return nil, errInvalidInvitation
	}
	p, err := s.store.AcceptGovInvitation(ctx, userID, inviteHash(code), s.now().UTC())
	switch {
	case errors.Is(err, store.ErrInvitationGone):
		return nil, errInvalidInvitation
	case err != nil:
		return nil, err
	}
	s.log.Info("government invitation accepted", "user_id", userID)
	s.changed(ctx, p, verification.ReasonGovernmentVerified)
	return &OrgStatus{
		IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags,
		OrganisationName: p.OrganisationName,
	}, nil
}

func (s *Service) refuse(ctx context.Context, app *store.OrgApplication, e *Error) error {
	if err := s.store.RefuseOrgApplication(ctx, app.ID, e.Code, s.now().UTC()); err != nil {
		s.log.Error("organisation verification: recording refusal failed", "user_id", app.UserID, "application_id", app.ID, "code", e.Code, "err", err)
	}
	s.log.Info("organisation verification refused", "user_id", app.UserID, "application_id", app.ID, "code", e.Code)
	return e
}

func (s *Service) changed(ctx context.Context, p *store.Profile, reason string) {
	if s.invalidate != nil {
		s.invalidate(ctx, p)
	}
	if s.pub == nil {
		return
	}
	if err := s.pub.PublishVerificationChanged(ctx, sharedEvents.IdentityVerificationChangedPayload{
		UserID: p.UserID.String(), IsVerified: p.IsVerified, VerificationLevel: p.VerificationLevel,
		BadgeFlags: p.BadgeFlags, Reason: reason, ChangedAt: s.now().UTC(),
	}); err != nil {
		s.log.Warn("official verification: publishing identity.verification.changed failed", "user_id", p.UserID, "reason", reason, "err", err)
	}
}

// newCode is a uniformly random six-digit code (FixedCode in email mock mode).
func (s *Service) newCode() (string, error) {
	if s.cfg.FixedCode != "" {
		return s.cfg.FixedCode, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	c := n.String()
	return strings.Repeat("0", 6-len(c)) + c, nil
}

// codeHash binds a code to its application, so a hash cannot be replayed
// against another application.
func codeHash(appID uuid.UUID, code string) string {
	sum := sha256.Sum256([]byte("org-domain-code.v1\x00" + appID.String() + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

const invitePrefix = "GOV-"

var inviteEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newInviteCode is GOV- plus 160 random bits in base32.
func newInviteCode() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return invitePrefix + inviteEncoding.EncodeToString(b), nil
}

func validInviteCode(code string) bool {
	if !strings.HasPrefix(code, invitePrefix) {
		return false
	}
	b, err := inviteEncoding.DecodeString(strings.TrimPrefix(code, invitePrefix))
	return err == nil && len(b) == 20
}

func inviteHash(code string) string {
	sum := sha256.Sum256([]byte("gov-invite.v1\x00" + code))
	return hex.EncodeToString(sum[:])
}
