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

// The admin console's reads and the invitation revoke for the Official and
// Government marks (docs/designs/official-marks-console-contract.md section
// 2). Lists page by a (created_at, id) keyset: the review queue oldest first
// (first come, first reviewed), everything else newest first.

// Government invitation statuses. InvitationExpired is never stored: it is a
// pending row whose expires_at has passed.
const (
	InvitationPending  = "pending"
	InvitationAccepted = "accepted"
	InvitationRevoked  = "revoked"
	InvitationExpired  = "expired"
)

var (
	// ErrInvitationNotFound: no invitation with that id.
	ErrInvitationNotFound = errors.New("invitation not found")
	// ErrInvitationNotPending: the invitation was accepted, revoked or has
	// expired, so it cannot be revoked.
	ErrInvitationNotPending = errors.New("invitation is not pending")
)

// Keyset is a list position: the last row's created_at and id.
type Keyset struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// MarksApplicant is the account behind an application or invitation as the
// console shows it.
type MarksApplicant struct {
	Username          string
	DisplayName       string
	IsVerified        bool
	VerificationLevel string
	BadgeFlags        int
}

// IsPersonVerified reads the person bit, not the level (16 Nov 2026): an
// account that already holds the Official or Government mark keeps the bit
// while its level moves on, and is still a verified person. HoldsPersonMark
// (level "person" too) stays the test for applying.
func (a MarksApplicant) IsPersonVerified() bool {
	return a.IsVerified && a.BadgeFlags&verification.BadgePersonVerified != 0
}

// AdminOrgApplication is an application with what the console shows beside it.
type AdminOrgApplication struct {
	OrgApplication
	ReviewReason string
	CompletedAt  *time.Time
	Applicant    MarksApplicant
}

// String keeps the registration number and signatory address out of logs.
func (a AdminOrgApplication) String() string { return a.OrgApplication.String() }

// Never selected: email_code_hash. The console has no use for it.
const adminOrgAppCols = `a.id, a.user_id, a.legal_name, a.registration_kind, a.registration_number, a.official_domain,
	a.signatory_email, a.registry_confirmed, a.registry_name, a.registry_provider, a.document_media_id,
	a.status, a.refusal_code, a.reviewer_id, a.review_reason, a.completed_at, a.created_at,
	COALESCE(p.username, ''), COALESCE(p.display_name, ''), COALESCE(p.is_verified, FALSE),
	COALESCE(p.verification_level, ''), COALESCE(p.badge_flags, 0)`

const adminOrgAppFrom = ` FROM profile.organisation_verification_applications a
	LEFT JOIN profile.profiles p ON p.user_id = a.user_id`

func scanAdminOrgApp(row pgx.Row) (*AdminOrgApplication, error) {
	var a AdminOrgApplication
	err := row.Scan(&a.ID, &a.UserID, &a.LegalName, &a.RegistrationKind, &a.RegistrationNumber, &a.OfficialDomain,
		&a.SignatoryEmail, &a.RegistryConfirmed, &a.RegistryName, &a.RegistryProvider, &a.DocumentMediaID,
		&a.Status, &a.RefusalCode, &a.ReviewerID, &a.ReviewReason, &a.CompletedAt, &a.CreatedAt,
		&a.Applicant.Username, &a.Applicant.DisplayName, &a.Applicant.IsVerified,
		&a.Applicant.VerificationLevel, &a.Applicant.BadgeFlags)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListOrgApplicationsAdmin pages applications in one status. in_review is
// oldest first; every other status newest first. after is the last row of
// the previous page (nil for the first). limit is the page size the caller
// wants; one extra row is read so the caller can tell whether a next page
// exists (the result may hold limit+1 rows).
func (s *Store) ListOrgApplicationsAdmin(ctx context.Context, status string, limit int, after *Keyset) ([]AdminOrgApplication, error) {
	order, cmp := "DESC", "<"
	if status == OrgAppInReview {
		order, cmp = "ASC", ">"
	}
	args := []any{status, limit + 1}
	where := ` WHERE a.status = $1`
	if after != nil {
		where += ` AND (a.created_at, a.id) ` + cmp + ` ($3, $4)`
		args = append(args, after.CreatedAt, after.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT `+adminOrgAppCols+adminOrgAppFrom+where+
		` ORDER BY a.created_at `+order+`, a.id `+order+` LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("list org applications: %w", err)
	}
	defer rows.Close()
	out := []AdminOrgApplication{}
	for rows.Next() {
		a, err := scanAdminOrgApp(rows)
		if err != nil {
			return nil, fmt.Errorf("list org applications: scan: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetOrgApplicationAdmin reads one application with its applicant. Nil when
// there is none.
func (s *Store) GetOrgApplicationAdmin(ctx context.Context, id uuid.UUID) (*AdminOrgApplication, error) {
	return scanAdminOrgApp(s.db.QueryRow(ctx, `SELECT `+adminOrgAppCols+adminOrgAppFrom+` WHERE a.id = $1`, id))
}

// MarksStats is the console's stats strip.
type MarksStats struct {
	InReview           int
	ApprovedSince      int
	RejectedSince      int
	InvitationsPending int
	OfficialAccounts   int
	GovernmentAccounts int
}

// MarksStats counts the queue, decisions closed since since, unexpired
// pending invitations at now, and the accounts holding each mark.
func (s *Store) MarksStats(ctx context.Context, since, now time.Time) (*MarksStats, error) {
	var m MarksStats
	err := s.db.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM profile.organisation_verification_applications WHERE status = 'in_review'),
		  (SELECT COUNT(*) FROM profile.organisation_verification_applications WHERE status = 'approved' AND completed_at >= $1),
		  (SELECT COUNT(*) FROM profile.organisation_verification_applications WHERE status = 'rejected' AND completed_at >= $1),
		  (SELECT COUNT(*) FROM profile.government_invitations WHERE status = 'pending' AND expires_at > $2),
		  (SELECT COUNT(*) FROM profile.profiles WHERE is_verified AND verification_level = $3),
		  (SELECT COUNT(*) FROM profile.profiles WHERE is_verified AND verification_level = $4)`,
		since, now, verification.LevelOrganisation, verification.LevelGovernment).
		Scan(&m.InReview, &m.ApprovedSince, &m.RejectedSince, &m.InvitationsPending, &m.OfficialAccounts, &m.GovernmentAccounts)
	if err != nil {
		return nil, fmt.Errorf("marks stats: %w", err)
	}
	return &m, nil
}

// AdminGovInvitation is an invitation as the console sees it. Never the code
// hash. Status is computed: a pending row past its expiry reads "expired".
type AdminGovInvitation struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	OfficialName string
	Status       string
	ApproverOne  uuid.UUID
	ApproverTwo  uuid.UUID
	Reason       string
	ExpiresAt    time.Time
	CreatedAt    time.Time
	AcceptedAt   *time.Time
	Invitee      MarksApplicant
}

// Never selected: code_hash.
const adminGovCols = `g.id, g.user_id, g.official_name,
	CASE WHEN g.status = 'pending' AND g.expires_at <= $1 THEN 'expired' ELSE g.status END,
	g.approver_one, g.approver_two, g.reason, g.expires_at, g.created_at, g.accepted_at,
	COALESCE(p.username, ''), COALESCE(p.display_name, ''), COALESCE(p.is_verified, FALSE),
	COALESCE(p.verification_level, ''), COALESCE(p.badge_flags, 0)`

const adminGovFrom = ` FROM profile.government_invitations g
	LEFT JOIN profile.profiles p ON p.user_id = g.user_id`

func scanAdminGov(row pgx.Row) (*AdminGovInvitation, error) {
	var g AdminGovInvitation
	err := row.Scan(&g.ID, &g.UserID, &g.OfficialName, &g.Status, &g.ApproverOne, &g.ApproverTwo, &g.Reason,
		&g.ExpiresAt, &g.CreatedAt, &g.AcceptedAt,
		&g.Invitee.Username, &g.Invitee.DisplayName, &g.Invitee.IsVerified, &g.Invitee.VerificationLevel, &g.Invitee.BadgeFlags)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// ListGovInvitationsAdmin pages invitations in one status (pending, accepted,
// revoked or the computed expired), newest first. pending excludes expired
// rows. Reads limit+1 rows like ListOrgApplicationsAdmin.
func (s *Store) ListGovInvitationsAdmin(ctx context.Context, status string, limit int, after *Keyset, now time.Time) ([]AdminGovInvitation, error) {
	args := []any{now, limit + 1}
	var where string
	switch status {
	case InvitationPending:
		where = ` WHERE g.status = 'pending' AND g.expires_at > $1`
	case InvitationExpired:
		where = ` WHERE g.status = 'pending' AND g.expires_at <= $1`
	case InvitationAccepted, InvitationRevoked:
		where = ` WHERE g.status = $3`
		args = append(args, status)
	default:
		return nil, fmt.Errorf("list government invitations: unknown status %q", status)
	}
	if after != nil {
		n := len(args)
		where += fmt.Sprintf(` AND (g.created_at, g.id) < ($%d, $%d)`, n+1, n+2)
		args = append(args, after.CreatedAt, after.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT `+adminGovCols+adminGovFrom+where+
		` ORDER BY g.created_at DESC, g.id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("list government invitations: %w", err)
	}
	defer rows.Close()
	out := []AdminGovInvitation{}
	for rows.Next() {
		g, err := scanAdminGov(rows)
		if err != nil {
			return nil, fmt.Errorf("list government invitations: scan: %w", err)
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// GetGovInvitationAdmin reads one invitation; nil when there is none.
func (s *Store) GetGovInvitationAdmin(ctx context.Context, id uuid.UUID, now time.Time) (*AdminGovInvitation, error) {
	return scanAdminGov(s.db.QueryRow(ctx, `SELECT `+adminGovCols+adminGovFrom+` WHERE g.id = $2`, now, id))
}

// RevokeGovInvitation closes a pending, unexpired invitation and records who
// revoked it and why. ErrInvitationNotFound when there is no such id,
// ErrInvitationNotPending when it was accepted, revoked or has expired.
func (s *Store) RevokeGovInvitation(ctx context.Context, id, revokedBy uuid.UUID, reason string, now time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE profile.government_invitations
		   SET status = 'revoked', revoked_at = $2, revoked_by = $3, revoke_reason = $4
		 WHERE id = $1 AND status = 'pending' AND expires_at > $2`, id, now, revokedBy, reason)
	if err != nil {
		return fmt.Errorf("revoke government invitation: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM profile.government_invitations WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("revoke government invitation: lookup: %w", err)
	}
	if !exists {
		return ErrInvitationNotFound
	}
	return ErrInvitationNotPending
}
