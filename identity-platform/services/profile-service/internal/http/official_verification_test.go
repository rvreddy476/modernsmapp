package http

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/identity-profile-service/internal/officialverify"
	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/verification"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// stubOfficial records what reached the service. err, when set, is every
// console method's answer.
type stubOfficial struct {
	calls     int
	gotUser   uuid.UUID
	gotID     uuid.UUID
	gotActor  uuid.UUID
	gotReason string
	gotQuery  officialverify.ListQuery
	gotReview officialverify.ReviewDecision
	gotInvite officialverify.GovInvitationRequest
	err       error
}

func (s *stubOfficial) SubmitOrganisation(_ context.Context, u uuid.UUID, _ officialverify.OrgApplicationRequest) (*officialverify.OrgStatus, error) {
	s.gotUser = u
	return &officialverify.OrgStatus{Next: "email_code"}, nil
}
func (s *stubOfficial) ConfirmOrganisationEmail(_ context.Context, u uuid.UUID, _ string) (*officialverify.OrgStatus, error) {
	s.gotUser = u
	return &officialverify.OrgStatus{}, nil
}
func (s *stubOfficial) OrganisationStatus(_ context.Context, u uuid.UUID) (*officialverify.OrgStatus, error) {
	s.gotUser = u
	return &officialverify.OrgStatus{Next: "apply"}, nil
}
func (s *stubOfficial) AcceptGovInvitation(_ context.Context, u uuid.UUID, _ string) (*officialverify.OrgStatus, error) {
	s.gotUser = u
	return &officialverify.OrgStatus{VerificationLevel: "government"}, nil
}
func (s *stubOfficial) Stats(context.Context) (*officialverify.MarksStats, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.MarksStats{InReview: 3}, nil
}
func (s *stubOfficial) ListApplications(_ context.Context, q officialverify.ListQuery) (*officialverify.ApplicationPage, error) {
	s.calls++
	s.gotQuery = q
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.ApplicationPage{Items: []officialverify.ApplicationSummary{}}, nil
}
func (s *stubOfficial) GetApplication(_ context.Context, id uuid.UUID) (*officialverify.Application, error) {
	s.calls++
	s.gotID = id
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.Application{ApplicationSummary: officialverify.ApplicationSummary{ApplicationID: id}}, nil
}
func (s *stubOfficial) ApplicationDocument(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	s.calls++
	s.gotID = id
	if s.err != nil {
		return uuid.Nil, s.err
	}
	return stubDocumentID, nil
}
func (s *stubOfficial) ReviewOrganisation(_ context.Context, id uuid.UUID, d officialverify.ReviewDecision) (*officialverify.Application, error) {
	s.calls++
	s.gotID, s.gotReview = id, d
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.Application{ApplicationSummary: officialverify.ApplicationSummary{ApplicationID: id, Status: "approved"}}, nil
}
func (s *stubOfficial) ListInvitations(_ context.Context, q officialverify.ListQuery) (*officialverify.InvitationPage, error) {
	s.calls++
	s.gotQuery = q
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.InvitationPage{Items: []officialverify.Invitation{}}, nil
}
func (s *stubOfficial) CreateGovInvitation(_ context.Context, req officialverify.GovInvitationRequest) (*officialverify.GovInvitationResult, error) {
	s.calls++
	s.gotInvite = req
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.GovInvitationResult{InvitationID: uuid.New(), InviteCode: "GOV-X"}, nil
}
func (s *stubOfficial) RevokeInvitation(_ context.Context, id, actor uuid.UUID, reason string) (*officialverify.Invitation, error) {
	s.calls++
	s.gotID, s.gotActor, s.gotReason = id, actor, reason
	if s.err != nil {
		return nil, s.err
	}
	return &officialverify.Invitation{InvitationID: id, Status: "revoked"}, nil
}

var stubDocumentID = uuid.MustParse("5a5a5a5a-0000-4000-8000-000000000001")

const testInternalKey = "test-internal-key"

func officialRouter(o OfficialVerification, key string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	if key != "" {
		h.WithInternalKey(key)
	}
	if o != nil {
		h.WithOfficialVerification(o)
	}
	pass := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, pass, pass)
	return r
}

func officialCall(r *gin.Engine, method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOfficialRoutes_UnwiredAnswer503(t *testing.T) {
	r := officialRouter(nil, "")
	user := map[string]string{"X-User-Id": uuid.NewString()}
	for _, c := range []struct{ method, path, code string }{
		{http.MethodGet, "/v1/profiles/me/verification/organisation", "ORG_VERIFICATION_DISABLED"},
		{http.MethodPost, "/v1/profiles/me/verification/organisation", "ORG_VERIFICATION_DISABLED"},
		{http.MethodPost, "/v1/profiles/me/verification/organisation/confirm-email", "ORG_VERIFICATION_DISABLED"},
		{http.MethodPost, "/v1/profiles/me/verification/government/accept", "GOVERNMENT_VERIFICATION_DISABLED"},
	} {
		w := officialCall(r, c.method, c.path, user, map[string]string{})
		if w.Code != http.StatusServiceUnavailable || decodeErrorCode(t, w) != c.code {
			t.Errorf("%s %s = %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

func TestOfficialRoutes_UserRoutesUseTheSignedInUser(t *testing.T) {
	o := &stubOfficial{}
	r := officialRouter(o, testInternalKey)
	u := uuid.New()
	w := officialCall(r, http.MethodPost, "/v1/profiles/me/verification/government/accept",
		map[string]string{"X-User-Id": u.String(), "X-Internal-Service-Key": testInternalKey}, map[string]string{"invite_code": "GOV-ABC"})
	if w.Code != http.StatusOK || o.gotUser != u {
		t.Fatalf("accept = %d %s (user %s)", w.Code, w.Body.String(), o.gotUser)
	}
	w = officialCall(r, http.MethodPost, "/v1/profiles/me/verification/organisation/confirm-email",
		map[string]string{"X-User-Id": u.String(), "X-Internal-Service-Key": testInternalKey}, map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing code = %d", w.Code)
	}
}

// The two internal-key routes that reviewed applications and created
// Government invitations were removed (16 Nov 2026): the console's token
// family replaces them. Nothing answers on the old paths, with or without the
// internal key, and the gateway-proxied prefix never reaches a console route.
func TestOfficialInternalKeyRoutes_AreGone(t *testing.T) {
	o := &stubOfficial{}
	r := officialRouter(o, testInternalKey)
	svc := map[string]string{"X-Internal-Service-Key": testInternalKey, "X-Caller-Service": "admin-service"}
	for _, p := range []string{
		"/internal/v1/profiles/verification/organisation/applications/" + uuid.NewString() + "/review",
		"/internal/v1/profiles/verification/government/invitations",
		"/v1/profiles/verification/government/invitations",
		"/v1/profiles/internal/verification/government/invitations",
		"/v1/profiles/admin/marks/government/invitations",
	} {
		w := officialCall(r, http.MethodPost, p, svc, map[string]any{"decision": "approve", "user_id": uuid.NewString()})
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s", p, w.Code, w.Body.String())
		}
	}
	if o.calls != 0 {
		t.Fatalf("a removed route reached the service %d times", o.calls)
	}
}

func TestNameReserved_Is409OnRenameAndHandleChange(t *testing.T) {
	w, out := runHandler(t, service.ErrNameReserved, `{"username":"infosys_official"}`, nil, (*Handler).ChangeHandle)
	if w.Code != http.StatusConflict || out.Error.Code != "NAME_RESERVED" {
		t.Fatalf("handle = %d %s", w.Code, w.Body.String())
	}

	gin.SetMode(gin.TestMode)
	h := &Handler{svc: &reservedNameService{}, log: slog.Default()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"display_name":"Infosys Official"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	h.UpdateMe(c)
	if rec.Code != http.StatusConflict || decodeErrorCode(t, rec) != "NAME_RESERVED" {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}
}

type reservedNameService struct{ stubProfileService }

func (s *reservedNameService) UpdateProfile(context.Context, uuid.UUID, store.UpdateProfileParams) (*store.Profile, error) {
	return nil, service.ErrNameReserved
}

func TestPublicProfile_OrganisationNameOnlyWithTheMark(t *testing.T) {
	p := &store.Profile{UserID: uuid.New(), DisplayName: "Infosys", IsVerified: true,
		VerificationLevel: verification.LevelOrganisation, OrganisationName: "Infosys Limited"}
	if got := ToPublicProfile(p).OrganisationName; got != "Infosys Limited" {
		t.Fatalf("org = %q", got)
	}
	p.VerificationLevel = verification.LevelGovernment
	if got := ToPublicProfile(p).OrganisationName; got != "Infosys Limited" {
		t.Fatalf("gov = %q", got)
	}
	p.IsVerified, p.VerificationLevel = false, ""
	if got := ToPublicProfile(p).OrganisationName; got != "" {
		t.Fatalf("a cleared mark still shows %q", got)
	}
	p.IsVerified, p.VerificationLevel = true, verification.LevelPerson
	if got := ToPublicProfile(p).OrganisationName; got != "" {
		t.Fatalf("a person shows %q", got)
	}
}
