// A user's own cases (audit TS-M1, TS-L1, TS-L2, TS-L3), no database: the
// reporter, appellant, complainant and verification applicant never see who
// handled their case or the staff's notes, an officer still does, and the
// grievance quota and the copyright redirect answer as specified.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// staffID is the moderator's id planted in every fake row; it must never
// appear in a user-facing body.
var staffID = uuid.MustParse("5afe5afe-0000-4000-8000-00000000beef")

const staffNote = "internal: reporter is a known brigader, close quietly"

type fakeCases struct {
	reports    []postgres.Report
	appeals    []postgres.ContentAppeal
	grievance  *postgres.Grievance
	verify     *postgres.VerificationRequest
	verifyErr  error
	fileErr    error
	userFiled  int
	adminFiled int
}

func (f *fakeCases) FileReportWith(_ context.Context, in service.FileReportInput) (*postgres.Report, error) {
	r := f.reports[0]
	r.ReporterID = in.ReporterID
	return &r, nil
}
func (f *fakeCases) ListOwnReports(context.Context, uuid.UUID, int, int) ([]postgres.Report, error) {
	return f.reports, nil
}
func (f *fakeCases) ListUserAppeals(context.Context, uuid.UUID) ([]postgres.ContentAppeal, error) {
	return f.appeals, nil
}
func (f *fakeCases) FileGrievance(context.Context, uuid.UUID, string, string, string, string) (*postgres.Grievance, error) {
	f.adminFiled++
	return f.grievance, nil
}
func (f *fakeCases) FileUserGrievance(context.Context, uuid.UUID, string, string, string, string) (*postgres.Grievance, error) {
	f.userFiled++
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	return f.grievance, nil
}
func (f *fakeCases) GetGrievance(context.Context, uuid.UUID) (*postgres.Grievance, error) {
	return f.grievance, nil
}
func (f *fakeCases) ListMyGrievances(context.Context, uuid.UUID, int, int) ([]postgres.Grievance, error) {
	return []postgres.Grievance{*f.grievance}, nil
}
func (f *fakeCases) SubmitVerificationRequest(_ context.Context, userID uuid.UUID, vtype string, docs map[string]string) (*postgres.VerificationRequest, error) {
	return &postgres.VerificationRequest{ID: uuid.New(), UserID: userID, Type: vtype, Status: "pending", SubmittedDocs: docs}, nil
}
func (f *fakeCases) OwnVerificationRequest(context.Context, uuid.UUID) (*postgres.VerificationRequest, error) {
	return f.verify, f.verifyErr
}

func plantedCases(owner uuid.UUID) *fakeCases {
	now := time.Now().UTC()
	staff, note := staffID, staffNote
	about := uuid.New()
	aboutType := "post"
	return &fakeCases{
		reports: []postgres.Report{{
			ID: uuid.New(), ReporterID: owner, EntityType: "message", EntityID: uuid.New(), Reason: "spam",
			Status: "dismissed", AssignedTo: &staff, ResolvedAt: &now, ResolutionNotes: staffNote,
			Evidence:  json.RawMessage(`{"text":"hi","source":"message_service","sender_id":"` + staffID.String() + `"}`),
			CreatedAt: now, UpdatedAt: now,
		}},
		appeals: []postgres.ContentAppeal{{
			ID: uuid.New(), UserID: owner, ContentType: "post", ContentID: uuid.New(), Status: postgres.AppealUpheld,
			ReviewedBy: &staff, ResolutionNote: &note, AppealedDecisionID: &staff, OverturnDecisionID: &staff,
			SubmittedAt: now, ResolvedAt: &now,
		}},
		grievance: &postgres.Grievance{
			ID: uuid.New(), ComplainantID: owner, Subject: "privacy", AboutEntityType: &aboutType, AboutEntityID: &about,
			Description: "please take this down", Status: "acknowledged", AssignedTo: &staff,
			ResolutionNotes: staffNote, CreatedAt: now, DueAt: now, UpdatedAt: now,
		},
		verify: &postgres.VerificationRequest{
			ID: uuid.New(), UserID: owner, Type: "creator", Status: "more_info_needed",
			SubmittedDocs: map[string]string{"pan": "ABCDE1234F"}, RejectionReason: strPtr("Upload a clearer ID photo"),
			ReviewedBy: &staff, CreatedAt: now, UpdatedAt: now,
		},
	}
}

func strPtr(s string) *string { return &s }

// forbidden are the stored fields no user-facing body may carry.
var forbidden = []string{"assigned_to", "reviewed_by", "resolution_notes", "resolution_note",
	"evidence", "source", "appealed_decision_id", "overturn_decision_id", "submitted_docs"}

// assertUserSafe fails when body names a forbidden key or carries the
// staff member's id or note anywhere.
func assertUserSafe(t *testing.T, body string) {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, body)
	}
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			for k, child := range x {
				for _, bad := range forbidden {
					if k == bad {
						t.Errorf("user-facing body carries %q: %s", k, body)
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(v)
	if strings.Contains(body, staffID.String()) || strings.Contains(body, "brigader") {
		t.Errorf("user-facing body leaks the staff id or note: %s", body)
	}
}

func casesRig(t *testing.T, f *fakeCases) func(method, path, body string, hdr map[string]string) (int, string) {
	t.Helper()
	h := New(nil)
	h.cases = f
	h.appeals = &fakeAppeals{}
	r := newTokenTestRouter(t, h)
	return func(method, path, body string, hdr map[string]string) (int, string) {
		w := serveAdmin(r, method, path, body, hdr)
		return w.Code, w.Body.String()
	}
}

func TestUserCaseReadsAreReporterSafe(t *testing.T) {
	owner := uuid.New()
	do := casesRig(t, plantedCases(owner))
	g := plantedCases(owner).grievance
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/reports?mine=true", ""},
		{http.MethodPost, "/v1/reports", `{"entity_type":"post","entity_id":"` + uuid.NewString() + `","reason":"spam"}`},
		{http.MethodGet, "/v1/appeals?mine=true", ""},
		{http.MethodGet, "/v1/grievances?mine=true", ""},
		{http.MethodGet, "/v1/grievances/" + g.ID.String(), ""},
		{http.MethodPost, "/v1/grievances", `{"subject":"privacy","description":"please take this down"}`},
		{http.MethodGet, "/v1/verification-requests?mine=true", ""},
		{http.MethodPost, "/v1/verification-requests", `{"type":"creator","docs":{"pan":"ABCDE1234F"}}`},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			code, body := do(rt.method, rt.path, rt.body, author(owner))
			if code >= 300 {
				t.Fatalf("status %d: %s", code, body)
			}
			assertUserSafe(t, body)
			if !strings.Contains(body, `"outcome"`) {
				t.Errorf("no outcome message: %s", body)
			}
		})
	}
}

func TestUserViewsCarryTheFixedOutcomeAndRole(t *testing.T) {
	owner := uuid.New()
	f := plantedCases(owner)
	ug := toUserGrievance(*f.grievance)
	if ug.HandledBy != GrievanceOfficerRole || ug.Outcome != grievanceOutcomes["acknowledged"] {
		t.Fatalf("grievance view = %+v", ug)
	}
	unassigned := *f.grievance
	unassigned.AssignedTo = nil
	if v := toUserGrievance(unassigned); v.HandledBy != "" {
		t.Fatalf("unassigned grievance names a handler: %+v", v)
	}
	if v := toUserReport(f.reports[0]); v.Outcome != reportOutcomes["dismissed"] {
		t.Fatalf("report outcome = %q", v.Outcome)
	}
	if v := toUserAppeal(f.appeals[0]); v.Outcome != appealOutcomes[postgres.AppealUpheld] {
		t.Fatalf("appeal outcome = %q", v.Outcome)
	}
	// The reviewer's reason reaches the applicant on more_info_needed and
	// rejected, and never on approved.
	if v := toUserVerification(*f.verify); v.Reason == nil || *v.Reason != "Upload a clearer ID photo" {
		t.Fatalf("more_info_needed reason = %v", v.Reason)
	}
	approved := *f.verify
	approved.Status = "approved"
	if v := toUserVerification(approved); v.Reason != nil {
		t.Fatalf("approved carries a reason: %v", *v.Reason)
	}
}

// An officer still sees the full grievance row: the admin token on the
// token family and the legacy admin scope on the key-gated route.
func TestOfficerKeepsTheFullGrievance(t *testing.T) {
	owner := uuid.New()
	f := plantedCases(owner)
	rg := newAdminTokenRig(t)
	h := New(nil).WithServiceAuth(rg.v)
	h.cases = f
	r := newTokenTestRouter(t, h)
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermGrievancesRead}, rg.actor.String())
	w := serveAdmin(r, http.MethodGet, InternalAdminPrefix+"/grievances/"+f.grievance.ID.String(), "", bearer(tok))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"assigned_to":"`+staffID.String()) {
		t.Fatalf("token officer: %d %s", w.Code, w.Body.String())
	}
	w = serveAdmin(r, http.MethodGet, "/v1/grievances/"+f.grievance.ID.String(), "", gatewayAdmin(uuid.New(), "admin"))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"resolution_notes":"`+staffNote) {
		t.Fatalf("scope officer: %d %s", w.Code, w.Body.String())
	}
}

// TS-L1: the user route goes through the quota; the officer's admin-token
// filing does not.
func TestGrievanceFilingRoutesThroughTheQuota(t *testing.T) {
	owner := uuid.New()
	f := plantedCases(owner)
	rg := newAdminTokenRig(t)
	h := New(nil).WithServiceAuth(rg.v)
	h.cases = f
	r := newTokenTestRouter(t, h)
	body := `{"subject":"privacy","description":"please take this down"}`

	if w := serveAdmin(r, http.MethodPost, "/v1/grievances", body, author(owner)); w.Code != http.StatusOK || f.userFiled != 1 || f.adminFiled != 0 {
		t.Fatalf("user filing: %d user=%d admin=%d %s", w.Code, f.userFiled, f.adminFiled, w.Body.String())
	}
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermGrievancesAct}, rg.actor.String())
	if w := serveAdmin(r, http.MethodPost, InternalAdminPrefix+"/grievances", body, bearer(tok)); w.Code != http.StatusOK || f.adminFiled != 1 || f.userFiled != 1 {
		t.Fatalf("officer filing: %d user=%d admin=%d %s", w.Code, f.userFiled, f.adminFiled, w.Body.String())
	}

	cases := []struct {
		err      error
		status   int
		code     string
		leakFree bool
	}{
		{service.ErrGrievanceRateLimited, http.StatusTooManyRequests, "RATE_LIMITED", true},
		{service.ErrActiveGrievanceExists, http.StatusConflict, "ACTIVE_GRIEVANCE_EXISTS", true},
		{errors.New("pq: relation trust.grievances is locked by pid 42"), http.StatusInternalServerError, "INTERNAL_ERROR", true},
	}
	for _, tc := range cases {
		f.fileErr = tc.err
		w := serveAdmin(r, http.MethodPost, "/v1/grievances", body, author(owner))
		if w.Code != tc.status || adminErrorCode(w) != tc.code {
			t.Fatalf("%v: %d %s", tc.err, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "pid 42") {
			t.Fatalf("store error text reached the client: %s", w.Body.String())
		}
	}
}

// TS-L3: ?mine=true is the caller's own request; without it the route is
// still the admin queue.
func TestOwnVerificationRequest(t *testing.T) {
	owner := uuid.New()
	f := plantedCases(owner)
	do := casesRig(t, f)
	code, body := do(http.MethodGet, "/v1/verification-requests?mine=true", "", author(owner))
	if code != http.StatusOK || !strings.Contains(body, `"status":"more_info_needed"`) || !strings.Contains(body, "clearer ID") {
		t.Fatalf("own request: %d %s", code, body)
	}
	f.verify = nil
	if code, body = do(http.MethodGet, "/v1/verification-requests?mine=true", "", author(owner)); code != http.StatusOK || !strings.Contains(body, `"items":[]`) {
		t.Fatalf("no request: %d %s", code, body)
	}
	f.verifyErr = errors.New("db down")
	if code, body = do(http.MethodGet, "/v1/verification-requests?mine=true", "", author(owner)); code != http.StatusInternalServerError || strings.Contains(body, "db down") {
		t.Fatalf("failed read: %d %s", code, body)
	}
	if code, _ = do(http.MethodGet, "/v1/verification-requests", "", author(owner)); code != http.StatusForbidden {
		t.Fatalf("queue without admin scope: %d", code)
	}
	if code, _ = do(http.MethodGet, "/v1/verification-requests?mine=true", "", map[string]string{"X-Internal-Service-Key": tokenTestInternalKey}); code != http.StatusUnauthorized {
		t.Fatalf("no user id: %d", code)
	}
}

// TS-L2: a copyright-held post is sent to the grievance route that exists,
// with the body to file there; the code stays the one clients branch on.
func TestCopyrightAppealPointsAtTheGrievanceRoute(t *testing.T) {
	h := New(nil)
	h.appeals = &fakeAppeals{submitErr: service.ErrAppealCopyrightCase}
	r := newTokenTestRouter(t, h)
	w := serveAdmin(r, http.MethodPost, "/v1/appeals", appealBody, author(uuid.New()))
	if w.Code != http.StatusConflict || adminErrorCode(w) != CodeAppealCopyrightCase {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Details struct {
				Path      string            `json:"grievance_path"`
				Grievance map[string]string `json:"grievance"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(env.Error.Message), "counter-notice") {
		t.Fatalf("message still names a process that does not exist: %q", env.Error.Message)
	}
	d := env.Error.Details
	if d.Path != GrievancePath || d.Grievance["subject"] != "intellectual_property" ||
		d.Grievance["about_entity_type"] != "post" || d.Grievance["about_entity_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("details = %+v", d)
	}
	// The named path is the route that is registered (a bad body is a 400
	// from FileGrievance, not a 404).
	if w := serveAdmin(r, http.MethodPost, GrievancePath, `{}`, author(uuid.New())); w.Code == http.StatusNotFound {
		t.Fatalf("%s is not a registered route", GrievancePath)
	}
}

// plantedAppeals answers a submission with a row carrying a reviewer, a
// note and decision ids (a resubmission's read-back can), so the response
// shape itself is what is under test.
type plantedAppeals struct{ fakeAppeals }

func (p *plantedAppeals) SubmitAppeal(_ context.Context, userID uuid.UUID, _, _, _ string, _ *uuid.UUID) (*postgres.ContentAppeal, error) {
	a := plantedCases(userID).appeals[0]
	return &a, nil
}

func TestSubmittedAppealIsAppellantSafe(t *testing.T) {
	h := New(nil)
	h.appeals = &plantedAppeals{}
	r := newTokenTestRouter(t, h)
	w := serveAdmin(r, http.MethodPost, "/v1/appeals", appealBody, author(uuid.New()))
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	assertUserSafe(t, w.Body.String())
}
