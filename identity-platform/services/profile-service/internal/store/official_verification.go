package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Official (organisation) and Government mark storage
// (internal/officialverify). See database/setup.sql for every column.

// Organisation application statuses.
const (
	OrgAppEmailPending = "email_pending"
	OrgAppInReview     = "in_review"
	OrgAppApproved     = "approved"
	OrgAppRejected     = "rejected"
	OrgAppRefused      = "refused"
	OrgAppSuperseded   = "superseded"
)

var (
	// ErrPersonMarkRequired: the applicant no longer holds the person mark.
	ErrPersonMarkRequired = errors.New("the applicant is not a verified person")
	// ErrApplicationGone: the application is no longer in the expected state.
	ErrApplicationGone = errors.New("application is no longer pending")
	// ErrInvitationGone: no pending, unexpired invitation with that code
	// for this account.
	ErrInvitationGone = errors.New("invitation is not valid")
)

// OrgApplication is one Official-mark application.
type OrgApplication struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	LegalName          string
	RegistrationKind   string
	RegistrationNumber string
	OfficialDomain     string
	SignatoryEmail     string
	RegistryConfirmed  bool
	RegistryName       string
	RegistryProvider   string
	DocumentMediaID    *uuid.UUID
	Status             string
	RefusalCode        string
	EmailCodeHash      string
	EmailCodeAttempts  int
	EmailCodeExpiresAt time.Time
	ReviewerID         *uuid.UUID
	CreatedAt          time.Time
}

// String keeps the code hash and the signatory address out of logs.
func (a OrgApplication) String() string {
	return "OrgApplication{" + a.ID.String() + ", " + a.Status + ", redacted}"
}

const orgAppCols = `id, user_id, legal_name, registration_kind, registration_number, official_domain,
	signatory_email, registry_confirmed, registry_name, registry_provider, document_media_id,
	status, refusal_code, COALESCE(email_code_hash, ''), email_code_attempts,
	COALESCE(email_code_expires_at, 'epoch'::timestamptz), reviewer_id, created_at`

func scanOrgApp(row pgx.Row) (*OrgApplication, error) {
	var a OrgApplication
	err := row.Scan(&a.ID, &a.UserID, &a.LegalName, &a.RegistrationKind, &a.RegistrationNumber, &a.OfficialDomain,
		&a.SignatoryEmail, &a.RegistryConfirmed, &a.RegistryName, &a.RegistryProvider, &a.DocumentMediaID,
		&a.Status, &a.RefusalCode, &a.EmailCodeHash, &a.EmailCodeAttempts, &a.EmailCodeExpiresAt, &a.ReviewerID, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// CountOrgApplicationsSince counts the user's applications created since t.
func (s *Store) CountOrgApplicationsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM profile.organisation_verification_applications WHERE user_id = $1 AND created_at >= $2`,
		userID, since).Scan(&n)
	return n, err
}

// CreateOrgApplication supersedes the user's open email_pending application
// and records a new one, in one transaction. An application in review blocks
// a new one (ErrApplicationGone is not used here: the service checks first,
// and the partial unique rule is the service's).
func (s *Store) CreateOrgApplication(ctx context.Context, a OrgApplication) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("org application: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE profile.organisation_verification_applications
		   SET status = 'superseded', email_code_hash = NULL, updated_at = $2, completed_at = $2
		 WHERE user_id = $1 AND status = 'email_pending'`, a.UserID, a.CreatedAt); err != nil {
		return fmt.Errorf("org application: supersede: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile.organisation_verification_applications
			(id, user_id, legal_name, registration_kind, registration_number, official_domain, signatory_email,
			 registry_confirmed, registry_name, registry_provider, document_media_id, status,
			 email_code_hash, email_code_attempts, email_code_expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'email_pending', $12, 0, $13, $14, $14)`,
		a.ID, a.UserID, a.LegalName, a.RegistrationKind, a.RegistrationNumber, a.OfficialDomain, a.SignatoryEmail,
		a.RegistryConfirmed, a.RegistryName, a.RegistryProvider, a.DocumentMediaID,
		a.EmailCodeHash, a.EmailCodeExpiresAt, a.CreatedAt); err != nil {
		return fmt.Errorf("org application: insert: %w", err)
	}
	return tx.Commit(ctx)
}

// LatestOrgApplication is the user's newest application.
func (s *Store) LatestOrgApplication(ctx context.Context, userID uuid.UUID) (*OrgApplication, error) {
	return scanOrgApp(s.db.QueryRow(ctx, `SELECT `+orgAppCols+`
		FROM profile.organisation_verification_applications
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, userID))
}

// GetOrgApplication reads one application.
func (s *Store) GetOrgApplication(ctx context.Context, id uuid.UUID) (*OrgApplication, error) {
	return scanOrgApp(s.db.QueryRow(ctx, `SELECT `+orgAppCols+`
		FROM profile.organisation_verification_applications WHERE id = $1`, id))
}

// CountOrgCodeAttempt records one code attempt on an open, unexpired
// application and returns the attempt count including this one. Nil when
// there is no such application.
func (s *Store) CountOrgCodeAttempt(ctx context.Context, id uuid.UUID, now time.Time) (*int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		UPDATE profile.organisation_verification_applications
		   SET email_code_attempts = email_code_attempts + 1, updated_at = $2
		 WHERE id = $1 AND status = 'email_pending' AND email_code_expires_at > $2
		RETURNING email_code_attempts`, id, now).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// RefuseOrgApplication ends an open application with code.
func (s *Store) RefuseOrgApplication(ctx context.Context, id uuid.UUID, code string, now time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile.organisation_verification_applications
		   SET status = 'refused', refusal_code = $2, email_code_hash = NULL, updated_at = $3, completed_at = $3
		 WHERE id = $1 AND status IN ('email_pending', 'in_review')`, id, code, now)
	return err
}

// MoveOrgApplicationToReview moves an application whose e-mail code was
// proven, and which carries a document instead of a registry confirmation,
// to the human review queue.
func (s *Store) MoveOrgApplicationToReview(ctx context.Context, id uuid.UUID, now time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE profile.organisation_verification_applications
		   SET status = 'in_review', email_code_hash = NULL, updated_at = $2
		 WHERE id = $1 AND status = 'email_pending' AND registry_confirmed = FALSE AND document_media_id IS NOT NULL`, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrApplicationGone
	}
	return nil
}

// RejectOrgApplication records a reviewer's refusal of an application in review.
func (s *Store) RejectOrgApplication(ctx context.Context, id, reviewerID uuid.UUID, reason string, now time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE profile.organisation_verification_applications
		   SET status = 'rejected', reviewer_id = $2, review_reason = $3, updated_at = $4, completed_at = $4
		 WHERE id = $1 AND status = 'in_review'`, id, reviewerID, reason, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrApplicationGone
	}
	return nil
}

// ApproveOrganisationInput is an approval: automatic (the e-mail code on a
// registry-confirmed application, FromStatus email_pending) or a reviewer's
// (FromStatus in_review, ReviewerID set).
type ApproveOrganisationInput struct {
	ApplicationID    uuid.UUID
	UserID           uuid.UUID
	FromStatus       string
	OrganisationName string
	ReviewerID       *uuid.UUID
	ReviewReason     string
	Now              time.Time
}

// ApproveOrganisation sets the Official mark in one transaction: the profile
// row is locked and must still hold the person mark (the applicant is the
// authorised signatory), the application moves from FromStatus to approved,
// profile.profiles gets verification_level 'organisation', the organisation
// bit, organisation_name and that name as its display name, usr.users is
// marked verified, and the names are reserved against impersonation.
func (s *Store) ApproveOrganisation(ctx context.Context, in ApproveOrganisationInput) (*Profile, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("approve organisation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var verified bool
	var level string
	var badges int
	err = tx.QueryRow(ctx, `SELECT is_verified, verification_level, badge_flags FROM profile.profiles WHERE user_id = $1 FOR UPDATE`, in.UserID).
		Scan(&verified, &level, &badges)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrApplicationGone
	}
	if err != nil {
		return nil, fmt.Errorf("approve organisation: lock profile: %w", err)
	}
	if !HoldsPersonMark(verified, level, badges) {
		return nil, ErrPersonMarkRequired
	}

	tag, err := tx.Exec(ctx, `
		UPDATE profile.organisation_verification_applications
		   SET status = 'approved', email_code_hash = NULL, reviewer_id = $4, review_reason = $5,
		       updated_at = $6, completed_at = $6
		 WHERE id = $1 AND user_id = $2 AND status = $3`,
		in.ApplicationID, in.UserID, in.FromStatus, in.ReviewerID, in.ReviewReason, in.Now)
	if err != nil {
		return nil, fmt.Errorf("approve organisation: close application: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrApplicationGone
	}
	p, err := setMarkTx(ctx, tx, in.UserID, verification.LevelOrganisation, verification.BadgeOrganisation, in.OrganisationName)
	if err != nil {
		return nil, fmt.Errorf("approve organisation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("approve organisation: commit: %w", err)
	}
	return p, nil
}

// HoldsPersonMark is the person-mark test the Official mark requires.
func HoldsPersonMark(isVerified bool, level string, badges int) bool {
	return isVerified && level == verification.LevelPerson && badges&verification.BadgePersonVerified != 0
}

// setMarkTx writes a mark onto profile.profiles and usr.users and reserves
// the account's names. The display name becomes the registered name, so the
// account presents as what was verified.
func setMarkTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, level string, bit int, name string) (*Profile, error) {
	p, err := scanProfile(tx.QueryRow(ctx, `
		UPDATE profile.profiles
		   SET is_verified = TRUE, verification_level = $2, badge_flags = badge_flags | $3,
		       organisation_name = $4, display_name = $4, updated_at = NOW()
		 WHERE user_id = $1
		RETURNING `+allProfileCols, userID, level, bit, name))
	if err != nil {
		return nil, fmt.Errorf("set profile: %w", err)
	}
	if p == nil {
		return nil, ErrApplicationGone
	}
	if _, err := tx.Exec(ctx, `UPDATE usr.users SET is_verified = TRUE, updated_at = NOW() WHERE id = $1`, userID); err != nil {
		return nil, fmt.Errorf("set usr.users: %w", err)
	}
	if err := refreshReservedNamesTx(ctx, tx, userID); err != nil {
		return nil, err
	}
	return p, nil
}

// GovInvitation is one Government-mark invitation.
type GovInvitation struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	OfficialName string
	CodeHash     string
	ApproverOne  uuid.UUID
	ApproverTwo  uuid.UUID
	Reason       string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

func (g GovInvitation) String() string { return "GovInvitation{" + g.ID.String() + ", redacted}" }

// CreateGovInvitation revokes the user's pending invitations and records a
// new one.
func (s *Store) CreateGovInvitation(ctx context.Context, g GovInvitation) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("government invitation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE profile.government_invitations SET status = 'revoked' WHERE user_id = $1 AND status = 'pending'`, g.UserID); err != nil {
		return fmt.Errorf("government invitation: revoke previous: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile.government_invitations
			(id, user_id, official_name, code_hash, approver_one, approver_two, reason, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)`,
		g.ID, g.UserID, g.OfficialName, g.CodeHash, g.ApproverOne, g.ApproverTwo, g.Reason, g.ExpiresAt, g.CreatedAt); err != nil {
		return fmt.Errorf("government invitation: insert: %w", err)
	}
	return tx.Commit(ctx)
}

// AcceptGovInvitation sets the Government mark in one transaction: the
// invitation must be pending, unexpired and addressed to userID; it is
// closed, and the profile gets verification_level 'government', the
// government bit and the official name.
func (s *Store) AcceptGovInvitation(ctx context.Context, userID uuid.UUID, codeHash string, now time.Time) (*Profile, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept invitation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name string
	err = tx.QueryRow(ctx, `
		UPDATE profile.government_invitations
		   SET status = 'accepted', accepted_at = $3
		 WHERE code_hash = $1 AND user_id = $2 AND status = 'pending' AND expires_at > $3
		RETURNING official_name`, codeHash, userID, now).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvitationGone
	}
	if err != nil {
		return nil, fmt.Errorf("accept invitation: close: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM profile.profiles WHERE user_id = $1 FOR UPDATE`, userID); err != nil {
		return nil, fmt.Errorf("accept invitation: lock profile: %w", err)
	}
	p, err := setMarkTx(ctx, tx, userID, verification.LevelGovernment, verification.BadgeGovernment, name)
	if err != nil {
		return nil, fmt.Errorf("accept invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("accept invitation: commit: %w", err)
	}
	return p, nil
}

// RegistrationHeldByOther reports whether another account currently holds
// the Official mark through an approved application with this registration
// number: one registration, one Official account.
func (s *Store) RegistrationHeldByOther(ctx context.Context, userID uuid.UUID, kind, number string) (bool, error) {
	var held bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile.organisation_verification_applications a
			  JOIN profile.profiles p ON p.user_id = a.user_id
			 WHERE a.registration_kind = $2 AND a.registration_number = $3 AND a.status = 'approved'
			   AND a.user_id <> $1 AND p.is_verified AND p.verification_level = 'organisation')`,
		userID, kind, number).Scan(&held)
	return held, err
}
