package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/officialverify"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/servicetoken/tokentest"
	"github.com/atpost/identity-shared/verification"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

const testAdminKID = "a1"

var consoleActor = uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")

func consoleRouter(t *testing.T, o OfficialVerification, kp *tokentest.Keypair, internalKey string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if internalKey != "" {
		h.WithInternalKey(internalKey)
	}
	if o != nil {
		h.WithOfficialVerification(o)
	}
	if kp != nil {
		v, err := AdminServiceVerifier(kp.PubB64, testAdminKID)
		if err != nil || v == nil {
			t.Fatalf("verifier: %v", err)
		}
		h.WithAdminTokenVerifier(v)
	}
	pass := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, pass, pass)
	return r
}

func tokenOpts(perm string, actor uuid.UUID) tokentest.Options {
	o := tokentest.Options{Issuer: IssuerAdminService, KID: testAdminKID, Subject: "admin-console",
		Audience: AudienceProfile, Scope: []string{perm}}
	if actor != uuid.Nil {
		o.Actor = actor.String()
	}
	return o
}

func mint(kp tokentest.Keypair, perm string, actor uuid.UUID) string {
	return tokentest.Mint(kp, tokenOpts(perm, actor))
}

func consoleCall(r *gin.Engine, method, path, token string, headers map[string]string, body any) *httptest.ResponseRecorder {
	h := map[string]string{}
	if token != "" {
		h[ServiceAuthHeader] = "Bearer " + token
	}
	for k, v := range headers {
		h[k] = v
	}
	return officialCall(r, method, path, h, body)
}

type consoleRoute struct {
	name, method, path, perm string
	body                     any
	ok                       int
}

func consoleRoutes() []consoleRoute {
	p := AdminMarksPrefix + "/marks"
	app, inv := uuid.NewString(), uuid.NewString()
	return []consoleRoute{
		{"stats", http.MethodGet, p + "/stats", PermOfficialMarksRead, nil, http.StatusOK},
		{"applications", http.MethodGet, p + "/organisation/applications", PermOfficialMarksRead, nil, http.StatusOK},
		{"application", http.MethodGet, p + "/organisation/applications/" + app, PermOfficialMarksRead, nil, http.StatusOK},
		{"document", http.MethodGet, p + "/organisation/applications/" + app + "/document", PermOfficialMarksReview, nil, http.StatusOK},
		{"review", http.MethodPost, p + "/organisation/applications/" + app + "/review", PermOfficialMarksReview,
			map[string]any{"decision": "approve", "reason": "checked"}, http.StatusOK},
		{"invitations", http.MethodGet, p + "/government/invitations", PermOfficialMarksRead, nil, http.StatusOK},
		{"invite", http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": uuid.NewString(), "official_name": "Ministry", "reason": "r", "requested_by": uuid.NewString()}, http.StatusCreated},
		{"revoke", http.MethodPost, p + "/government/invitations/" + inv + "/revoke", PermGovernmentMarksInvite,
			map[string]any{"reason": "r"}, http.StatusOK},
	}
}

// ---------------------------------------------------------------------------
// The guard
// ---------------------------------------------------------------------------

// TestConsoleGuard_OnlyAScopedAdminServiceTokenGetsIn: every route refuses
// everything but an admin-service token, for audience profile, whose scope is
// the route's permission and which names the acting admin. Nothing refused
// reaches the service.
func TestConsoleGuard_OnlyAScopedAdminServiceTokenGetsIn(t *testing.T) {
	kp, other := tokentest.NewKeypair(), tokentest.NewKeypair()
	o := &stubOfficial{}
	r := consoleRouter(t, o, &kp, testInternalKey)
	all := []string{PermOfficialMarksRead, PermOfficialMarksReview, PermGovernmentMarksInvite}

	for _, rt := range consoleRoutes() {
		with := func(f func(*tokentest.Options)) string {
			opts := tokenOpts(rt.perm, consoleActor)
			f(&opts)
			return tokentest.Mint(kp, opts)
		}
		good := mint(kp, rt.perm, consoleActor)
		type refusal struct {
			name    string
			token   string
			headers map[string]string
			status  int
			code    string
		}
		cases := []refusal{
			{"no token", "", nil, http.StatusUnauthorized, CodeAdminTokenRequired},
			{"internal key only", "", map[string]string{"X-Internal-Service-Key": testInternalKey}, http.StatusUnauthorized, CodeAdminTokenRequired},
			{"wrong audience", with(func(o *tokentest.Options) { o.Audience = "identity" }), nil, http.StatusForbidden, CodeServiceTokenRejected},
			{"another key", mint(other, rt.perm, consoleActor), nil, http.StatusForbidden, CodeServiceTokenRejected},
			{"another issuer", with(func(o *tokentest.Options) { o.Issuer = "dating-service" }), nil, http.StatusForbidden, CodeServiceTokenRejected},
			{"another kid", with(func(o *tokentest.Options) { o.KID = "a2" }), nil, http.StatusForbidden, CodeServiceTokenRejected},
			{"expired", with(func(o *tokentest.Options) { o.Now = time.Now().Add(-2 * time.Minute) }), nil, http.StatusForbidden, CodeServiceTokenRejected},
			{"alg none", with(func(o *tokentest.Options) { o.Alg = "none" }), nil, http.StatusForbidden, CodeServiceTokenRejected},
			{"no actor", mint(kp, rt.perm, uuid.Nil), nil, http.StatusForbidden, CodeAdminActorRequired},
			{"actor not a uuid", with(func(o *tokentest.Options) { o.Actor = "admin" }), nil, http.StatusForbidden, CodeAdminActorRequired},
			{"scope of all three but this one", with(func(o *tokentest.Options) {
				o.Scope = nil
				for _, p := range all {
					if p != rt.perm {
						o.Scope = append(o.Scope, p)
					}
				}
			}), nil, http.StatusForbidden, CodeAdminPermissionScope},
		}
		for _, perm := range all {
			if perm != rt.perm {
				cases = append(cases, refusal{"scope " + perm, mint(kp, perm, consoleActor), nil, http.StatusForbidden, CodeAdminPermissionScope})
			}
		}
		for _, hdr := range gatewayIdentityHeaders {
			cases = append(cases, refusal{"good token with " + hdr, good, map[string]string{hdr: uuid.NewString()}, http.StatusForbidden, CodeUserCallerRefused})
		}
		for _, tc := range cases {
			w := consoleCall(r, rt.method, rt.path, tc.token, tc.headers, rt.body)
			if w.Code != tc.status || errorCode(t, w) != tc.code {
				t.Errorf("%s, %s: %d %s, want %d %s", rt.name, tc.name, w.Code, w.Body.String(), tc.status, tc.code)
			}
		}
	}
	if o.calls != 0 {
		t.Fatalf("a refused caller reached the service %d times", o.calls)
	}
	// The good token, with no internal key although the service has one.
	for _, rt := range consoleRoutes() {
		if w := consoleCall(r, rt.method, rt.path, mint(kp, rt.perm, consoleActor), nil, rt.body); w.Code != rt.ok {
			t.Errorf("%s: %d %s, want %d", rt.name, w.Code, w.Body.String(), rt.ok)
		}
	}
	if o.calls != len(consoleRoutes()) {
		t.Fatalf("service calls = %d, want %d", o.calls, len(consoleRoutes()))
	}
}

// TestConsoleFamily_NotRegisteredWithoutAKey: no ADMIN_SERVICE_TOKEN_PUBKEY,
// no family. Every path under the prefix is a plain 404, with or without the
// internal key configured, whatever token arrives.
func TestConsoleFamily_NotRegisteredWithoutAKey(t *testing.T) {
	kp := tokentest.NewKeypair()
	for _, key := range []string{"", testInternalKey} {
		o := &stubOfficial{}
		r := consoleRouter(t, o, nil, key)
		for _, rt := range consoleRoutes() {
			w := consoleCall(r, rt.method, rt.path, mint(kp, rt.perm, consoleActor), nil, rt.body)
			if w.Code != http.StatusNotFound || errorCode(t, w) != "NOT_FOUND" {
				t.Errorf("internal key %q, %s: %d %s", key, rt.name, w.Code, w.Body.String())
			}
		}
		if o.calls != 0 {
			t.Fatal("an unregistered family reached the service")
		}
	}
	// A key but no official-marks service: not registered either.
	r := consoleRouter(t, nil, &kp, testInternalKey)
	if w := consoleCall(r, http.MethodGet, AdminMarksPrefix+"/marks/stats", mint(kp, PermOfficialMarksRead, consoleActor), nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unwired service: %d", w.Code)
	}
}

func TestAdminServiceVerifier_Config(t *testing.T) {
	if v, err := AdminServiceVerifier("", "a1"); v != nil || err != nil {
		t.Fatalf("no key = %v, %v", v, err)
	}
	if _, err := AdminServiceVerifier("not base64!", "a1"); err == nil {
		t.Fatal("a bad key was accepted")
	}
	kp := tokentest.NewKeypair()
	// An empty kid takes the default a1.
	v, err := AdminServiceVerifier(kp.PubB64, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(mint(kp, PermOfficialMarksRead, consoleActor), PermOfficialMarksRead); err != nil {
		t.Fatalf("default kid: %v", err)
	}
	// admin-service may only ever name the three marks permissions here.
	opts := tokenOpts("platform:roles.manage", consoleActor)
	if _, err := v.Verify(tokentest.Mint(kp, opts), "platform:roles.manage"); err == nil {
		t.Fatal("a permission outside the family's list was accepted")
	}
}

// ---------------------------------------------------------------------------
// Handlers (stubbed service)
// ---------------------------------------------------------------------------

// TestConsoleRoutes_ActorComesFromTheToken: the reviewer, the second
// approver and the revoker are the signed act claim. A body field naming
// someone else changes nothing.
func TestConsoleRoutes_ActorComesFromTheToken(t *testing.T) {
	kp := tokentest.NewKeypair()
	o := &stubOfficial{}
	r := consoleRouter(t, o, &kp, "")
	p := AdminMarksPrefix + "/marks"
	impostor := uuid.NewString()

	app := uuid.New()
	w := consoleCall(r, http.MethodPost, p+"/organisation/applications/"+app.String()+"/review", mint(kp, PermOfficialMarksReview, consoleActor), nil,
		map[string]any{"decision": "reject", "reason": "mismatch", "reviewer_id": impostor})
	if w.Code != http.StatusOK || o.gotID != app || o.gotReview.ReviewerID != consoleActor || o.gotReview.Decision != "reject" || o.gotReview.Reason != "mismatch" {
		t.Fatalf("review = %d %+v", w.Code, o.gotReview)
	}

	invitee, requester := uuid.New(), uuid.New()
	w = consoleCall(r, http.MethodPost, p+"/government/invitations", mint(kp, PermGovernmentMarksInvite, consoleActor), nil,
		map[string]any{"user_id": invitee, "official_name": "Ministry", "reason": "MoU", "requested_by": requester,
			"approver_ids": []string{impostor, impostor}})
	if w.Code != http.StatusCreated || o.gotInvite.UserID != invitee || o.gotInvite.RequestedBy != requester || o.gotInvite.ApproverID != consoleActor {
		t.Fatalf("invite = %d %+v", w.Code, o.gotInvite)
	}

	inv := uuid.New()
	w = consoleCall(r, http.MethodPost, p+"/government/invitations/"+inv.String()+"/revoke", mint(kp, PermGovernmentMarksInvite, consoleActor), nil,
		map[string]any{"reason": "wrong account", "actor": impostor})
	if w.Code != http.StatusOK || o.gotID != inv || o.gotActor != consoleActor || o.gotReason != "wrong account" {
		t.Fatalf("revoke = %d id=%s actor=%s reason=%q", w.Code, o.gotID, o.gotActor, o.gotReason)
	}
}

func TestConsoleRoutes_QueryAndPathValidation(t *testing.T) {
	kp := tokentest.NewKeypair()
	o := &stubOfficial{}
	r := consoleRouter(t, o, &kp, "")
	p := AdminMarksPrefix + "/marks"
	read := mint(kp, PermOfficialMarksRead, consoleActor)
	for _, path := range []string{
		p + "/organisation/applications?limit=0",
		p + "/organisation/applications?limit=51",
		p + "/organisation/applications?limit=ten",
		p + "/government/invitations?limit=-1",
		p + "/organisation/applications/not-a-uuid",
		p + "/organisation/applications/" + uuid.Nil.String(),
	} {
		if w := consoleCall(r, http.MethodGet, path, read, nil, nil); w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("%s = %d %s", path, w.Code, w.Body.String())
		}
	}
	if o.calls != 0 {
		t.Fatal("a refused query reached the service")
	}
	w := consoleCall(r, http.MethodGet, p+"/organisation/applications?status=approved&limit=50&cursor=abc", read, nil, nil)
	if w.Code != http.StatusOK || o.gotQuery != (officialverify.ListQuery{Status: "approved", Limit: 50, Cursor: "abc"}) {
		t.Fatalf("query = %d %+v", w.Code, o.gotQuery)
	}
	w = consoleCall(r, http.MethodPost, p+"/organisation/applications/"+uuid.NewString()+"/review", mint(kp, PermOfficialMarksReview, consoleActor), nil, "not an object")
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_REQUEST" {
		t.Fatalf("bad body = %d %s", w.Code, w.Body.String())
	}
	w = consoleCall(r, http.MethodPost, p+"/government/invitations", mint(kp, PermGovernmentMarksInvite, consoleActor), nil,
		map[string]any{"official_name": "Ministry", "reason": "r", "requested_by": uuid.NewString()})
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_REQUEST" {
		t.Fatalf("no user_id = %d %s", w.Code, w.Body.String())
	}
}

func TestConsoleRoutes_RefusalsAndFailuresKeepTheEnvelope(t *testing.T) {
	kp := tokentest.NewKeypair()
	o := &stubOfficial{err: &officialverify.Error{Status: http.StatusConflict, Code: "APPLICATION_NOT_IN_REVIEW", Message: "x"}}
	r := consoleRouter(t, o, &kp, "")
	for _, rt := range consoleRoutes() {
		w := consoleCall(r, rt.method, rt.path, mint(kp, rt.perm, consoleActor), nil, rt.body)
		if w.Code != http.StatusConflict || errorCode(t, w) != "APPLICATION_NOT_IN_REVIEW" {
			t.Errorf("%s: %d %s", rt.name, w.Code, w.Body.String())
		}
	}
	o.err = errors.New("database is down: secret detail")
	for _, rt := range consoleRoutes() {
		w := consoleCall(r, rt.method, rt.path, mint(kp, rt.perm, consoleActor), nil, rt.body)
		if w.Code != http.StatusInternalServerError || errorCode(t, w) != "INTERNAL_ERROR" || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("%s: %d %s", rt.name, w.Code, w.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// The real service behind the routes
// ---------------------------------------------------------------------------

// consoleStore is a fixed officialverify.Store for the routes' end-to-end
// tests and the golden fixtures. Methods no console route uses are left to
// the nil embedded interface: calling one panics the test.
type consoleStore struct {
	officialverify.Store
	profiles map[uuid.UUID]*store.Profile
	apps     map[uuid.UUID]*store.AdminOrgApplication
	invites  []*store.AdminGovInvitation
	created  []store.GovInvitation
	stats    store.MarksStats
}

func (s *consoleStore) GetProfile(_ context.Context, id uuid.UUID) (*store.Profile, error) {
	if p, ok := s.profiles[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}

func (s *consoleStore) GetOrgApplication(_ context.Context, id uuid.UUID) (*store.OrgApplication, error) {
	if a, ok := s.apps[id]; ok {
		cp := a.OrgApplication
		return &cp, nil
	}
	return nil, nil
}

func (s *consoleStore) GetOrgApplicationAdmin(_ context.Context, id uuid.UUID) (*store.AdminOrgApplication, error) {
	if a, ok := s.apps[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, nil
}

func (s *consoleStore) ListOrgApplicationsAdmin(_ context.Context, status string, limit int, _ *store.Keyset) ([]store.AdminOrgApplication, error) {
	var out []store.AdminOrgApplication
	for _, a := range s.apps {
		if a.Status == status {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if status == store.OrgAppInReview {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit+1 {
		out = out[:limit+1]
	}
	return out, nil
}

func (s *consoleStore) MarksStats(context.Context, time.Time, time.Time) (*store.MarksStats, error) {
	m := s.stats
	return &m, nil
}

func (s *consoleStore) RejectOrgApplication(_ context.Context, id, reviewer uuid.UUID, reason string, now time.Time) error {
	a := s.apps[id]
	a.Status, a.ReviewerID, a.ReviewReason, a.CompletedAt = store.OrgAppRejected, &reviewer, reason, &now
	return nil
}

func (s *consoleStore) ApproveOrganisation(_ context.Context, in store.ApproveOrganisationInput) (*store.Profile, error) {
	a := s.apps[in.ApplicationID]
	if !a.Applicant.IsPersonVerified() {
		return nil, store.ErrPersonMarkRequired
	}
	a.Status, a.ReviewerID, a.ReviewReason, a.CompletedAt = store.OrgAppApproved, in.ReviewerID, in.ReviewReason, &in.Now
	return &store.Profile{UserID: a.UserID, IsVerified: true, VerificationLevel: verification.LevelOrganisation}, nil
}

func (s *consoleStore) RefuseOrgApplication(_ context.Context, id uuid.UUID, code string, _ time.Time) error {
	a := s.apps[id]
	a.Status, a.RefusalCode = store.OrgAppRefused, code
	return nil
}

func (s *consoleStore) CreateGovInvitation(_ context.Context, g store.GovInvitation) error {
	s.created = append(s.created, g)
	return nil
}

func (s *consoleStore) ListGovInvitationsAdmin(_ context.Context, status string, _ int, _ *store.Keyset, _ time.Time) ([]store.AdminGovInvitation, error) {
	var out []store.AdminGovInvitation
	for _, g := range s.invites {
		if g.Status == status {
			out = append(out, *g)
		}
	}
	return out, nil
}

func (s *consoleStore) GetGovInvitationAdmin(_ context.Context, id uuid.UUID, _ time.Time) (*store.AdminGovInvitation, error) {
	for _, g := range s.invites {
		if g.ID == id {
			cp := *g
			return &cp, nil
		}
	}
	return nil, nil
}

func (s *consoleStore) RevokeGovInvitation(_ context.Context, id, _ uuid.UUID, _ string, _ time.Time) error {
	for _, g := range s.invites {
		if g.ID == id {
			if g.Status != store.InvitationPending {
				return store.ErrInvitationNotPending
			}
			g.Status = store.InvitationRevoked
			return nil
		}
	}
	return store.ErrInvitationNotFound
}

// Fixed contract data: the values the golden fixtures carry.
var (
	goldenNow        = time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	goldenApplicant  = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	goldenApp        = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	goldenApp2       = uuid.MustParse("22222222-2222-4222-8222-333333333333")
	goldenDocument   = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	goldenInvitation = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	goldenInvitee    = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	goldenRequester  = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
)

func goldenStore() *consoleStore {
	person := store.MarksApplicant{Username: "acme_india", DisplayName: "Asha Rao", IsVerified: true,
		VerificationLevel: verification.LevelPerson, BadgeFlags: verification.BadgePersonVerified}
	doc := goldenDocument
	app := func(id uuid.UUID, at time.Time) *store.AdminOrgApplication {
		return &store.AdminOrgApplication{
			OrgApplication: store.OrgApplication{ID: id, UserID: goldenApplicant, LegalName: "Acme India Private Limited",
				RegistrationKind: verification.RegistrationGSTIN, RegistrationNumber: "29AAACA1234A1Z5", OfficialDomain: "acme.co.in",
				SignatoryEmail: "asha@acme.co.in", DocumentMediaID: &doc, Status: store.OrgAppInReview, CreatedAt: at},
			Applicant: person,
		}
	}
	return &consoleStore{
		profiles: map[uuid.UUID]*store.Profile{goldenInvitee: {UserID: goldenInvitee, DisplayName: "Example Ministry"}},
		apps: map[uuid.UUID]*store.AdminOrgApplication{
			goldenApp:  app(goldenApp, time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)),
			goldenApp2: app(goldenApp2, time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)),
		},
		invites: []*store.AdminGovInvitation{{
			ID: goldenInvitation, UserID: goldenInvitee, OfficialName: "Ministry of Example Affairs", Status: store.InvitationPending,
			ApproverOne: goldenRequester, ApproverTwo: consoleActor, Reason: "MoU 12 signed with the ministry",
			ExpiresAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
			Invitee: store.MarksApplicant{Username: "example_ministry", DisplayName: "Example Ministry"},
		}},
		stats: store.MarksStats{InReview: 3, ApprovedSince: 5, RejectedSince: 1, InvitationsPending: 0, OfficialAccounts: 12, GovernmentAccounts: 1},
	}
}

func realConsole(t *testing.T, st *consoleStore, cfg officialverify.Config) (*gin.Engine, tokentest.Keypair) {
	t.Helper()
	kp := tokentest.NewKeypair()
	svc := officialverify.New(cfg, st, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithClock(func() time.Time { return goldenNow })
	return consoleRouter(t, svc, &kp, testInternalKey), kp
}

var bothOn = officialverify.Config{OrgEnabled: true, GovEnabled: true}

func TestConsoleRules_EndToEnd(t *testing.T) {
	p := AdminMarksPrefix + "/marks"
	reviewPath := p + "/organisation/applications/" + goldenApp.String() + "/review"
	cases := []struct {
		name   string
		actor  uuid.UUID
		method string
		path   string
		perm   string
		body   any
		status int
		code   string
	}{
		{"reason required", consoleActor, http.MethodPost, reviewPath, PermOfficialMarksReview,
			map[string]any{"decision": "approve", "reason": "  "}, http.StatusBadRequest, "REASON_REQUIRED"},
		{"reason too long", consoleActor, http.MethodPost, reviewPath, PermOfficialMarksReview,
			map[string]any{"decision": "approve", "reason": strings.Repeat("x", 501)}, http.StatusBadRequest, "REASON_REQUIRED"},
		{"bad decision", consoleActor, http.MethodPost, reviewPath, PermOfficialMarksReview,
			map[string]any{"decision": "maybe", "reason": "x"}, http.StatusBadRequest, "INVALID_DECISION"},
		{"reviewer is the applicant", goldenApplicant, http.MethodPost, reviewPath, PermOfficialMarksReview,
			map[string]any{"decision": "approve", "reason": "mine"}, http.StatusForbidden, "REVIEWER_IS_APPLICANT"},
		{"unknown application", consoleActor, http.MethodPost, p + "/organisation/applications/" + uuid.NewString() + "/review", PermOfficialMarksReview,
			map[string]any{"decision": "approve", "reason": "x"}, http.StatusNotFound, "APPLICATION_NOT_FOUND"},
		{"unknown application detail", consoleActor, http.MethodGet, p + "/organisation/applications/" + uuid.NewString(), PermOfficialMarksRead,
			nil, http.StatusNotFound, "APPLICATION_NOT_FOUND"},
		{"two approvers must differ", goldenRequester, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs", "reason": "MoU", "requested_by": goldenRequester},
			http.StatusBadRequest, "TWO_APPROVERS_REQUIRED"},
		{"requested_by missing", consoleActor, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs", "reason": "MoU"},
			http.StatusBadRequest, "TWO_APPROVERS_REQUIRED"},
		{"approver is the invitee", goldenInvitee, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs", "reason": "MoU", "requested_by": goldenRequester},
			http.StatusForbidden, "APPROVER_IS_INVITEE"},
		{"requester is the invitee", consoleActor, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs", "reason": "MoU", "requested_by": goldenInvitee},
			http.StatusForbidden, "APPROVER_IS_INVITEE"},
		{"invite reason required", consoleActor, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs", "requested_by": goldenRequester},
			http.StatusBadRequest, "REASON_REQUIRED"},
		{"invalid official name", consoleActor, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "M", "reason": "MoU", "requested_by": goldenRequester},
			http.StatusBadRequest, "INVALID_OFFICIAL_NAME"},
		{"invitee has no profile", consoleActor, http.MethodPost, p + "/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": uuid.NewString(), "official_name": "Ministry of Example Affairs", "reason": "MoU", "requested_by": goldenRequester},
			http.StatusNotFound, "PROFILE_NOT_FOUND"},
		{"revoke reason required", consoleActor, http.MethodPost, p + "/government/invitations/" + goldenInvitation.String() + "/revoke", PermGovernmentMarksInvite,
			map[string]any{"reason": ""}, http.StatusBadRequest, "REASON_REQUIRED"},
		{"revoke unknown", consoleActor, http.MethodPost, p + "/government/invitations/" + uuid.NewString() + "/revoke", PermGovernmentMarksInvite,
			map[string]any{"reason": "x"}, http.StatusNotFound, "INVITATION_NOT_FOUND"},
		{"unknown list status", consoleActor, http.MethodGet, p + "/government/invitations?status=approved", PermOfficialMarksRead,
			nil, http.StatusBadRequest, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		st := goldenStore()
		r, kp := realConsole(t, st, bothOn)
		w := consoleCall(r, tc.method, tc.path, mint(kp, tc.perm, tc.actor), nil, tc.body)
		if w.Code != tc.status || errorCode(t, w) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, w.Code, w.Body.String(), tc.status, tc.code)
		}
		if len(st.created) != 0 || st.apps[goldenApp].Status != store.OrgAppInReview || st.invites[0].Status != store.InvitationPending {
			t.Errorf("%s: a refused request changed state", tc.name)
		}
	}
}

func TestConsoleRules_DecisionsAndStates(t *testing.T) {
	p := AdminMarksPrefix + "/marks"
	reviewPath := p + "/organisation/applications/" + goldenApp.String() + "/review"
	approveBody := map[string]any{"decision": "approve", "reason": "Certificate checked against the registry"}

	// Applicant lost the person mark: 422 and the application is refused.
	st := goldenStore()
	st.apps[goldenApp].Applicant.BadgeFlags = 0
	r, kp := realConsole(t, st, bothOn)
	w := consoleCall(r, http.MethodPost, reviewPath, mint(kp, PermOfficialMarksReview, consoleActor), nil, approveBody)
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != "PERSON_MARK_REQUIRED" || st.apps[goldenApp].Status != store.OrgAppRefused {
		t.Fatalf("person lost: %d %s (%s)", w.Code, w.Body.String(), st.apps[goldenApp].Status)
	}

	// Approve, then again: 409.
	st = goldenStore()
	r, kp = realConsole(t, st, bothOn)
	w = consoleCall(r, http.MethodPost, reviewPath, mint(kp, PermOfficialMarksReview, consoleActor), nil, approveBody)
	if w.Code != http.StatusOK || st.apps[goldenApp].Status != store.OrgAppApproved || *st.apps[goldenApp].ReviewerID != consoleActor {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	w = consoleCall(r, http.MethodPost, reviewPath, mint(kp, PermOfficialMarksReview, consoleActor), nil, approveBody)
	if w.Code != http.StatusConflict || errorCode(t, w) != "APPLICATION_NOT_IN_REVIEW" {
		t.Fatalf("second decision: %d %s", w.Code, w.Body.String())
	}

	// No document: 404 DOCUMENT_NOT_FOUND.
	st = goldenStore()
	st.apps[goldenApp].DocumentMediaID = nil
	r, kp = realConsole(t, st, bothOn)
	w = consoleCall(r, http.MethodGet, p+"/organisation/applications/"+goldenApp.String()+"/document", mint(kp, PermOfficialMarksReview, consoleActor), nil, nil)
	if w.Code != http.StatusNotFound || errorCode(t, w) != "DOCUMENT_NOT_FOUND" {
		t.Fatalf("no document: %d %s", w.Code, w.Body.String())
	}

	// Revoke twice: 409.
	st = goldenStore()
	r, kp = realConsole(t, st, bothOn)
	revoke := p + "/government/invitations/" + goldenInvitation.String() + "/revoke"
	if w := consoleCall(r, http.MethodPost, revoke, mint(kp, PermGovernmentMarksInvite, consoleActor), nil, map[string]any{"reason": "x"}); w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	w = consoleCall(r, http.MethodPost, revoke, mint(kp, PermGovernmentMarksInvite, consoleActor), nil, map[string]any{"reason": "x"})
	if w.Code != http.StatusConflict || errorCode(t, w) != "INVITATION_NOT_PENDING" {
		t.Fatalf("second revoke: %d %s", w.Code, w.Body.String())
	}

	// Already Government: 409.
	st = goldenStore()
	st.profiles[goldenInvitee].IsVerified, st.profiles[goldenInvitee].VerificationLevel = true, verification.LevelGovernment
	r, kp = realConsole(t, st, bothOn)
	w = consoleCall(r, http.MethodPost, p+"/government/invitations", mint(kp, PermGovernmentMarksInvite, consoleActor), nil,
		map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs", "reason": "MoU", "requested_by": goldenRequester})
	if w.Code != http.StatusConflict || errorCode(t, w) != "ALREADY_GOVERNMENT" {
		t.Fatalf("already government: %d %s", w.Code, w.Body.String())
	}
}

func TestConsoleRules_FlagsOff(t *testing.T) {
	r, kp := realConsole(t, goldenStore(), officialverify.Config{})
	for _, rt := range consoleRoutes() {
		w := consoleCall(r, rt.method, rt.path, mint(kp, rt.perm, consoleActor), nil, rt.body)
		want := "ORG_VERIFICATION_DISABLED"
		if strings.Contains(rt.path, "/government/") {
			want = "GOVERNMENT_VERIFICATION_DISABLED"
		}
		if w.Code != http.StatusServiceUnavailable || errorCode(t, w) != want {
			t.Errorf("%s: %d %s, want 503 %s", rt.name, w.Code, w.Body.String(), want)
		}
	}
}

// ---------------------------------------------------------------------------
// Golden contract fixtures (testdata/contracts, copied byte for byte by
// admin-service and the console). Regenerate deliberately with
// UPDATE_CONTRACTS=1 and review the diff.
// ---------------------------------------------------------------------------

var contractsDir = filepath.Join("..", "..", "testdata", "contracts")

func assertGolden(t *testing.T, w *httptest.ResponseRecorder, status int, name string, replace map[string]string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("%s: status %d, want %d (%s)", name, w.Code, status, w.Body.String())
	}
	body := strings.TrimSpace(w.Body.String())
	for from, to := range replace {
		body = strings.ReplaceAll(body, from, to)
	}
	path := filepath.Join(contractsDir, name+".json")
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, []byte(body), "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		if err := os.MkdirAll(contractsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with UPDATE_CONTRACTS=1 to create it)", name, err)
	}
	var wantC, gotC bytes.Buffer
	if err := json.Compact(&wantC, want); err != nil {
		t.Fatalf("%s: fixture is not JSON: %v", name, err)
	}
	if err := json.Compact(&gotC, []byte(body)); err != nil {
		t.Fatalf("%s: body is not JSON: %v", name, err)
	}
	if wantC.String() != gotC.String() {
		t.Fatalf("%s drifted from the contract:\n got  %s\n want %s", name, gotC.String(), wantC.String())
	}
}

var inviteCodeRe = regexp.MustCompile(`GOV-[A-Z2-7]{32}`)

func TestConsoleContractFixtures(t *testing.T) {
	p := AdminMarksPrefix + "/marks"
	get := func(t *testing.T, st *consoleStore, path, perm string) *httptest.ResponseRecorder {
		r, kp := realConsole(t, st, bothOn)
		return consoleCall(r, http.MethodGet, path, mint(kp, perm, consoleActor), nil, nil)
	}
	post := func(t *testing.T, st *consoleStore, path, perm string, body any) *httptest.ResponseRecorder {
		r, kp := realConsole(t, st, bothOn)
		return consoleCall(r, http.MethodPost, path, mint(kp, perm, consoleActor), nil, body)
	}

	t.Run("stats", func(t *testing.T) {
		assertGolden(t, get(t, goldenStore(), p+"/stats", PermOfficialMarksRead), http.StatusOK, "marks_stats_get_200", nil)
	})
	t.Run("applications", func(t *testing.T) {
		w := get(t, goldenStore(), p+"/organisation/applications?limit=1", PermOfficialMarksRead)
		assertGolden(t, w, http.StatusOK, "marks_applications_get_200", nil)
	})
	t.Run("application", func(t *testing.T) {
		w := get(t, goldenStore(), p+"/organisation/applications/"+goldenApp.String(), PermOfficialMarksRead)
		assertGolden(t, w, http.StatusOK, "marks_application_get_200", nil)
	})
	t.Run("document", func(t *testing.T) {
		w := get(t, goldenStore(), p+"/organisation/applications/"+goldenApp.String()+"/document", PermOfficialMarksReview)
		assertGolden(t, w, http.StatusOK, "marks_application_document_get_200", nil)
	})
	t.Run("review", func(t *testing.T) {
		w := post(t, goldenStore(), p+"/organisation/applications/"+goldenApp.String()+"/review", PermOfficialMarksReview,
			map[string]any{"decision": "reject", "reason": "The certificate's legal name does not match the application"})
		assertGolden(t, w, http.StatusOK, "marks_application_review_post_200", nil)
	})
	t.Run("invitations", func(t *testing.T) {
		w := get(t, goldenStore(), p+"/government/invitations", PermOfficialMarksRead)
		assertGolden(t, w, http.StatusOK, "marks_invitations_get_200", nil)
	})
	t.Run("invite", func(t *testing.T) {
		st := goldenStore()
		w := post(t, st, p+"/government/invitations", PermGovernmentMarksInvite,
			map[string]any{"user_id": goldenInvitee, "official_name": "Ministry of Example Affairs",
				"reason": "MoU 12 signed with the ministry", "requested_by": goldenRequester})
		if len(st.created) != 1 {
			t.Fatalf("created = %d", len(st.created))
		}
		g := st.created[0]
		if g.ApproverOne != goldenRequester || g.ApproverTwo != consoleActor {
			t.Fatalf("approvers = [%s %s], want [requested_by act]", g.ApproverOne, g.ApproverTwo)
		}
		code := inviteCodeRe.FindString(w.Body.String())
		if code == "" {
			t.Fatalf("no invite code in %s", w.Body.String())
		}
		assertGolden(t, w, http.StatusCreated, "marks_invitation_create_post_201", map[string]string{
			g.ID.String(): goldenInvitation.String(),
			code:          "GOV-ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
		})
	})
	t.Run("revoke", func(t *testing.T) {
		w := post(t, goldenStore(), p+"/government/invitations/"+goldenInvitation.String()+"/revoke", PermGovernmentMarksInvite,
			map[string]any{"reason": "Issued to the wrong account"})
		assertGolden(t, w, http.StatusOK, "marks_invitation_revoke_post_200", nil)
	})
}
