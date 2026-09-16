// Admin-service token tests (admin console Wave 2 — Content). No database:
// the handler runs with a nil service, so a request that gets past every gate
// panics into gin.Recovery (500). 401/403 with the auth layer's own code
// prove a refusal before the service was touched. A probe route proves the
// actor is the act claim and identity headers are stripped. Audit rows,
// per-kind narrowing on posts and stats counts are in
// admin_token_integration_test.go.
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const adminTestKey = "post-admin-token-test-key"

type adminTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	notifier *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims to be admin-service, unregistered key
	actor    uuid.UUID
}

func adminTestVerifier(t *testing.T) (*servicetoken.Verifier, string, string, string) {
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
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": PermCommentsModerate + "," + PermSocialStatsRead,
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return v, aPriv, nPriv, rPriv
}

func newAdminTokenRig(t *testing.T) *adminTokenRig {
	t.Helper()
	v, aPriv, nPriv, rPriv := adminTestVerifier(t)
	mk := func(iss, kid, priv string) *servicetoken.Signer {
		s, err := servicetoken.NewSignerFromBase64(iss, kid, priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return &adminTokenRig{
		r:        adminTokenRouter(v),
		v:        v,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

// adminTokenRouter mirrors main.go's registration with a nil service.
func adminTokenRouter(v *servicetoken.Verifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := New(nil, nil).WithInternalKey(adminTestKey).WithServiceAuth(v)
	h.RegisterRoutes(r)
	h.RegisterReelDiscoveryRoutes(r)
	h.RegisterReportRoutes(r)
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

// edgeModerator is what the gateway would send for a superadmin, key included.
func edgeModerator(user uuid.UUID) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": adminTestKey,
		"X-User-Id":              user.String(),
		"X-Scopes":               "superadmin admin moderator",
	}
}

func adminErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
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

func commentModerationPath() string {
	return InternalAdminPrefix + "/comments/" + uuid.NewString() + "/moderation"
}

// A probe behind the real gate: the actor handlers see is act, and the
// forged identity headers riding along are gone.
func TestAdminToken_AdmittedActorIsActAndHeadersStripped(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := gin.New()
	h := New(nil, nil).WithInternalKey(adminTestKey).WithServiceAuth(rg.v)
	r.GET(InternalAdminPrefix+"/probe", h.requireAdminToken(PermCommentsModerate), func(c *gin.Context) {
		actor, _ := tokenActor(c)
		c.JSON(http.StatusOK, gin.H{
			"actor": actor.String(), "x_user_id": c.GetHeader("X-User-Id"), "x_scopes": c.GetHeader("X-Scopes"),
			"key": c.GetHeader("X-Internal-Service-Key"),
		})
	})
	tok := rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsModerate}, rg.actor.String())
	hdr := edgeModerator(uuid.New())
	hdr[ServiceAuthHeader] = "Bearer " + tok
	w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/probe", "", hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["actor"] != rg.actor.String() {
		t.Fatalf("actor=%q, want act %s", got["actor"], rg.actor)
	}
	if got["x_user_id"] != "" || got["x_scopes"] != "" || got["key"] != "" {
		t.Fatalf("identity headers survived admission: %v", got)
	}
}

// The real routes admit the right token (500 = reached the nil service).
func TestAdminToken_RightScopeAdmitted(t *testing.T) {
	rg := newAdminTokenRig(t)
	cases := []struct {
		method, path, body string
		scope              string
	}{
		{http.MethodGet, InternalAdminPrefix + "/stats", "", PermSocialStatsRead},
		{http.MethodGet, InternalAdminPrefix + "/tube/stats", "", PermTubeStatsRead},
		{http.MethodPatch, commentModerationPath(), `{"status":"removed"}`, PermCommentsRemove},
		{http.MethodGet, InternalAdminPrefix + "/reports?target_type=video", "", PermTubeReportsAct},
		{http.MethodGet, InternalAdminPrefix + "/posts/review-queue?kind=reel", "", PermReelsModerate},
	}
	for _, tc := range cases {
		tok := rg.mint(t, rg.admin, AudiencePost, []string{tc.scope}, rg.actor.String())
		hdr := bearer(tok) // no internal key: the family is outside the key gate
		if w := adminServe(rg.r, tc.method, tc.path, tc.body, hdr); w.Code != http.StatusInternalServerError {
			t.Fatalf("%s %s with %s: status=%d body=%s, want 500 (admitted to nil service)", tc.method, tc.path, tc.scope, w.Code, w.Body.String())
		}
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
		{"wrong audience", rg.mint(t, rg.admin, "food", []string{PermCommentsModerate}, actor), 0, CodeServiceTokenRejected},
		{"payments audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, []string{PermCommentsModerate}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsModerate}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsModerate}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsModerate}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudiencePost, []string{PermSocialStatsRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsModerate}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudiencePost, []string{PermCommentsModerate}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudiencePost, []string{PermCommentsModerate}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			hdr := edgeModerator(uuid.New())
			hdr[ServiceAuthHeader] = "Bearer " + tc.tok
			w := adminServe(rg.r, http.MethodPatch, commentModerationPath(), `{"status":"visible"}`, hdr)
			if w.Code != http.StatusForbidden || adminErrorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, adminErrorCode(t, w), w.Body.String(), tc.wantCode)
			}
		})
	}
}

// The edge cannot reach the token-only family with the key the gateway stamps
// plus a forged moderator: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.New()
	checked := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		hdr := edgeModerator(forged)
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Actor-Id"] = forged.String()
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), `{"status":"removed"}`, hdr)
		if w.Code != http.StatusUnauthorized || adminErrorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, ri.Path, w.Code, adminErrorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRoutePermissions()) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(adminRoutePermissions()))
	}
}

// The key-gate exemption follows the matched route, not the URL: an
// unregistered path under the prefix still needs the key.
func TestAdminToken_UnmatchedPathUnderPrefixStillNeedsKey(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudiencePost, AdminPermissions, rg.actor.String())
	if w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/not-a-route", "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("unmatched path without key: status=%d, want 401", w.Code)
	}
}

// adminRoutePermissions is the route table under test: method + suffix →
// every permission the route's gate admits.
func adminRoutePermissions() map[string][]string {
	remove := []string{PermCommentsModerate, PermCommentsRemove}
	return map[string][]string{
		"GET /stats":                            {PermSocialStatsRead},
		"GET /tube/stats":                       {PermTubeStatsRead},
		"GET /posts/review-queue":               anyPostPerm,
		"GET /posts/:postId":                    anyPostPerm,
		"GET /posts/:postId/moderation-history": anyPostPerm,
		"POST /posts/:postId/moderation":        anyPostPerm,
		"POST /posts/review-status":             anyPostPerm,
		"POST /posts/visibility":                anyPostPerm,
		"GET /reels/flagged":                    {PermReelsModerate, PermReelsRemove},
		"GET /reels/:reelId/moderation":         {PermReelsModerate, PermReelsRemove},
		"GET /reports":                          {PermSocialReportsAct, PermTubeReportsAct},
		"PATCH /reports/:reportId":              {PermSocialReportsAct, PermTubeReportsAct},
		"GET /comments/moderation":              remove,
		"GET /comments/:commentId/audit":        remove,
		"PATCH /comments/:commentId/moderation": remove,
		"GET /channels/search":                  {PermTubeChannelsModerate},
		"GET /channels/:ref":                    {PermTubeChannelsModerate},
		"GET /creators/:userId/counts":          {PermSocialUsersRead},
		"GET /creators/:userId/video-series":    {PermVideosModerate, PermVideosRemove},
	}
}

func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	want := adminRoutePermissions()
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		perms, ok := want[ri.Method+" "+strings.TrimPrefix(ri.Path, InternalAdminPrefix)]
		if !ok {
			t.Fatalf("undeclared admin route %s %s", ri.Method, ri.Path)
		}
		seen++
		admitted := map[string]bool{}
		for _, p := range perms {
			admitted[p] = true
		}
		var others []string
		for _, p := range AdminPermissions {
			if !admitted[p] {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudiencePost, others, rg.actor.String())
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), `{"status":"removed"}`, bearer(tok))
		if w.Code != http.StatusForbidden || adminErrorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %v: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perms, w.Code, adminErrorCode(t, w), CodeAdminPermissionScope)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d admin routes, want %d", seen, len(want))
	}
}

// Takedowns need .remove; restores need .moderate. Refusals happen before the
// service (403); admissions reach the nil service (500).
func TestAdminToken_CommentStatusNarrowsPerAction(t *testing.T) {
	rg := newAdminTokenRig(t)
	moderate := bearer(rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsModerate}, rg.actor.String()))
	remove := bearer(rg.mint(t, rg.admin, AudiencePost, []string{PermCommentsRemove}, rg.actor.String()))
	for _, status := range []string{"hidden", "removed"} {
		body := `{"status":"` + status + `"}`
		if w := adminServe(rg.r, http.MethodPatch, commentModerationPath(), body, moderate); w.Code != http.StatusForbidden || adminErrorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s with moderate: status=%d code=%q, want 403", status, w.Code, adminErrorCode(t, w))
		}
		if w := adminServe(rg.r, http.MethodPatch, commentModerationPath(), body, remove); w.Code != http.StatusInternalServerError {
			t.Fatalf("%s with remove: status=%d, want admitted", status, w.Code)
		}
	}
	for _, status := range []string{"visible", "review"} {
		body := `{"status":"` + status + `"}`
		if w := adminServe(rg.r, http.MethodPatch, commentModerationPath(), body, remove); w.Code != http.StatusForbidden {
			t.Fatalf("%s with only remove: status=%d, want 403", status, w.Code)
		}
		if w := adminServe(rg.r, http.MethodPatch, commentModerationPath(), body, moderate); w.Code != http.StatusInternalServerError {
			t.Fatalf("%s with moderate: status=%d, want admitted", status, w.Code)
		}
	}
}

// Tube and Social stay apart where the kind is known from the request.
func TestAdminToken_KindFromRequestNarrows(t *testing.T) {
	rg := newAdminTokenRig(t)
	social := bearer(rg.mint(t, rg.admin, AudiencePost, []string{PermSocialReportsAct, PermPostsModerate}, rg.actor.String()))
	tube := bearer(rg.mint(t, rg.admin, AudiencePost, []string{PermTubeReportsAct, PermVideosModerate}, rg.actor.String()))
	refused := []struct {
		path string
		hdr  map[string]string
	}{
		{InternalAdminPrefix + "/reports?target_type=video", social},
		{InternalAdminPrefix + "/reports?target_type=post", tube},
		{InternalAdminPrefix + "/reports?target_type=comment", tube},
		{InternalAdminPrefix + "/posts/review-queue?kind=video", social},
		{InternalAdminPrefix + "/posts/review-queue?kind=post", tube},
		{InternalAdminPrefix + "/posts/review-queue?kind=reel", social},
	}
	for _, tc := range refused {
		if w := adminServe(rg.r, http.MethodGet, tc.path, "", tc.hdr); w.Code != http.StatusForbidden || adminErrorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s: status=%d code=%q, want 403", tc.path, w.Code, adminErrorCode(t, w))
		}
	}
}

// LEGACY routes are unchanged: moderator headers plus the key, never a token.
func TestAdminToken_LegacyRoutesUnchanged(t *testing.T) {
	rg := newAdminTokenRig(t)
	mod := uuid.New()
	path := "/v1/admin/comments/" + uuid.NewString() + "/moderation"
	if w := adminServe(rg.r, http.MethodPatch, path, `{"status":"hidden"}`, edgeModerator(mod)); w.Code != http.StatusInternalServerError {
		t.Fatalf("legacy moderator: status=%d, want admitted", w.Code)
	}
	user := edgeModerator(mod)
	user["X-Scopes"] = "user"
	if w := adminServe(rg.r, http.MethodPatch, path, `{"status":"hidden"}`, user); w.Code != http.StatusForbidden {
		t.Fatalf("legacy plain user: status=%d, want 403", w.Code)
	}
	tok := rg.mint(t, rg.admin, AudiencePost, AdminPermissions, rg.actor.String())
	if w := adminServe(rg.r, http.MethodPatch, path, `{"status":"hidden"}`, bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy with only a token: status=%d, want 401 (key required)", w.Code)
	}
	withKey := bearer(tok)
	withKey["X-Internal-Service-Key"] = adminTestKey
	if w := adminServe(rg.r, http.MethodPatch, path, `{"status":"hidden"}`, withKey); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy with token and key but no moderator: status=%d, want 401", w.Code)
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/posts/internal/review-status", `{"post_id":"`+uuid.NewString()+`","status":"approved"}`, bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("internal review-status with only a token: status=%d, want 401", w.Code)
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudiencePost, []string{PermSocialStatsRead}, rg.actor.String())
	r := adminTokenRouter(nil)
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusUnauthorized || adminErrorCode(t, w) != CodeServiceCredentialRequired {
		t.Fatalf("no verifier: status=%d code=%q, want 401 %s", w.Code, adminErrorCode(t, w), CodeServiceCredentialRequired)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	if v, err := ServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil, nil", v, err)
	}
	pub, _, _ := servicetoken.GenerateKeypair()
	for name, env := range map[string]map[string]string{
		"missing key": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermSocialStatsRead},
		"missing ops": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
	} {
		if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("%s: want a configuration error", name)
		}
	}
}

func TestPermissionMappings(t *testing.T) {
	cases := map[[2]string]string{
		{"post", "reject"}: PermPostsRemove, {"post", "approve"}: PermPostsModerate, {"post", "needs_changes"}: PermPostsModerate,
		{"reel", "rejected"}: PermReelsRemove, {"reel", "approved"}: PermReelsModerate,
		{"video", "reject"}: PermVideosRemove, {"video", "promote"}: PermVideosModerate,
	}
	for in, want := range cases {
		if got := PostActionPermission(in[0], in[1]); got != want {
			t.Fatalf("PostActionPermission(%s,%s)=%s, want %s", in[0], in[1], got, want)
		}
	}
	if ReportPermission("video") != PermTubeReportsAct || ReportPermission("comment") != PermSocialReportsAct {
		t.Fatal("ReportPermission mapping")
	}
}
