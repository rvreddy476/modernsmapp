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

type stubOfficial struct {
	reviews     int
	invitations int
	gotUser     uuid.UUID
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
func (s *stubOfficial) ReviewOrganisation(context.Context, uuid.UUID, officialverify.ReviewDecision) (*officialverify.OrgStatus, error) {
	s.reviews++
	return &officialverify.OrgStatus{}, nil
}
func (s *stubOfficial) CreateGovInvitation(context.Context, officialverify.GovInvitationRequest) (*officialverify.GovInvitationResult, error) {
	s.invitations++
	return &officialverify.GovInvitationResult{InviteCode: "GOV-X"}, nil
}
func (s *stubOfficial) AcceptGovInvitation(_ context.Context, u uuid.UUID, _ string) (*officialverify.OrgStatus, error) {
	s.gotUser = u
	return &officialverify.OrgStatus{VerificationLevel: "government"}, nil
}

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

var reviewPath = "/internal/v1/profiles/verification/organisation/applications/" + uuid.NewString() + "/review"

const invitePath = "/internal/v1/profiles/verification/government/invitations"

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

func TestOfficialInternalRoutes_ServiceCallerOnly(t *testing.T) {
	o := &stubOfficial{}
	r := officialRouter(o, testInternalKey)
	review := map[string]any{"decision": "approve", "reviewer_id": uuid.NewString()}
	invite := map[string]any{"user_id": uuid.NewString(), "official_name": "Ministry", "approver_ids": []string{uuid.NewString(), uuid.NewString()}}

	for _, tc := range []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"no key", map[string]string{}, http.StatusUnauthorized},
		{"wrong key", map[string]string{"X-Internal-Service-Key": "nope"}, http.StatusUnauthorized},
		{"user identity with the key", map[string]string{"X-Internal-Service-Key": testInternalKey, "X-User-Id": uuid.NewString()}, http.StatusForbidden},
		{"admin scope with the key", map[string]string{"X-Internal-Service-Key": testInternalKey, "X-Scopes": "admin"}, http.StatusForbidden},
	} {
		if w := officialCall(r, http.MethodPost, reviewPath, tc.headers, review); w.Code != tc.status {
			t.Errorf("review, %s: %d %s", tc.name, w.Code, w.Body.String())
		}
		if w := officialCall(r, http.MethodPost, invitePath, tc.headers, invite); w.Code != tc.status {
			t.Errorf("invite, %s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	if o.reviews != 0 || o.invitations != 0 {
		t.Fatalf("a refused caller reached the service: %d reviews, %d invitations", o.reviews, o.invitations)
	}
	svc := map[string]string{"X-Internal-Service-Key": testInternalKey, "X-Caller-Service": "admin-service"}
	if w := officialCall(r, http.MethodPost, reviewPath, svc, review); w.Code != http.StatusOK {
		t.Fatalf("service review = %d %s", w.Code, w.Body.String())
	}
	if w := officialCall(r, http.MethodPost, invitePath, svc, invite); w.Code != http.StatusCreated {
		t.Fatalf("service invite = %d %s", w.Code, w.Body.String())
	}
}

func TestOfficialInternalRoutes_NotUnderTheGatewayPrefix(t *testing.T) {
	o := &stubOfficial{}
	r := officialRouter(o, testInternalKey)
	svc := map[string]string{"X-Internal-Service-Key": testInternalKey}
	// The gateway proxies /v1/profiles/* with the key attached: nothing
	// service-only may answer there.
	for _, p := range []string{
		"/v1/profiles/verification/government/invitations",
		"/v1/profiles/internal/verification/government/invitations",
		"/v1/profiles/me/verification/government/invitations",
	} {
		if w := officialCall(r, http.MethodPost, p, svc, map[string]any{}); w.Code == http.StatusCreated || o.invitations != 0 {
			t.Fatalf("%s reached the invitation route: %d", p, w.Code)
		}
	}
}

func TestOfficialInternalRoutes_NotRegisteredWithoutAKey(t *testing.T) {
	o := &stubOfficial{}
	r := officialRouter(o, "")
	if w := officialCall(r, http.MethodPost, invitePath, nil, map[string]any{}); w.Code != http.StatusNotFound || o.invitations != 0 {
		t.Fatalf("invite without a configured key = %d", w.Code)
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
