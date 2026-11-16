//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

// Official and Government mark SQL against live PostgreSQL (CI only):
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/store/ -run 'Official|Government|ReservedNames' -v
//
// The tables match database/setup.sql.
const officialVerificationTestSchema = `
CREATE TABLE IF NOT EXISTS profile.organisation_verification_applications (
    id                    UUID PRIMARY KEY,
    user_id               UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    legal_name            TEXT NOT NULL,
    registration_kind     TEXT NOT NULL CHECK (registration_kind IN ('gstin', 'cin')),
    registration_number   TEXT NOT NULL,
    official_domain       TEXT NOT NULL,
    signatory_email       TEXT NOT NULL,
    registry_confirmed    BOOLEAN NOT NULL DEFAULT FALSE,
    registry_name         TEXT NOT NULL DEFAULT '',
    registry_provider     TEXT NOT NULL DEFAULT '',
    document_media_id     UUID,
    status                TEXT NOT NULL DEFAULT 'email_pending'
        CHECK (status IN ('email_pending', 'in_review', 'approved', 'rejected', 'refused', 'superseded')),
    refusal_code          TEXT NOT NULL DEFAULT '',
    email_code_hash       TEXT,
    email_code_attempts   INT NOT NULL DEFAULT 0,
    email_code_expires_at TIMESTAMPTZ,
    reviewer_id           UUID,
    review_reason         TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at          TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS profile.government_invitations (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    official_name TEXT NOT NULL,
    code_hash     TEXT NOT NULL UNIQUE,
    approver_one  UUID NOT NULL,
    approver_two  UUID NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked')),
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at   TIMESTAMPTZ,
    CHECK (approver_one <> approver_two),
    CHECK (approver_one <> user_id AND approver_two <> user_id)
);
ALTER TABLE profile.government_invitations
    ADD COLUMN IF NOT EXISTS revoked_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_by    UUID,
    ADD COLUMN IF NOT EXISTS revoke_reason TEXT NOT NULL DEFAULT '';
`

func officialStore(t *testing.T) (*Store, func(name string, person bool) uuid.UUID) {
	t.Helper()
	pool, s := personPool(t)
	if _, err := pool.Exec(context.Background(), officialVerificationTestSchema); err != nil {
		t.Fatalf("install official verification schema: %v", err)
	}
	seed := func(name string, person bool) uuid.UUID {
		id := seedPerson(t, pool, name)
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM profile.organisation_verification_applications WHERE user_id = $1`, id)
			_, _ = pool.Exec(context.Background(), `DELETE FROM profile.government_invitations WHERE user_id = $1`, id)
		})
		if person {
			if _, err := pool.Exec(context.Background(), `UPDATE profile.profiles SET is_verified = TRUE, verification_level = 'person', badge_flags = 1 WHERE user_id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	return s, seed
}

func pendingApp(t *testing.T, s *Store, user uuid.UUID, number string) OrgApplication {
	t.Helper()
	now := time.Now().UTC()
	a := OrgApplication{ID: uuid.New(), UserID: user, LegalName: "Acme Pvt Ltd", RegistrationKind: "gstin", RegistrationNumber: number,
		OfficialDomain: "acme.example", SignatoryEmail: "a@acme.example", RegistryConfirmed: true, RegistryName: "ACME PRIVATE LIMITED",
		EmailCodeHash: "h", EmailCodeExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.CreateOrgApplication(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOfficialVerification_ApproveSetsTheMarkAndReservesTheName(t *testing.T) {
	s, seed := officialStore(t)
	ctx := context.Background()
	u := seed("Asha Rao", true)
	number := "GST-" + uuid.NewString()
	a := pendingApp(t, s, u, number)

	n, err := s.CountOrgCodeAttempt(ctx, a.ID, time.Now().UTC())
	if err != nil || n == nil || *n != 1 {
		t.Fatalf("attempt = %v %v", n, err)
	}
	p, err := s.ApproveOrganisation(ctx, ApproveOrganisationInput{ApplicationID: a.ID, UserID: u, FromStatus: OrgAppEmailPending,
		OrganisationName: "ACME PRIVATE LIMITED", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsVerified || p.VerificationLevel != verification.LevelOrganisation || p.BadgeFlags&verification.BadgeOrganisation == 0 ||
		p.OrganisationName != "ACME PRIVATE LIMITED" || p.DisplayName != "ACME PRIVATE LIMITED" {
		t.Fatalf("profile = %+v", p)
	}
	other := seed("Some One", false)
	hit, err := s.FindReservedNameHolder(ctx, other, "Acme Private Limited Official")
	if err != nil || hit == nil || hit.UserID != u || hit.Kind != verification.LevelOrganisation {
		t.Fatalf("hold = %+v %v", hit, err)
	}
	if own, _ := s.FindReservedNameHolder(ctx, u, "ACME PRIVATE LIMITED"); own != nil {
		t.Fatal("the owner is held by its own name")
	}
	if held, err := s.RegistrationHeldByOther(ctx, other, "gstin", number); err != nil || !held {
		t.Fatalf("registration held = %v %v", held, err)
	}
	// Approving again is refused: the application is closed.
	if _, err := s.ApproveOrganisation(ctx, ApproveOrganisationInput{ApplicationID: a.ID, UserID: u, FromStatus: OrgAppEmailPending, Now: time.Now()}); !errors.Is(err, ErrApplicationGone) {
		t.Fatalf("second approval = %v", err)
	}

	// A rename to another name clears the mark; the names are released.
	name := "Different Name"
	if _, err := s.UpdateProfile(ctx, u, UpdateProfileParams{DisplayName: &name, ClearVerificationOnRename: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshReservedNames(ctx, u); err != nil {
		t.Fatal(err)
	}
	pp, _ := s.GetProfile(ctx, u)
	if pp.IsVerified || pp.VerificationLevel != "" || pp.BadgeFlags&verification.BadgeAllMarks != 0 {
		t.Fatalf("rename kept the mark: %+v", pp)
	}
	if hit, _ := s.FindReservedNameHolder(ctx, other, "Acme Private Limited"); hit != nil {
		t.Fatal("a cleared mark still holds its name")
	}
}

func TestOfficialVerification_ApproveNeedsThePersonMark(t *testing.T) {
	s, seed := officialStore(t)
	u := seed("Asha Rao", false)
	a := pendingApp(t, s, u, "GST-"+uuid.NewString())
	_, err := s.ApproveOrganisation(context.Background(), ApproveOrganisationInput{ApplicationID: a.ID, UserID: u, FromStatus: OrgAppEmailPending,
		OrganisationName: "ACME", Now: time.Now().UTC()})
	if !errors.Is(err, ErrPersonMarkRequired) {
		t.Fatalf("err = %v, want ErrPersonMarkRequired", err)
	}
}

func TestGovernmentInvitation_AcceptOnceByTheInvitee(t *testing.T) {
	s, seed := officialStore(t)
	ctx := context.Background()
	u := seed("Some Officer", false)
	other := seed("Someone Else", false)
	now := time.Now().UTC()
	g := GovInvitation{ID: uuid.New(), UserID: u, OfficialName: "Ministry of Example Affairs", CodeHash: "hash-" + uuid.NewString(),
		ApproverOne: uuid.New(), ApproverTwo: uuid.New(), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.CreateGovInvitation(ctx, g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptGovInvitation(ctx, other, g.CodeHash, now); !errors.Is(err, ErrInvitationGone) {
		t.Fatalf("another account accepted: %v", err)
	}
	p, err := s.AcceptGovInvitation(ctx, u, g.CodeHash, now)
	if err != nil || p.VerificationLevel != verification.LevelGovernment || p.BadgeFlags&verification.BadgeGovernment == 0 ||
		p.OrganisationName != "Ministry of Example Affairs" {
		t.Fatalf("accept = %+v %v", p, err)
	}
	if _, err := s.AcceptGovInvitation(ctx, u, g.CodeHash, now); !errors.Is(err, ErrInvitationGone) {
		t.Fatalf("accepted twice: %v", err)
	}
	// Two-person rule backstop in the schema.
	same := uuid.New()
	bad := GovInvitation{ID: uuid.New(), UserID: u, OfficialName: "X", CodeHash: "hash-" + uuid.NewString(),
		ApproverOne: same, ApproverTwo: same, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.CreateGovInvitation(ctx, bad); err == nil {
		t.Fatal("one approver twice was stored")
	}
}
