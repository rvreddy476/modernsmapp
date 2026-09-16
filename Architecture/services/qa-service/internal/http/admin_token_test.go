// Admin-service token tests (admin console Wave 2 — Content apps, Q&A). No
// database: a recording fake store proves admission and the actor handed to
// the store; refusals are proven by the auth layer's own status and code.
// Audit rows and stats counts are in admin_token_integration_test.go.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/qa-service/internal/service"
	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const adminTestInternalKey = "qa-admin-token-test-key"

// adminFakeStore records the actor each admin call receives.
type adminFakeStore struct {
	actors []uuid.UUID
	reads  int
}

func (f *adminFakeStore) rec(actor uuid.UUID) (*store.ModerationAction, error) {
	f.actors = append(f.actors, actor)
	return &store.ModerationAction{ID: uuid.New(), ActorID: actor}, nil
}

func (f *adminFakeStore) ListReports(context.Context, string, int, int) ([]store.ModerationReport, error) {
	f.reads++
	return nil, nil
}

func (f *adminFakeStore) GetReport(context.Context, uuid.UUID) (*store.ModerationReport, error) {
	f.reads++
	return &store.ModerationReport{}, nil
}

func (f *adminFakeStore) AdminDecideReport(_ context.Context, actor, _ uuid.UUID, _, _ string) (*store.ModerationAction, error) {
	return f.rec(actor)
}

func (f *adminFakeStore) AdminHideContent(_ context.Context, actor uuid.UUID, _ string, _ uuid.UUID, _ *uuid.UUID, _ string) (*store.ModerationAction, error) {
	return f.rec(actor)
}

func (f *adminFakeStore) AdminLockQuestion(_ context.Context, actor, _ uuid.UUID, _ *uuid.UUID, _ string) (*store.ModerationAction, error) {
	return f.rec(actor)
}

func (f *adminFakeStore) AdminMergeQuestion(_ context.Context, actor, _, _ uuid.UUID, _ *uuid.UUID, _ string) (*store.ModerationAction, error) {
	return f.rec(actor)
}

func (f *adminFakeStore) AdminMarkDuplicate(_ context.Context, actor, _, _ uuid.UUID, _ *uuid.UUID, _ string) (*store.ModerationAction, error) {
	return f.rec(actor)
}

func (f *adminFakeStore) ListModerationActionsFiltered(context.Context, *uuid.UUID, *uuid.UUID, string, int, int) ([]store.ModerationAction, error) {
	f.reads++
	return []store.ModerationAction{}, nil
}

func (f *adminFakeStore) AdminStats(context.Context) (*store.AdminStats, error) {
	f.reads++
	return &store.AdminStats{OpenReportsByReason: map[string]int{}}, nil
}

type adminTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
	st       *adminFakeStore
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	notifier *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims to be admin-service, unregistered key
	actor    uuid.UUID
}

func newAdminTokenRig(t *testing.T) *adminTokenRig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	nPub, nPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, rPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                            "admin-service,notification-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":           "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":        aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":           strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_NOTIFICATION_SERVICE_KID":    "n1",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": nPub,
		// Deliberately over-granted: even with the op registered, a caller
		// other than admin-service must not reach an admin route.
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": strings.Join(AdminPermissions, ","),
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	mk := func(iss, kid, priv string) *servicetoken.Signer {
		s, err := servicetoken.NewSignerFromBase64(iss, kid, priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	st := &adminFakeStore{}
	return &adminTokenRig{
		r:        adminTokenRouter(t, st, v),
		v:        v,
		st:       st,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

func adminTokenRouter(t *testing.T, st AdminStore, v *servicetoken.Verifier) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(service.New(nil, nil)).WithInternalKey(adminTestInternalKey).WithServiceAuth(v).WithAdminStore(st).RegisterRoutes(r)
	return r
}

func (rg *adminTokenRig) mint(t *testing.T, s *servicetoken.Signer, aud string, scope []string, actor string) string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", scope, nil, time.Minute, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func adminServe(r *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// gatewayUser is what the gateway would send for a signed-in user, key included.
func gatewayUser(user uuid.UUID) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": adminTestInternalKey,
		"X-User-ID":              user.String(),
		"X-Scopes":               "superadmin admin moderator",
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error == nil {
		return ""
	}
	return env.Error.Code
}

func uuidParams(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = uuid.NewString()
		}
	}
	return strings.Join(parts, "/")
}

// adminWriteJSON is a body every token-path write accepts.
func adminWriteJSON() string {
	return `{"reason":"IT reason","merge_into_id":"` + uuid.NewString() + `","duplicate_of_id":"` + uuid.NewString() + `"}`
}

func answerHidePath() string {
	return InternalAdminPrefix + "/answers/" + uuid.NewString() + "/hide"
}

// The route table under test: method + suffix → permission. Written out here
// rather than read from AdminRoutes so a changed permission fails a test.
func expectedAdminRoutes() map[string]string {
	return map[string]string{
		"GET /stats":                            PermStatsRead,
		"GET /reports":                          PermReportsRead,
		"GET /reports/:reportId":                PermReportsRead,
		"POST /reports/:reportId/resolve":       PermReportsAct,
		"POST /reports/:reportId/dismiss":       PermReportsAct,
		"POST /questions/:questionId/hide":      PermQuestionsModerate,
		"POST /questions/:questionId/lock":      PermQuestionsModerate,
		"POST /questions/:questionId/duplicate": PermQuestionsModerate,
		"POST /questions/:questionId/merge":     PermQuestionsMerge,
		"POST /answers/:answerId/hide":          PermAnswersModerate,
		"POST /comments/:commentId/hide":        PermCommentsModerate,
		"GET /actions":                          PermAuditRead,
	}
}

func TestAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceQA, []string{PermAnswersModerate}, rg.actor.String())
	// A forged gateway identity and the key ride along; the actor is act.
	hdr := gatewayUser(uuid.New())
	hdr["X-Admin-Id"] = uuid.NewString()
	hdr["X-Actor-Id"] = uuid.NewString()
	hdr[ServiceAuthHeader] = "Bearer " + tok
	w := adminServe(rg.r, http.MethodPost, answerHidePath(), `{"reason":"spam"}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if len(rg.st.actors) != 1 || rg.st.actors[0] != rg.actor {
		t.Fatalf("store actors=%v, want [%s]", rg.st.actors, rg.actor)
	}
}

func TestAdminToken_EveryWriteRecordsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	var writes int
	for _, ri := range rg.r.Routes() {
		if ri.Method != http.MethodPost || !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		perm := expectedAdminRoutes()[ri.Method+" "+strings.TrimPrefix(ri.Path, InternalAdminPrefix)]
		hdr := gatewayUser(uuid.New())
		hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceQA, []string{perm}, rg.actor.String())
		before := len(rg.st.actors)
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), adminWriteJSON(), hdr)
		if w.Code != http.StatusOK || len(rg.st.actors) != before+1 || rg.st.actors[before] != rg.actor {
			t.Fatalf("%s %s: status=%d body=%s actors=%v, want 200 with act %s", ri.Method, ri.Path, w.Code, w.Body.String(), rg.st.actors, rg.actor)
		}
		writes++
	}
	if writes != 8 {
		t.Fatalf("checked %d write routes, want 8", writes)
	}
}

func TestAdminToken_WriteNeedsReason(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceQA, []string{PermAnswersModerate}, rg.actor.String())
	for _, body := range []string{``, `{}`, `{"reason":"   "}`} {
		if w := adminServe(rg.r, http.MethodPost, answerHidePath(), body, bearer(tok)); w.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status=%d, want 400", body, w.Code)
		}
	}
	if len(rg.st.actors) != 0 {
		t.Fatalf("a write without a reason reached the store")
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	actor := rg.actor.String()
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience", rg.mint(t, rg.admin, "food", []string{PermAnswersModerate}, actor), 0, CodeServiceTokenRejected},
		{"payments audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, []string{PermAnswersModerate}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceQA, []string{PermAnswersModerate}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceQA, []string{PermAnswersModerate}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceQA, []string{PermAnswersModerate}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceQA, []string{PermQuestionsModerate, PermCommentsModerate}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceQA, []string{PermAnswersModerate}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceQA, []string{PermAnswersModerate}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceQA, []string{PermAnswersModerate}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			hdr := gatewayUser(uuid.New())
			hdr[ServiceAuthHeader] = "Bearer " + tc.tok
			w := adminServe(rg.r, http.MethodPost, answerHidePath(), `{"reason":"spam"}`, hdr)
			if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
			}
			if len(rg.st.actors) != 0 {
				t.Fatalf("a refused request reached the store")
			}
		})
	}
}

// The edge cannot reach the token-only family with the key the gateway stamps
// plus a forged actor: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.New()
	var checked int
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		path := uuidParams(ri.Path)
		hdr := gatewayUser(forged)
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Actor-Id"] = forged.String()
		w := adminServe(rg.r, ri.Method, path, adminWriteJSON(), hdr)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked != len(expectedAdminRoutes()) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(expectedAdminRoutes()))
	}
	if len(rg.st.actors) != 0 || rg.st.reads != 0 {
		t.Fatalf("a request without a token reached the store")
	}
}

// Every route needs exactly its permission: every OTHER admin permission is
// refused by scope, its own is admitted.
func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	want := expectedAdminRoutes()
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		suffix := strings.TrimPrefix(ri.Path, InternalAdminPrefix)
		perm, ok := want[ri.Method+" "+suffix]
		if !ok {
			t.Fatalf("undeclared admin route %s %s", ri.Method, ri.Path)
		}
		seen++
		var others []string
		for _, p := range AdminPermissions {
			if p != perm {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceQA, others, rg.actor.String())
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), adminWriteJSON(), bearer(tok))
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
		own := rg.mint(t, rg.admin, AudienceQA, []string{perm}, rg.actor.String())
		if w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), adminWriteJSON(), bearer(own)); w.Code != http.StatusOK {
			t.Fatalf("%s %s with %s: status=%d body=%s, want 200", ri.Method, ri.Path, perm, w.Code, w.Body.String())
		}
	}
	if seen != len(want) || len(AdminRoutes) != len(want) {
		t.Fatalf("saw %d routes, table has %d, want %d", seen, len(AdminRoutes), len(want))
	}
}

// Merging is narrower than hiding or locking: questions.moderate does not merge.
func TestAdminToken_MergeNeedsMergePermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	path := InternalAdminPrefix + "/questions/" + uuid.NewString() + "/merge"
	mod := rg.mint(t, rg.admin, AudienceQA, []string{PermQuestionsModerate, PermAnswersModerate, PermCommentsModerate, PermReportsAct}, rg.actor.String())
	if w := adminServe(rg.r, http.MethodPost, path, adminWriteJSON(), bearer(mod)); w.Code != http.StatusForbidden {
		t.Fatalf("merge with moderate permissions: status=%d, want 403", w.Code)
	}
	for _, row := range AdminRoutes {
		if row.Permission == PermQuestionsMerge && !row.StepUp {
			t.Fatalf("%s %s must require step-up", row.Method, row.Path)
		}
		if row.TwoPerson {
			t.Fatalf("%s %s: no Q&A route is two-person", row.Method, row.Path)
		}
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceQA, []string{PermStatsRead}, rg.actor.String())
	st := &adminFakeStore{}
	r := adminTokenRouter(t, st, nil)
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
	}
	if st.reads != 0 {
		t.Fatalf("no verifier: the store was read")
	}
}

// The LEGACY allowlist gate is unchanged: key required, a moderator named in
// QA_MODERATOR_USER_IDS passes the gate, anyone else is refused, and a token
// does not open it. (The DB-backed legacy write is in the integration test.)
func TestAdminToken_LegacyAllowlistUnchanged(t *testing.T) {
	moderator := uuid.New()
	t.Setenv("QA_MODERATOR_USER_IDS", moderator.String())
	rg := newAdminTokenRig(t)
	path := "/v1/qa/admin/questions/" + uuid.NewString() + "/merge"

	if w := adminServe(rg.r, http.MethodPost, path, `{"merge_into_id":"bad"}`, gatewayUser(uuid.New())); w.Code != http.StatusForbidden || errorCode(t, w) != "NOT_MODERATOR" {
		t.Fatalf("legacy non-moderator: status=%d code=%q, want 403 NOT_MODERATOR", w.Code, errorCode(t, w))
	}
	// Past the gate the handler validates the body: 400 proves admission
	// without touching the (absent) database.
	if w := adminServe(rg.r, http.MethodPost, path, `{"merge_into_id":"bad"}`, gatewayUser(moderator)); w.Code != http.StatusBadRequest {
		t.Fatalf("legacy moderator: status=%d body=%s, want 400 from the handler", w.Code, w.Body.String())
	}
	noKey := gatewayUser(moderator)
	delete(noKey, "X-Internal-Service-Key")
	if w := adminServe(rg.r, http.MethodPost, path, `{"merge_into_id":"bad"}`, noKey); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("legacy without the internal key: status=%d, want refused", w.Code)
	}
	tokOnly := bearer(rg.mint(t, rg.admin, AudienceQA, AdminPermissions, rg.actor.String()))
	tokOnly["X-Internal-Service-Key"] = adminTestInternalKey
	if w := adminServe(rg.r, http.MethodPost, path, `{"merge_into_id":"bad"}`, tokOnly); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy with only a token: status=%d, want 401 (the legacy path takes no tokens)", w.Code)
	}

	t.Setenv("QA_MODERATOR_USER_IDS", "")
	closed := adminTokenRouter(t, &adminFakeStore{}, rg.v)
	if w := adminServe(closed, http.MethodPost, path, `{"merge_into_id":"bad"}`, gatewayUser(moderator)); w.Code != http.StatusForbidden || errorCode(t, w) != "MODERATION_DISABLED" {
		t.Fatalf("legacy with no allowlist: status=%d code=%q, want 403 MODERATION_DISABLED", w.Code, errorCode(t, w))
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	if v, err := ServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil, nil", v, err)
	}
	pub, _, _ := servicetoken.GenerateKeypair()
	for name, env := range map[string]map[string]string{
		"missing key": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		"missing ops": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
	} {
		if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("%s: want a configuration error", name)
		}
	}
}
