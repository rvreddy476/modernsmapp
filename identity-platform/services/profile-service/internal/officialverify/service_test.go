package officialverify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

var (
	t0        = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	applicant = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	adminA    = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	adminB    = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	notAdmin  = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	official  = uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
)

// validGSTIN is a checksum-valid company GSTIN.
var validGSTIN = func() string {
	b := "29AAACZ1234Z1Z"
	return b + string(verification.GSTINCheckDigit(b))
}()

type fakeStore struct {
	mu          sync.Mutex
	profiles    map[uuid.UUID]*store.Profile
	apps        map[uuid.UUID]*store.OrgApplication
	invites     map[string]*store.GovInvitation
	inviteState map[string]string
	heldByOther bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{profiles: map[uuid.UUID]*store.Profile{}, apps: map[uuid.UUID]*store.OrgApplication{},
		invites: map[string]*store.GovInvitation{}, inviteState: map[string]string{}}
}

func (f *fakeStore) addPerson(id uuid.UUID, name string) {
	f.profiles[id] = &store.Profile{UserID: id, DisplayName: name, IsVerified: true,
		VerificationLevel: verification.LevelPerson, BadgeFlags: verification.BadgePersonVerified}
}

func (f *fakeStore) GetProfile(_ context.Context, id uuid.UUID) (*store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *fakeStore) CountOrgApplicationsSince(_ context.Context, id uuid.UUID, since time.Time) (int, error) {
	n := 0
	for _, a := range f.apps {
		if a.UserID == id && !a.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) RegistrationHeldByOther(context.Context, uuid.UUID, string, string) (bool, error) {
	return f.heldByOther, nil
}

func (f *fakeStore) CreateOrgApplication(_ context.Context, a store.OrgApplication) error {
	for _, o := range f.apps {
		if o.UserID == a.UserID && o.Status == store.OrgAppEmailPending {
			o.Status = store.OrgAppSuperseded
		}
	}
	a.Status = store.OrgAppEmailPending
	f.apps[a.ID] = &a
	return nil
}

func (f *fakeStore) LatestOrgApplication(_ context.Context, id uuid.UUID) (*store.OrgApplication, error) {
	var best *store.OrgApplication
	for _, a := range f.apps {
		if a.UserID == id && (best == nil || a.CreatedAt.After(best.CreatedAt) || a.CreatedAt.Equal(best.CreatedAt) && a.Status == store.OrgAppEmailPending) {
			best = a
		}
	}
	if best == nil {
		return nil, nil
	}
	cp := *best
	return &cp, nil
}

func (f *fakeStore) GetOrgApplication(_ context.Context, id uuid.UUID) (*store.OrgApplication, error) {
	a, ok := f.apps[id]
	if !ok {
		return nil, nil
	}
	cp := *a
	return &cp, nil
}

func (f *fakeStore) CountOrgCodeAttempt(_ context.Context, id uuid.UUID, now time.Time) (*int, error) {
	a, ok := f.apps[id]
	if !ok || a.Status != store.OrgAppEmailPending || !a.EmailCodeExpiresAt.After(now) {
		return nil, nil
	}
	a.EmailCodeAttempts++
	n := a.EmailCodeAttempts
	return &n, nil
}

func (f *fakeStore) RefuseOrgApplication(_ context.Context, id uuid.UUID, code string, _ time.Time) error {
	if a, ok := f.apps[id]; ok && (a.Status == store.OrgAppEmailPending || a.Status == store.OrgAppInReview) {
		a.Status, a.RefusalCode = store.OrgAppRefused, code
	}
	return nil
}

func (f *fakeStore) MoveOrgApplicationToReview(_ context.Context, id uuid.UUID, _ time.Time) error {
	a, ok := f.apps[id]
	if !ok || a.Status != store.OrgAppEmailPending || a.RegistryConfirmed || a.DocumentMediaID == nil {
		return store.ErrApplicationGone
	}
	a.Status = store.OrgAppInReview
	return nil
}

func (f *fakeStore) RejectOrgApplication(_ context.Context, id, reviewer uuid.UUID, _ string, _ time.Time) error {
	a, ok := f.apps[id]
	if !ok || a.Status != store.OrgAppInReview {
		return store.ErrApplicationGone
	}
	a.Status, a.ReviewerID = store.OrgAppRejected, &reviewer
	return nil
}

func (f *fakeStore) ApproveOrganisation(_ context.Context, in store.ApproveOrganisationInput) (*store.Profile, error) {
	p := f.profiles[in.UserID]
	if p == nil {
		return nil, store.ErrApplicationGone
	}
	if !store.HoldsPersonMark(p.IsVerified, p.VerificationLevel, p.BadgeFlags) {
		return nil, store.ErrPersonMarkRequired
	}
	a := f.apps[in.ApplicationID]
	if a == nil || a.UserID != in.UserID || a.Status != in.FromStatus {
		return nil, store.ErrApplicationGone
	}
	a.Status, a.ReviewerID = store.OrgAppApproved, in.ReviewerID
	p.IsVerified, p.VerificationLevel = true, verification.LevelOrganisation
	p.BadgeFlags |= verification.BadgeOrganisation
	p.OrganisationName, p.DisplayName = in.OrganisationName, in.OrganisationName
	cp := *p
	return &cp, nil
}

func (f *fakeStore) CreateGovInvitation(_ context.Context, g store.GovInvitation) error {
	for h, o := range f.invites {
		if o.UserID == g.UserID && f.inviteState[h] == "pending" {
			f.inviteState[h] = "revoked"
		}
	}
	f.invites[g.CodeHash] = &g
	f.inviteState[g.CodeHash] = "pending"
	return nil
}

func (f *fakeStore) AcceptGovInvitation(_ context.Context, userID uuid.UUID, hash string, now time.Time) (*store.Profile, error) {
	g, ok := f.invites[hash]
	if !ok || g.UserID != userID || f.inviteState[hash] != "pending" || !g.ExpiresAt.After(now) {
		return nil, store.ErrInvitationGone
	}
	f.inviteState[hash] = "accepted"
	p := f.profiles[userID]
	p.IsVerified, p.VerificationLevel = true, verification.LevelGovernment
	p.BadgeFlags |= verification.BadgeGovernment
	p.OrganisationName, p.DisplayName = g.OfficialName, g.OfficialName
	cp := *p
	return &cp, nil
}

type fakeRoles struct {
	admins map[uuid.UUID]bool
	err    error
	asked  []uuid.UUID
}

func (r *fakeRoles) HasRole(_ context.Context, id uuid.UUID, role string) (bool, error) {
	r.asked = append(r.asked, id)
	if r.err != nil {
		return false, r.err
	}
	return role == AdminRole && r.admins[id], nil
}

type fakePub struct {
	events []sharedEvents.IdentityVerificationChangedPayload
}

func (p *fakePub) PublishVerificationChanged(_ context.Context, e sharedEvents.IdentityVerificationChangedPayload) error {
	p.events = append(p.events, e)
	return nil
}

type fixture struct {
	st    *fakeStore
	reg   *MockRegistry
	mail  *MockMailer
	roles *fakeRoles
	pub   *fakePub
	svc   *Service
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		st:    newFakeStore(),
		reg:   &MockRegistry{Records: map[string]RegistryRecord{validGSTIN: {Found: true, Active: true, LegalName: "ACME PRIVATE LIMITED", Provider: "test"}}},
		mail:  &MockMailer{},
		roles: &fakeRoles{admins: map[uuid.UUID]bool{adminA: true, adminB: true}},
		pub:   &fakePub{},
		clock: t0,
	}
	f.st.addPerson(applicant, "Asha Rao")
	f.svc = New(Config{OrgEnabled: true, GovEnabled: true, Registry: f.reg, Mailer: f.mail, Roles: f.roles},
		f.st, f.pub, nil, nil).WithClock(func() time.Time { return f.clock })
	return f
}

func goodRequest() OrgApplicationRequest {
	return OrgApplicationRequest{
		LegalName: "Acme Pvt. Ltd.", GSTIN: validGSTIN, OfficialDomain: "acme.co.in", SignatoryEmail: "asha@acme.co.in",
	}
}

func wantRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestOrganisation_AllChecksPassAutoApproves(t *testing.T) {
	f := newFixture(t)
	st, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	if err != nil || st.Next != "email_code" {
		t.Fatalf("submit = %+v, %v", st, err)
	}
	if f.mail.LastTo != "asha@acme.co.in" || len(f.mail.LastCode) != 6 {
		t.Fatalf("code sent to %q", f.mail.LastTo)
	}
	st, err = f.svc.ConfirmOrganisationEmail(context.Background(), applicant, f.mail.LastCode)
	if err != nil {
		t.Fatal(err)
	}
	p := f.st.profiles[applicant]
	if !p.IsVerified || p.VerificationLevel != verification.LevelOrganisation || p.BadgeFlags&verification.BadgeOrganisation == 0 ||
		p.OrganisationName != "ACME PRIVATE LIMITED" || st.Next != "" || st.OrganisationName != "ACME PRIVATE LIMITED" {
		t.Fatalf("not approved: %+v / %+v", p, st)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].Reason != verification.ReasonOrganisationVerified ||
		f.pub.events[0].VerificationLevel != verification.LevelOrganisation {
		t.Fatalf("events = %+v", f.pub.events)
	}
}

func TestOrganisation_RequiresThePersonMark(t *testing.T) {
	f := newFixture(t)
	f.st.profiles[applicant].IsVerified = false
	_, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "PERSON_VERIFICATION_REQUIRED")

	// Lost between submit and confirm: the approval transaction refuses.
	f = newFixture(t)
	if _, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest()); err != nil {
		t.Fatal(err)
	}
	f.st.profiles[applicant].BadgeFlags = 0
	_, err = f.svc.ConfirmOrganisationEmail(context.Background(), applicant, f.mail.LastCode)
	wantRefusal(t, err, "PERSON_VERIFICATION_REQUIRED")
	if f.st.profiles[applicant].VerificationLevel == verification.LevelOrganisation {
		t.Fatal("approved without the person mark")
	}
}

func TestOrganisation_FormatChecks(t *testing.T) {
	cases := map[string]func(*OrgApplicationRequest){
		"INVALID_GSTIN":         func(r *OrgApplicationRequest) { r.GSTIN = validGSTIN[:14] + "0" },
		"REGISTRATION_REQUIRED": func(r *OrgApplicationRequest) { r.CIN = "L17110MH1973PLC019786" },
		"INVALID_CIN":           func(r *OrgApplicationRequest) { r.GSTIN, r.CIN = "", "L17110MH1973ZZZ019786" },
		"INVALID_LEGAL_NAME":    func(r *OrgApplicationRequest) { r.LegalName = "Ltd" },
		"INVALID_DOMAIN":        func(r *OrgApplicationRequest) { r.OfficialDomain = "localhost" },
		"INVALID_EMAIL":         func(r *OrgApplicationRequest) { r.SignatoryEmail = "Asha <asha@acme.co.in>" },
	}
	if validGSTIN[14] == '0' {
		cases["INVALID_GSTIN"] = func(r *OrgApplicationRequest) { r.GSTIN = validGSTIN[:14] + "1" }
	}
	for code, mutate := range cases {
		f := newFixture(t)
		req := goodRequest()
		mutate(&req)
		_, err := f.svc.SubmitOrganisation(context.Background(), applicant, req)
		wantRefusal(t, err, code)
		if f.mail.Sent != 0 {
			t.Fatalf("%s: a code was sent", code)
		}
	}
}

func TestOrganisation_FreeMailDomainRefused(t *testing.T) {
	f := newFixture(t)
	req := goodRequest()
	req.OfficialDomain, req.SignatoryEmail = "gmail.com", "acme@gmail.com"
	_, err := f.svc.SubmitOrganisation(context.Background(), applicant, req)
	wantRefusal(t, err, "FREE_MAIL_DOMAIN")
}

func TestOrganisation_SignatoryMustBeOnTheOfficialDomain(t *testing.T) {
	for _, email := range []string{"asha@gmail.com", "asha@mail.acme.co.in", "asha@acme.com", "asha@evilacme.co.in"} {
		f := newFixture(t)
		req := goodRequest()
		req.SignatoryEmail = email
		_, err := f.svc.SubmitOrganisation(context.Background(), applicant, req)
		wantRefusal(t, err, "DOMAIN_MISMATCH")
	}
}

func TestOrganisation_RegistryMustConfirm(t *testing.T) {
	f := newFixture(t)
	f.reg.Records = nil
	_, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "REGISTRATION_NOT_FOUND")

	f = newFixture(t)
	f.reg.Records[validGSTIN] = RegistryRecord{Found: true, Active: false, LegalName: "ACME PRIVATE LIMITED"}
	_, err = f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "REGISTRATION_NOT_FOUND")

	f = newFixture(t)
	f.reg.Err = ErrRegistryUnavailable
	_, err = f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "REGISTRY_UNAVAILABLE")
}

func TestOrganisation_LegalNameMustMatchTheRegistry(t *testing.T) {
	f := newFixture(t)
	req := goodRequest()
	req.LegalName = "Acme Holdings Ltd"
	_, err := f.svc.SubmitOrganisation(context.Background(), applicant, req)
	wantRefusal(t, err, "LEGAL_NAME_MISMATCH")
	if f.mail.Sent != 0 || len(f.st.apps) != 0 {
		t.Fatal("a mismatched name opened an application")
	}
}

func TestOrganisation_OneRegistrationOneAccount(t *testing.T) {
	f := newFixture(t)
	f.st.heldByOther = true
	_, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "REGISTRATION_IN_USE")
}

func TestOrganisation_WrongCodesEndTheApplication(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest()); err != nil {
		t.Fatal(err)
	}
	good := f.mail.LastCode
	wrong := "000000"
	if good == wrong {
		wrong = "111111"
	}
	for i := 1; i < MaxCodeAttempts; i++ {
		_, err := f.svc.ConfirmOrganisationEmail(context.Background(), applicant, wrong)
		wantRefusal(t, err, "CODE_MISMATCH")
	}
	_, err := f.svc.ConfirmOrganisationEmail(context.Background(), applicant, wrong)
	wantRefusal(t, err, "TOO_MANY_CODE_ATTEMPTS")
	_, err = f.svc.ConfirmOrganisationEmail(context.Background(), applicant, good)
	wantRefusal(t, err, "NO_PENDING_APPLICATION")
	if f.st.profiles[applicant].VerificationLevel != verification.LevelPerson {
		t.Fatal("approved after the application ended")
	}
}

func TestOrganisation_ExpiredCodeRefused(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest()); err != nil {
		t.Fatal(err)
	}
	f.clock = t0.Add(CodeTTL + time.Second)
	_, err := f.svc.ConfirmOrganisationEmail(context.Background(), applicant, f.mail.LastCode)
	wantRefusal(t, err, "NO_PENDING_APPLICATION")
}

func TestOrganisation_DocumentGoesToReviewNotApproval(t *testing.T) {
	f := newFixture(t)
	f.reg.Err = errors.New("must not be asked")
	doc := uuid.New()
	req := goodRequest()
	req.DocumentMediaID = &doc
	if _, err := f.svc.SubmitOrganisation(context.Background(), applicant, req); err != nil {
		t.Fatal(err)
	}
	st, err := f.svc.ConfirmOrganisationEmail(context.Background(), applicant, f.mail.LastCode)
	if err != nil || st.Status != store.OrgAppInReview || st.Next != "review" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if f.st.profiles[applicant].VerificationLevel != verification.LevelPerson {
		t.Fatal("a document application was approved without review")
	}
	// A new submission while in review is refused.
	_, err = f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "APPLICATION_IN_REVIEW")
}

func reviewFixture(t *testing.T) (*fixture, uuid.UUID) {
	t.Helper()
	f := newFixture(t)
	doc := uuid.New()
	req := goodRequest()
	req.DocumentMediaID = &doc
	st, err := f.svc.SubmitOrganisation(context.Background(), applicant, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmOrganisationEmail(context.Background(), applicant, f.mail.LastCode); err != nil {
		t.Fatal(err)
	}
	return f, *st.ApplicationID
}

func TestReview_AdminApproves(t *testing.T) {
	f, id := reviewFixture(t)
	st, err := f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "approve", ReviewerID: adminA, Reason: "GST certificate checked"})
	if err != nil || st.VerificationLevel != verification.LevelOrganisation || st.OrganisationName != "Acme Pvt. Ltd." {
		t.Fatalf("review = %+v, %v", st, err)
	}
	if r := f.st.apps[id].ReviewerID; r == nil || *r != adminA {
		t.Fatal("reviewer not recorded")
	}
}

func TestReview_ReviewerMustBeAdmin(t *testing.T) {
	f, id := reviewFixture(t)
	_, err := f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "approve", ReviewerID: notAdmin})
	wantRefusal(t, err, "REVIEWER_NOT_ADMIN")
	if f.st.profiles[applicant].VerificationLevel != verification.LevelPerson {
		t.Fatal("a non-admin approved")
	}
	f.roles.err = ErrRolesUnavailable
	_, err = f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "approve", ReviewerID: adminA})
	wantRefusal(t, err, "ROLE_CHECK_UNAVAILABLE")
}

func TestReview_ApplicantCannotReviewThemselves(t *testing.T) {
	f, id := reviewFixture(t)
	f.roles.admins[applicant] = true
	_, err := f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "approve", ReviewerID: applicant})
	wantRefusal(t, err, "REVIEWER_IS_APPLICANT")
}

func TestReview_RejectAndStateChecks(t *testing.T) {
	f, id := reviewFixture(t)
	_, err := f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "maybe", ReviewerID: adminA})
	wantRefusal(t, err, "INVALID_DECISION")
	if _, err := f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "reject", ReviewerID: adminA}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.ReviewOrganisation(context.Background(), id, ReviewDecision{Decision: "approve", ReviewerID: adminA})
	wantRefusal(t, err, "APPLICATION_NOT_IN_REVIEW")
	_, err = f.svc.ReviewOrganisation(context.Background(), uuid.New(), ReviewDecision{Decision: "approve", ReviewerID: adminA})
	wantRefusal(t, err, "APPLICATION_NOT_FOUND")
}

func TestOrganisation_Disabled(t *testing.T) {
	svc := New(Config{}, newFakeStore(), nil, nil, nil)
	_, err := svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.AcceptGovInvitation(context.Background(), applicant, "GOV-X")
	wantRefusal(t, err, "GOVERNMENT_VERIFICATION_DISABLED")
}

func govFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.st.profiles[official] = &store.Profile{UserID: official, DisplayName: "Some Name"}
	return f
}

func TestGovernment_TwoAdminsInviteAndTheUserAccepts(t *testing.T) {
	f := govFixture(t)
	inv, err := f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
		UserID: official, OfficialName: "Ministry of Example Affairs", ApproverIDs: []uuid.UUID{adminA, adminB}, Reason: "MoU 12",
	})
	if err != nil || !strings.HasPrefix(inv.InviteCode, "GOV-") {
		t.Fatalf("invite = %+v, %v", inv, err)
	}
	if len(f.roles.asked) != 2 {
		t.Fatalf("roles asked for %v", f.roles.asked)
	}
	// Another account cannot use the code.
	_, err = f.svc.AcceptGovInvitation(context.Background(), applicant, inv.InviteCode)
	wantRefusal(t, err, "INVALID_INVITATION")

	st, err := f.svc.AcceptGovInvitation(context.Background(), official, strings.ToLower(inv.InviteCode))
	if err != nil || st.VerificationLevel != verification.LevelGovernment || st.BadgeFlags&verification.BadgeGovernment == 0 ||
		st.OrganisationName != "Ministry of Example Affairs" {
		t.Fatalf("accept = %+v, %v", st, err)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].Reason != verification.ReasonGovernmentVerified {
		t.Fatalf("events = %+v", f.pub.events)
	}
	// Single use.
	_, err = f.svc.AcceptGovInvitation(context.Background(), official, inv.InviteCode)
	wantRefusal(t, err, "INVALID_INVITATION")
}

func TestGovernment_TwoPersonRule(t *testing.T) {
	for name, approvers := range map[string][]uuid.UUID{
		"one":   {adminA},
		"same":  {adminA, adminA},
		"three": {adminA, adminB, notAdmin},
		"nil":   {adminA, uuid.Nil},
		"none":  nil,
	} {
		f := govFixture(t)
		_, err := f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
			UserID: official, OfficialName: "Ministry of Example Affairs", ApproverIDs: approvers,
		})
		var e *Error
		if !errors.As(err, &e) || e.Code != "TWO_APPROVERS_REQUIRED" {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(f.st.invites) != 0 {
			t.Errorf("%s: an invitation was created", name)
		}
	}
}

func TestGovernment_ApproversMustBeAdmins(t *testing.T) {
	f := govFixture(t)
	_, err := f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
		UserID: official, OfficialName: "Ministry of Example Affairs", ApproverIDs: []uuid.UUID{adminA, notAdmin},
	})
	wantRefusal(t, err, "APPROVER_NOT_ADMIN")
	_, err = f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
		UserID: official, OfficialName: "Ministry of Example Affairs", ApproverIDs: []uuid.UUID{notAdmin, adminB},
	})
	wantRefusal(t, err, "APPROVER_NOT_ADMIN")
	f.roles.err = ErrRolesUnavailable
	_, err = f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
		UserID: official, OfficialName: "Ministry of Example Affairs", ApproverIDs: []uuid.UUID{adminA, adminB},
	})
	wantRefusal(t, err, "ROLE_CHECK_UNAVAILABLE")
	if len(f.st.invites) != 0 {
		t.Fatal("an invitation was created")
	}
}

func TestGovernment_ApproverCannotInviteThemselves(t *testing.T) {
	f := govFixture(t)
	_, err := f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
		UserID: adminA, OfficialName: "Ministry of Example Affairs", ApproverIDs: []uuid.UUID{adminA, adminB},
	})
	wantRefusal(t, err, "APPROVER_IS_INVITEE")
}

func TestGovernment_ExpiredOrMalformedCodes(t *testing.T) {
	f := govFixture(t)
	inv, err := f.svc.CreateGovInvitation(context.Background(), GovInvitationRequest{
		UserID: official, OfficialName: "Ministry of Example Affairs", ApproverIDs: []uuid.UUID{adminA, adminB},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"", "GOV-", "nonsense", inv.InviteCode + "A"} {
		_, err := f.svc.AcceptGovInvitation(context.Background(), official, c)
		wantRefusal(t, err, "INVALID_INVITATION")
	}
	f.clock = t0.Add(InvitationTTL + time.Second)
	_, err = f.svc.AcceptGovInvitation(context.Background(), official, inv.InviteCode)
	wantRefusal(t, err, "INVALID_INVITATION")
}

func TestConfig_ProductionRefusesMocksAndNeedsProviders(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	base := map[string]string{"APP_ENV": "production", EnvAuthURL: "http://auth", EnvInternalKey: "k"}

	// Off by default.
	cfg, err := ConfigFromEnv(env(map[string]string{}))
	if err != nil || cfg.OrgEnabled || cfg.GovEnabled {
		t.Fatalf("default = %+v, %v", cfg, err)
	}
	// Enabled in production with mock providers: refused.
	m := map[string]string{EnvOrgEnabled: "true", EnvRegistryMode: "mock", EnvEmailMode: "mock"}
	for k, v := range base {
		m[k] = v
	}
	if _, err := ConfigFromEnv(env(m)); err == nil || !strings.Contains(err.Error(), EnvRegistryMode) {
		t.Fatalf("production mock registry accepted: %v", err)
	}
	m[EnvRegistryMode] = ""
	if _, err := ConfigFromEnv(env(m)); err == nil || !strings.Contains(err.Error(), "registry provider") {
		t.Fatalf("production without a registry accepted: %v", err)
	}
	// Development with mocks: allowed, and the dev code is fixed.
	dev := map[string]string{"APP_ENV": "dev", EnvOrgEnabled: "true", EnvRegistryMode: "mock", EnvEmailMode: "mock", EnvAuthURL: "http://auth", EnvInternalKey: "k"}
	cfg, err = ConfigFromEnv(env(dev))
	if err != nil || cfg.Registry == nil || cfg.FixedCode != MockEmailCode {
		t.Fatalf("dev = %+v, %v", cfg, err)
	}
	// Enabled without the role read: refused.
	delete(dev, EnvAuthURL)
	if _, err := ConfigFromEnv(env(dev)); err == nil {
		t.Fatal("enabled without AUTH_SERVICE_URL")
	}
	// Mock email in production: refused even when the registry is fine.
	if _, _, err := MailerFromEnv(env(nil), true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := MailerFromEnv(env(map[string]string{EnvEmailMode: "mock"}), true); err == nil {
		t.Fatal("production mock mailer accepted")
	}
}

func TestCodes(t *testing.T) {
	s := New(Config{}, nil, nil, nil, nil)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := s.newCode()
		if err != nil || len(c) != 6 {
			t.Fatalf("code %q %v", c, err)
		}
		seen[c] = true
	}
	if len(seen) < 45 {
		t.Fatal("codes repeat")
	}
	a, b := uuid.New(), uuid.New()
	if codeHash(a, "123456") == codeHash(b, "123456") {
		t.Fatal("code hash not bound to the application")
	}
	c, _ := newInviteCode()
	if !validInviteCode(c) || validInviteCode(strings.TrimPrefix(c, invitePrefix)) {
		t.Fatal("invite code format")
	}
}

func TestOrganisation_DailySubmissionCap(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < MaxApplicationsPerDay; i++ {
		if _, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest()); err != nil {
			t.Fatalf("submission %d: %v", i+1, err)
		}
		f.clock = f.clock.Add(time.Minute)
	}
	_, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest())
	wantRefusal(t, err, "TOO_MANY_APPLICATIONS")
	f.clock = f.clock.Add(24 * time.Hour)
	if _, err := f.svc.SubmitOrganisation(context.Background(), applicant, goodRequest()); err != nil {
		t.Fatalf("after a day: %v", err)
	}
}
