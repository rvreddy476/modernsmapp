package officialverify

import (
	"context"
	"errors"
	"sort"
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
	mu           sync.Mutex
	profiles     map[uuid.UUID]*store.Profile
	apps         map[uuid.UUID]*store.OrgApplication
	invites      map[string]*store.GovInvitation
	inviteState  map[string]string
	heldByOther  bool
	reviewReason map[uuid.UUID]string
	completedAt  map[uuid.UUID]*time.Time
	revokedBy    map[uuid.UUID]uuid.UUID
	revokeReason map[uuid.UUID]string
	listCalls    int
	statsSince   time.Time
	statsNow     time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{profiles: map[uuid.UUID]*store.Profile{}, apps: map[uuid.UUID]*store.OrgApplication{},
		invites: map[string]*store.GovInvitation{}, inviteState: map[string]string{},
		reviewReason: map[uuid.UUID]string{}, completedAt: map[uuid.UUID]*time.Time{},
		revokedBy: map[uuid.UUID]uuid.UUID{}, revokeReason: map[uuid.UUID]string{}}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (f *fakeStore) addPerson(id uuid.UUID, name string) {
	handle := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
	f.profiles[id] = &store.Profile{UserID: id, Username: &handle, DisplayName: name, IsVerified: true,
		VerificationLevel: verification.LevelPerson, BadgeFlags: verification.BadgePersonVerified}
}

func (f *fakeStore) closed(id uuid.UUID, reason string, now time.Time) {
	f.reviewReason[id] = reason
	f.completedAt[id] = &now
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

func (f *fakeStore) RejectOrgApplication(_ context.Context, id, reviewer uuid.UUID, reason string, now time.Time) error {
	a, ok := f.apps[id]
	if !ok || a.Status != store.OrgAppInReview {
		return store.ErrApplicationGone
	}
	a.Status, a.ReviewerID = store.OrgAppRejected, &reviewer
	f.closed(id, reason, now)
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
	f.closed(a.ID, in.ReviewReason, in.Now)
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

func (f *fakeStore) adminApp(a *store.OrgApplication) store.AdminOrgApplication {
	out := store.AdminOrgApplication{OrgApplication: *a, ReviewReason: f.reviewReason[a.ID], CompletedAt: f.completedAt[a.ID]}
	if p := f.profiles[a.UserID]; p != nil {
		out.Applicant = store.MarksApplicant{Username: deref(p.Username), DisplayName: p.DisplayName, IsVerified: p.IsVerified,
			VerificationLevel: p.VerificationLevel, BadgeFlags: p.BadgeFlags}
	}
	return out
}

func (f *fakeStore) ListOrgApplicationsAdmin(_ context.Context, status string, limit int, after *store.Keyset) ([]store.AdminOrgApplication, error) {
	f.listCalls++
	asc := status == store.OrgAppInReview
	var rows []store.AdminOrgApplication
	for _, a := range f.apps {
		if a.Status == status {
			rows = append(rows, f.adminApp(a))
		}
	}
	less := func(x, y store.AdminOrgApplication) bool {
		if !x.CreatedAt.Equal(y.CreatedAt) {
			return x.CreatedAt.Before(y.CreatedAt)
		}
		return x.ID.String() < y.ID.String()
	}
	sort.Slice(rows, func(i, j int) bool {
		if asc {
			return less(rows[i], rows[j])
		}
		return less(rows[j], rows[i])
	})
	out := []store.AdminOrgApplication{}
	for _, r := range rows {
		if after != nil {
			k := store.AdminOrgApplication{OrgApplication: store.OrgApplication{ID: after.ID, CreatedAt: after.CreatedAt}}
			if asc && !less(k, r) || !asc && !less(r, k) {
				continue
			}
		}
		out = append(out, r)
		if len(out) == limit+1 {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) GetOrgApplicationAdmin(_ context.Context, id uuid.UUID) (*store.AdminOrgApplication, error) {
	a, ok := f.apps[id]
	if !ok {
		return nil, nil
	}
	out := f.adminApp(a)
	return &out, nil
}

func (f *fakeStore) MarksStats(_ context.Context, since, now time.Time) (*store.MarksStats, error) {
	f.statsSince, f.statsNow = since, now
	m := &store.MarksStats{}
	for _, a := range f.apps {
		if a.Status == store.OrgAppInReview {
			m.InReview++
		}
		if c := f.completedAt[a.ID]; c != nil && !c.Before(since) {
			switch a.Status {
			case store.OrgAppApproved:
				m.ApprovedSince++
			case store.OrgAppRejected:
				m.RejectedSince++
			}
		}
	}
	for h, g := range f.invites {
		if f.inviteState[h] == "pending" && g.ExpiresAt.After(now) {
			m.InvitationsPending++
		}
	}
	return m, nil
}

func (f *fakeStore) adminGov(h string, now time.Time) store.AdminGovInvitation {
	g := f.invites[h]
	st := f.inviteState[h]
	if st == "pending" && !g.ExpiresAt.After(now) {
		st = store.InvitationExpired
	}
	out := store.AdminGovInvitation{ID: g.ID, UserID: g.UserID, OfficialName: g.OfficialName, Status: st,
		ApproverOne: g.ApproverOne, ApproverTwo: g.ApproverTwo, Reason: g.Reason, ExpiresAt: g.ExpiresAt, CreatedAt: g.CreatedAt}
	if p := f.profiles[g.UserID]; p != nil {
		out.Invitee = store.MarksApplicant{Username: deref(p.Username), DisplayName: p.DisplayName, VerificationLevel: p.VerificationLevel}
	}
	return out
}

func (f *fakeStore) ListGovInvitationsAdmin(_ context.Context, status string, limit int, after *store.Keyset, now time.Time) ([]store.AdminGovInvitation, error) {
	var rows []store.AdminGovInvitation
	for h := range f.invites {
		if g := f.adminGov(h, now); g.Status == status {
			rows = append(rows, g)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID.String() > rows[j].ID.String()
	})
	out := []store.AdminGovInvitation{}
	for _, r := range rows {
		if after != nil && !(r.CreatedAt.Before(after.CreatedAt) || r.CreatedAt.Equal(after.CreatedAt) && r.ID.String() < after.ID.String()) {
			continue
		}
		out = append(out, r)
		if len(out) == limit+1 {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) GetGovInvitationAdmin(_ context.Context, id uuid.UUID, now time.Time) (*store.AdminGovInvitation, error) {
	for h, g := range f.invites {
		if g.ID == id {
			out := f.adminGov(h, now)
			return &out, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) RevokeGovInvitation(_ context.Context, id, by uuid.UUID, reason string, now time.Time) error {
	for h, g := range f.invites {
		if g.ID != id {
			continue
		}
		if f.inviteState[h] != "pending" || !g.ExpiresAt.After(now) {
			return store.ErrInvitationNotPending
		}
		f.inviteState[h] = "revoked"
		f.revokedBy[id], f.revokeReason[id] = by, reason
		return nil
	}
	return store.ErrInvitationNotFound
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
		pub:   &fakePub{},
		clock: t0,
	}
	f.st.addPerson(applicant, "Asha Rao")
	f.svc = New(Config{OrgEnabled: true, GovEnabled: true, Registry: f.reg, Mailer: f.mail},
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

func approve(reviewer uuid.UUID) ReviewDecision {
	return ReviewDecision{Decision: "approve", ReviewerID: reviewer, Reason: "GST certificate checked"}
}

func TestReview_ApprovesAndReturnsTheApplication(t *testing.T) {
	f, id := reviewFixture(t)
	f.clock = t0.Add(time.Hour)
	app, err := f.svc.ReviewOrganisation(context.Background(), id, approve(adminA))
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != store.OrgAppApproved || app.ReviewerID == nil || *app.ReviewerID != adminA ||
		app.ReviewReason != "GST certificate checked" || app.ReviewedAt == nil || !app.ReviewedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("application = %+v", app)
	}
	if p := f.st.profiles[applicant]; p.VerificationLevel != verification.LevelOrganisation || p.OrganisationName != "Acme Pvt. Ltd." {
		t.Fatalf("profile = %+v", p)
	}
	if len(f.pub.events) != 1 || f.pub.events[0].Reason != verification.ReasonOrganisationVerified {
		t.Fatalf("events = %+v", f.pub.events)
	}
}

func TestReview_AnyReviewerButTheApplicant(t *testing.T) {
	// No admin-role read any more: the token's scope is the authority, so
	// any reviewer who is not the applicant decides.
	f, id := reviewFixture(t)
	if _, err := f.svc.ReviewOrganisation(context.Background(), id, approve(notAdmin)); err != nil {
		t.Fatalf("review by a scoped reviewer: %v", err)
	}
}

func TestReview_ApplicantCannotReviewThemselves(t *testing.T) {
	f, id := reviewFixture(t)
	_, err := f.svc.ReviewOrganisation(context.Background(), id, approve(applicant))
	wantRefusal(t, err, "REVIEWER_IS_APPLICANT")
	_, err = f.svc.ReviewOrganisation(context.Background(), id, approve(uuid.Nil))
	wantRefusal(t, err, "REVIEWER_IS_APPLICANT")
	if f.st.apps[id].Status != store.OrgAppInReview {
		t.Fatal("a refused review changed the application")
	}
}

func TestReview_ReasonRequired(t *testing.T) {
	f, id := reviewFixture(t)
	for name, reason := range map[string]string{
		"empty": "", "blank": "   \n", "too long": strings.Repeat("é", 501),
	} {
		d := approve(adminA)
		d.Reason = reason
		_, err := f.svc.ReviewOrganisation(context.Background(), id, d)
		var e *Error
		if !errors.As(err, &e) || e.Code != "REASON_REQUIRED" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	d := approve(adminA)
	d.Reason = strings.Repeat("é", 500)
	if _, err := f.svc.ReviewOrganisation(context.Background(), id, d); err != nil {
		t.Fatalf("500 runes refused: %v", err)
	}
}

func TestReview_RejectAndStateChecks(t *testing.T) {
	f, id := reviewFixture(t)
	d := approve(adminA)
	d.Decision = "maybe"
	_, err := f.svc.ReviewOrganisation(context.Background(), id, d)
	wantRefusal(t, err, "INVALID_DECISION")
	d.Decision = "reject"
	app, err := f.svc.ReviewOrganisation(context.Background(), id, d)
	if err != nil || app.Status != store.OrgAppRejected || app.ReviewerID == nil || *app.ReviewerID != adminA {
		t.Fatalf("reject = %+v, %v", app, err)
	}
	if f.st.profiles[applicant].VerificationLevel != verification.LevelPerson {
		t.Fatal("a rejection set the mark")
	}
	_, err = f.svc.ReviewOrganisation(context.Background(), id, approve(adminA))
	wantRefusal(t, err, "APPLICATION_NOT_IN_REVIEW")
	_, err = f.svc.ReviewOrganisation(context.Background(), uuid.New(), approve(adminA))
	wantRefusal(t, err, "APPLICATION_NOT_FOUND")
}

func TestReview_ApplicantLostThePersonMark(t *testing.T) {
	f, id := reviewFixture(t)
	f.st.profiles[applicant].BadgeFlags = 0
	_, err := f.svc.ReviewOrganisation(context.Background(), id, approve(adminA))
	var e *Error
	if !errors.As(err, &e) || e.Code != "PERSON_MARK_REQUIRED" || e.Status != 422 {
		t.Fatalf("err = %v", err)
	}
	if a := f.st.apps[id]; a.Status != store.OrgAppRefused || a.RefusalCode != "PERSON_MARK_REQUIRED" {
		t.Fatalf("application = %s %s, want refused", a.Status, a.RefusalCode)
	}
}

func TestOrganisation_Disabled(t *testing.T) {
	svc := New(Config{}, newFakeStore(), nil, nil, nil)
	ctx := context.Background()
	_, err := svc.SubmitOrganisation(ctx, applicant, goodRequest())
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.AcceptGovInvitation(ctx, applicant, "GOV-X")
	wantRefusal(t, err, "GOVERNMENT_VERIFICATION_DISABLED")
	// The console family answers the same refusals.
	_, err = svc.Stats(ctx)
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.ListApplications(ctx, ListQuery{})
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.GetApplication(ctx, uuid.New())
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.ApplicationDocument(ctx, uuid.New())
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.ReviewOrganisation(ctx, uuid.New(), approve(adminA))
	wantRefusal(t, err, "ORG_VERIFICATION_DISABLED")
	_, err = svc.ListInvitations(ctx, ListQuery{})
	wantRefusal(t, err, "GOVERNMENT_VERIFICATION_DISABLED")
	_, err = svc.CreateGovInvitation(ctx, invite(adminA, adminB))
	wantRefusal(t, err, "GOVERNMENT_VERIFICATION_DISABLED")
	_, err = svc.RevokeInvitation(ctx, uuid.New(), adminA, "r")
	wantRefusal(t, err, "GOVERNMENT_VERIFICATION_DISABLED")
}

func govFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	handle := "ministry_example"
	f.st.profiles[official] = &store.Profile{UserID: official, Username: &handle, DisplayName: "Some Name"}
	return f
}

// invite is a create request as admin-service sends it: requestedBy in the
// body, the approver from the token's act.
func invite(requestedBy, approver uuid.UUID) GovInvitationRequest {
	return GovInvitationRequest{UserID: official, OfficialName: "Ministry of Example Affairs", Reason: "MoU 12",
		RequestedBy: requestedBy, ApproverID: approver}
}

func TestGovernment_TwoAdminsInviteAndTheUserAccepts(t *testing.T) {
	f := govFixture(t)
	inv, err := f.svc.CreateGovInvitation(context.Background(), invite(adminA, adminB))
	if err != nil || !strings.HasPrefix(inv.InviteCode, "GOV-") || !inv.ExpiresAt.Equal(t0.Add(InvitationTTL)) {
		t.Fatalf("invite = %+v, %v", inv, err)
	}
	for _, g := range f.st.invites {
		if g.ApproverOne != adminA || g.ApproverTwo != adminB || g.Reason != "MoU 12" {
			t.Fatalf("stored = %+v, want approvers [requested_by, act]", g)
		}
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

func TestGovernment_TwoApproversMustDiffer(t *testing.T) {
	for name, pair := range map[string][2]uuid.UUID{
		"same":         {adminA, adminA},
		"no requester": {uuid.Nil, adminB},
		"no act":       {adminA, uuid.Nil},
		"neither":      {uuid.Nil, uuid.Nil},
	} {
		f := govFixture(t)
		_, err := f.svc.CreateGovInvitation(context.Background(), invite(pair[0], pair[1]))
		var e *Error
		if !errors.As(err, &e) || e.Code != "TWO_APPROVERS_REQUIRED" {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(f.st.invites) != 0 {
			t.Errorf("%s: an invitation was created", name)
		}
	}
}

func TestGovernment_InviteeIsNotAnApprover(t *testing.T) {
	for name, pair := range map[string][2]uuid.UUID{
		"requester": {official, adminB},
		"approver":  {adminA, official},
	} {
		f := govFixture(t)
		_, err := f.svc.CreateGovInvitation(context.Background(), invite(pair[0], pair[1]))
		var e *Error
		if !errors.As(err, &e) || e.Code != "APPROVER_IS_INVITEE" {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(f.st.invites) != 0 {
			t.Errorf("%s: an invitation was created", name)
		}
	}
}

func TestGovernment_CreateRefusals(t *testing.T) {
	f := govFixture(t)
	req := invite(adminA, adminB)
	req.Reason = " "
	_, err := f.svc.CreateGovInvitation(context.Background(), req)
	wantRefusal(t, err, "REASON_REQUIRED")
	req = invite(adminA, adminB)
	req.OfficialName = "x"
	_, err = f.svc.CreateGovInvitation(context.Background(), req)
	wantRefusal(t, err, "INVALID_OFFICIAL_NAME")
	req = invite(adminA, adminB)
	req.UserID = uuid.New()
	_, err = f.svc.CreateGovInvitation(context.Background(), req)
	wantRefusal(t, err, "PROFILE_NOT_FOUND")
	f.st.profiles[official].IsVerified, f.st.profiles[official].VerificationLevel = true, verification.LevelGovernment
	_, err = f.svc.CreateGovInvitation(context.Background(), invite(adminA, adminB))
	wantRefusal(t, err, "ALREADY_GOVERNMENT")
	if len(f.st.invites) != 0 {
		t.Fatal("an invitation was created")
	}
}

func TestGovernment_ExpiredOrMalformedCodes(t *testing.T) {
	f := govFixture(t)
	inv, err := f.svc.CreateGovInvitation(context.Background(), invite(adminA, adminB))
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
	base := map[string]string{"APP_ENV": "production"}

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
	// No AUTH_SERVICE_URL or internal key is needed any more: there is no
	// admin-role read (the console's token carries the permission).
	dev := map[string]string{"APP_ENV": "dev", EnvOrgEnabled: "true", EnvRegistryMode: "mock", EnvEmailMode: "mock"}
	cfg, err = ConfigFromEnv(env(dev))
	if err != nil || cfg.Registry == nil || cfg.FixedCode != MockEmailCode {
		t.Fatalf("dev = %+v, %v", cfg, err)
	}
	if cfg, err := ConfigFromEnv(env(map[string]string{"APP_ENV": "production", EnvGovEnabled: "true"})); err != nil || !cfg.GovEnabled {
		t.Fatalf("government alone in production = %+v, %v", cfg, err)
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
