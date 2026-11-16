package officialverify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// addApp puts an application straight into the fake store.
func (f *fixture) addApp(status string, at time.Time, withDoc bool) uuid.UUID {
	id := uuid.New()
	a := &store.OrgApplication{ID: id, UserID: applicant, LegalName: "Acme Pvt. Ltd.", RegistrationKind: "gstin",
		RegistrationNumber: validGSTIN, OfficialDomain: "acme.co.in", SignatoryEmail: "asha@acme.co.in",
		Status: status, CreatedAt: at, EmailCodeHash: "never-shown"}
	if withDoc {
		doc := uuid.New()
		a.DocumentMediaID = &doc
	} else {
		a.RegistryConfirmed, a.RegistryName, a.RegistryProvider = true, "ACME PRIVATE LIMITED", "test"
	}
	f.st.apps[id] = a
	return id
}

func TestListApplications_InReviewOldestFirstAndPages(t *testing.T) {
	f := newFixture(t)
	var want []uuid.UUID
	for i := 0; i < 5; i++ {
		want = append(want, f.addApp(store.OrgAppInReview, t0.Add(time.Duration(i)*time.Minute), true))
	}
	f.addApp(store.OrgAppApproved, t0, false)

	var got []uuid.UUID
	cursor := ""
	for page := 0; page < 5; page++ {
		p, err := f.svc.ListApplications(context.Background(), ListQuery{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			got = append(got, it.ApplicationID)
			if it.Status != store.OrgAppInReview || !it.HasDocument || it.Applicant.Username != "asha_rao" {
				t.Fatalf("row = %+v", it)
			}
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows, want 5: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want oldest first %v", got, want)
		}
	}
}

func TestListApplications_OtherStatusesNewestFirst(t *testing.T) {
	f := newFixture(t)
	old := f.addApp(store.OrgAppApproved, t0, false)
	recent := f.addApp(store.OrgAppApproved, t0.Add(time.Hour), false)
	p, err := f.svc.ListApplications(context.Background(), ListQuery{Status: store.OrgAppApproved})
	if err != nil || len(p.Items) != 2 || p.Items[0].ApplicationID != recent || p.Items[1].ApplicationID != old || p.NextCursor != nil {
		t.Fatalf("page = %+v, %v", p, err)
	}
	if p.Items[0].HasDocument || !p.Items[0].RegistryConfirmed {
		t.Fatalf("row = %+v", p.Items[0])
	}
}

func TestListApplications_QueryRefusals(t *testing.T) {
	f := newFixture(t)
	f.addApp(store.OrgAppInReview, t0, true)
	f.addApp(store.OrgAppInReview, t0.Add(time.Minute), true)
	p, err := f.svc.ListApplications(context.Background(), ListQuery{Limit: 1})
	if err != nil || p.NextCursor == nil {
		t.Fatalf("page = %+v, %v", p, err)
	}
	for name, q := range map[string]ListQuery{
		"unknown status":   {Status: "superseded"},
		"limit too big":    {Limit: 51},
		"limit negative":   {Limit: -1},
		"garbage cursor":   {Cursor: "not-a-cursor!"},
		"other status's":   {Status: store.OrgAppApproved, Cursor: *p.NextCursor},
		"over-long cursor": {Cursor: strings.Repeat("A", 300)},
	} {
		_, err := f.svc.ListApplications(context.Background(), q)
		var e *Error
		if !asError(err, &e) || e.Code != "INVALID_REQUEST" || e.Status != 400 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	calls := f.st.listCalls
	if _, err := f.svc.ListApplications(context.Background(), ListQuery{Status: "superseded"}); err == nil || f.st.listCalls != calls {
		t.Fatal("a refused query reached the store")
	}
}

func TestGetApplication_DetailAndJSONShape(t *testing.T) {
	f := newFixture(t)
	id := f.addApp(store.OrgAppInReview, t0, true)
	app, err := f.svc.GetApplication(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if app.RegistrationNumber != validGSTIN || app.SignatoryEmail != "asha@acme.co.in" || !app.Applicant.IsPersonVerified ||
		app.ReviewerID != nil || app.ReviewedAt != nil {
		t.Fatalf("detail = %+v", app)
	}
	b, _ := json.Marshal(app)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	applicantObj, _ := m["applicant"].(map[string]any)
	if applicantObj["is_person_verified"] != true || applicantObj["username"] != "asha_rao" {
		t.Fatalf("applicant = %v", m["applicant"])
	}
	for _, never := range []string{"email_code_hash", "never-shown", "document_media_id"} {
		if strings.Contains(string(b), never) {
			t.Fatalf("detail carries %q: %s", never, b)
		}
	}
	_, err = f.svc.GetApplication(context.Background(), uuid.New())
	wantRefusal(t, err, "APPLICATION_NOT_FOUND")
}

func TestApplicationDocument(t *testing.T) {
	f := newFixture(t)
	withDoc := f.addApp(store.OrgAppInReview, t0, true)
	without := f.addApp(store.OrgAppApproved, t0, false)
	got, err := f.svc.ApplicationDocument(context.Background(), withDoc)
	if err != nil || got != *f.st.apps[withDoc].DocumentMediaID {
		t.Fatalf("document = %v, %v", got, err)
	}
	_, err = f.svc.ApplicationDocument(context.Background(), without)
	wantRefusal(t, err, "DOCUMENT_NOT_FOUND")
	_, err = f.svc.ApplicationDocument(context.Background(), uuid.New())
	wantRefusal(t, err, "APPLICATION_NOT_FOUND")
}

func TestStats_ThirtyDayWindow(t *testing.T) {
	f := newFixture(t)
	f.addApp(store.OrgAppInReview, t0, true)
	if _, err := f.svc.Stats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.st.statsNow.Equal(t0) || !f.st.statsSince.Equal(t0.Add(-30*24*time.Hour)) {
		t.Fatalf("window = %v..%v", f.st.statsSince, f.st.statsNow)
	}
	st, _ := f.svc.Stats(context.Background())
	if st.InReview != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// Either mark on serves the strip.
	gov := New(Config{GovEnabled: true}, f.st, nil, nil, nil).WithClock(func() time.Time { return t0 })
	if _, err := gov.Stats(context.Background()); err != nil {
		t.Fatalf("government-only stats: %v", err)
	}
}

func TestInvitations_ListComputesExpiredAndRevokes(t *testing.T) {
	f := govFixture(t)
	inv, err := f.svc.CreateGovInvitation(context.Background(), invite(adminA, adminB))
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.ListInvitations(context.Background(), ListQuery{})
	if err != nil || len(p.Items) != 1 || p.Items[0].InvitationID != inv.InvitationID || p.Items[0].Status != "pending" ||
		p.Items[0].ApproverIDs != [2]uuid.UUID{adminA, adminB} || p.Items[0].Invitee.Username != "ministry_example" {
		t.Fatalf("pending = %+v, %v", p, err)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), inv.InviteCode) || strings.Contains(string(b), inviteHash(inv.InviteCode)) || strings.Contains(string(b), "code") {
		t.Fatalf("the list carries the code: %s", b)
	}

	// Past its expiry it is "expired", not pending, and cannot be revoked.
	f.clock = t0.Add(InvitationTTL + time.Second)
	if p, _ := f.svc.ListInvitations(context.Background(), ListQuery{}); len(p.Items) != 0 {
		t.Fatalf("pending still lists an expired invitation: %+v", p.Items)
	}
	p, _ = f.svc.ListInvitations(context.Background(), ListQuery{Status: "expired"})
	if len(p.Items) != 1 || p.Items[0].Status != "expired" {
		t.Fatalf("expired = %+v", p.Items)
	}
	_, err = f.svc.RevokeInvitation(context.Background(), inv.InvitationID, adminA, "body asked")
	wantRefusal(t, err, "INVITATION_NOT_PENDING")

	// A fresh one revokes, with the actor and reason recorded.
	f.clock = t0
	inv2, err := f.svc.CreateGovInvitation(context.Background(), invite(adminA, adminB))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.RevokeInvitation(context.Background(), inv2.InvitationID, adminB, " ")
	wantRefusal(t, err, "REASON_REQUIRED")
	out, err := f.svc.RevokeInvitation(context.Background(), inv2.InvitationID, adminB, "issued to the wrong account")
	if err != nil || out.Status != "revoked" {
		t.Fatalf("revoke = %+v, %v", out, err)
	}
	if f.st.revokedBy[inv2.InvitationID] != adminB || f.st.revokeReason[inv2.InvitationID] != "issued to the wrong account" {
		t.Fatal("revoke not attributed")
	}
	_, err = f.svc.RevokeInvitation(context.Background(), inv2.InvitationID, adminB, "again")
	wantRefusal(t, err, "INVITATION_NOT_PENDING")
	_, err = f.svc.RevokeInvitation(context.Background(), uuid.New(), adminB, "who")
	wantRefusal(t, err, "INVITATION_NOT_FOUND")
	// The revoked code no longer accepts.
	_, err = f.svc.AcceptGovInvitation(context.Background(), official, inv2.InviteCode)
	wantRefusal(t, err, "INVALID_INVITATION")
}

func TestInvitations_UnknownStatusRefused(t *testing.T) {
	f := govFixture(t)
	_, err := f.svc.ListInvitations(context.Background(), ListQuery{Status: "approved"})
	wantRefusal(t, err, "INVALID_REQUEST")
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}
