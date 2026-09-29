// Standing route (Copyright Match plan T4-2 / T4-3), no database: the
// standing source is a fake, so these prove the guards and the wire
// contract. The policy itself is table-tested in internal/service; the
// store filter and the end-to-end path are in standing_integration_test.go.
package http

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// standingRig is the admin rig plus a registered post-service caller and a
// fake standing source.
type standingRig struct {
	*adminTokenRig
	post *servicetoken.Signer // post-service, registered for OpStandingRead (and, over-granted, the admin perms)
	fake *fakeStanding
}

type fakeStanding struct {
	standing *service.Standing
	err      error
	calls    int
}

func (f *fakeStanding) Standing(context.Context, uuid.UUID) (*service.Standing, error) {
	f.calls++
	return f.standing, f.err
}

func newStandingRig(t *testing.T) *standingRig {
	t.Helper()
	rg := newAdminTokenRig(t)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// post-service is deliberately over-granted with every admin permission:
	// the admin family must still refuse it by issuer, not by scope.
	if err := rg.v.RegisterBase64("post-service", "p1", pub, append([]string{OpStandingRead}, AdminPermissions...), nil); err != nil {
		t.Fatal(err)
	}
	// admin-service is deliberately over-granted with standing.read too:
	// the standing route must still refuse it (allowlist, not scope).
	if err := rg.v.RegisterBase64(IssuerAdminService, "a1", rg.adminPub, append([]string{OpStandingRead}, AdminPermissions...), nil); err != nil {
		t.Fatal(err)
	}
	post, err := servicetoken.NewSignerFromBase64("post-service", "p1", priv)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeStanding{standing: fixtureStanding()}
	rg.r = newTokenTestRouter(t, New(nil).WithServiceAuth(rg.v).WithStandingReader(fake))
	return &standingRig{adminTokenRig: rg, post: post, fake: fake}
}

// fixtureStanding is the scenario pinned by testdata/contracts/standing.v1.json.
func fixtureStanding() *service.Standing {
	caseID := uuid.MustParse("7d2f9c3a-1b4e-4f6a-8c9d-0e1f2a3b4c5d")
	severeIssued := time.Date(2026, 9, 28, 9, 30, 0, 0, time.UTC)
	warnIssued := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	until := severeIssued.Add(postgres.StrikeDuration)
	return &service.Standing{
		UserID:         uuid.MustParse("5e0a7b2c-3d4e-4f5a-9b6c-7d8e9f0a1b2c"),
		Standing:       service.StandingSuspended,
		PolicyVersion:  service.StandingPolicyVersion,
		SuspendedUntil: &until,
		ActiveStrikes: []postgres.UserStrike{
			{ID: uuid.MustParse("0b6c1d4e-8f2a-4c3b-9d1e-2f3a4b5c6d7e"), Severity: "severe_strike", Reason: "copyright: upheld case",
				CaseID: &caseID, CreatedAt: severeIssued.In(time.FixedZone("IST", 5*3600+1800)), ExpiresAt: until},
			{ID: uuid.MustParse("1c7d2e5f-9a3b-4d4c-8e2f-3a4b5c6d7e8f"), Severity: "warning", Reason: "community guidelines: first notice",
				CreatedAt: warnIssued, ExpiresAt: warnIssued.Add(postgres.StrikeDuration)},
		},
		EvaluatedAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
	}
}

func (rg *standingRig) postToken(t *testing.T, scope []string) string {
	t.Helper()
	tok, err := rg.post.Mint(AudienceTrustSafety, "standing", scope, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// withKey adds the internal key to a header set.
func withKey(hdr map[string]string) map[string]string {
	hdr["X-Internal-Service-Key"] = tokenTestInternalKey
	return hdr
}

var standingURL = "/v1/internal/standing/" + uuid.NewString()

func TestStanding_ContractGolden(t *testing.T) {
	rg := newStandingRig(t)
	want, err := os.ReadFile("testdata/contracts/standing.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	w := serveAdmin(rg.r, http.MethodGet, standingURL, "", withKey(bearer(rg.postToken(t, []string{OpStandingRead}))))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != strings.TrimSpace(string(want)) {
		t.Fatalf("body differs from the pinned contract:\n got: %s\nwant: %s", got, strings.TrimSpace(string(want)))
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=60" {
		t.Fatalf("Cache-Control=%q", cc)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type=%q", ct)
	}
	etag := w.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"`) || len(etag) != 34 {
		t.Fatalf("ETag=%q, want a strong 32-hex etag", etag)
	}
	// Same answer → same ETag → 304 on If-None-Match; a different one → 200.
	hdr := withKey(bearer(rg.postToken(t, []string{OpStandingRead})))
	hdr["If-None-Match"] = etag
	if w2 := serveAdmin(rg.r, http.MethodGet, standingURL, "", hdr); w2.Code != http.StatusNotModified || w2.Body.Len() != 0 || w2.Header().Get("ETag") != etag {
		t.Fatalf("If-None-Match: status=%d body=%q etag=%q", w2.Code, w2.Body.String(), w2.Header().Get("ETag"))
	}
	hdr["If-None-Match"] = `W/` + etag
	if w3 := serveAdmin(rg.r, http.MethodGet, standingURL, "", hdr); w3.Code != http.StatusNotModified {
		t.Fatalf("weak If-None-Match: status=%d", w3.Code)
	}
	hdr["If-None-Match"] = `"stale"`
	if w4 := serveAdmin(rg.r, http.MethodGet, standingURL, "", hdr); w4.Code != http.StatusOK {
		t.Fatalf("stale If-None-Match: status=%d", w4.Code)
	}
	if rg.fake.calls != 4 {
		t.Fatalf("standing source called %d times, want 4 (every request re-evaluates; the cache is the caller's)", rg.fake.calls)
	}
}

func TestStanding_OKUserHasEmptyArrayAndNullSuspension(t *testing.T) {
	rg := newStandingRig(t)
	rg.fake.standing = &service.Standing{Standing: service.StandingOK, PolicyVersion: service.StandingPolicyVersion, ActiveStrikes: nil}
	w := serveAdmin(rg.r, http.MethodGet, standingURL, "", withKey(bearer(rg.postToken(t, []string{OpStandingRead}))))
	want := `{"data":{"standing":"ok","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("status=%d body=%s\nwant %s", w.Code, w.Body.String(), want)
	}
}

func TestStanding_Refusals(t *testing.T) {
	rg := newStandingRig(t)
	postTok := rg.postToken(t, []string{OpStandingRead})
	adminTok := rg.mint(t, rg.admin, AudienceTrustSafety, append([]string{OpStandingRead}, PermStrikesRead), rg.actor.String())
	cases := []struct {
		name     string
		hdr      map[string]string
		clock    time.Duration
		wantCode int
		wantErr  string
	}{
		{"no internal key, valid token", bearer(postTok), 0, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"internal key only", withKey(map[string]string{}), 0, http.StatusUnauthorized, CodeServiceCredentialRequired},
		{"internal key plus forged admin scopes, no token", withKey(gatewayAdmin(uuid.New(), "admin superadmin")), 0, http.StatusUnauthorized, CodeServiceCredentialRequired},
		{"admin-service token carrying standing.read", withKey(bearer(adminTok)), 0, http.StatusForbidden, CodeServiceCallerRefused},
		{"post-service token without the scope", withKey(bearer(rg.postToken(t, []string{"trust_safety:reports.read"}))), 0, http.StatusForbidden, CodeServiceScopeDenied},
		{"expired post-service token", withKey(bearer(postTok)), 2 * time.Minute, http.StatusForbidden, CodeServiceCallerRefused},
		{"garbage token", withKey(bearer("a.b.c")), 0, http.StatusForbidden, CodeServiceCallerRefused},
		{"unregistered key claiming post-service", withKey(bearer(rogueToken(t, "post-service", "p1", []string{OpStandingRead}))), 0, http.StatusForbidden, CodeServiceCallerRefused},
		{"wrong audience", withKey(bearer(mintFor(t, rg.post, "dating", []string{OpStandingRead}))), 0, http.StatusForbidden, CodeServiceCallerRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			before := rg.fake.calls
			w := serveAdmin(rg.r, http.MethodGet, standingURL, "", tc.hdr)
			if w.Code != tc.wantCode || adminErrorCode(w) != tc.wantErr {
				t.Fatalf("status=%d code=%q body=%s, want %d %s", w.Code, adminErrorCode(w), w.Body.String(), tc.wantCode, tc.wantErr)
			}
			if rg.fake.calls != before {
				t.Fatal("a refused request must not evaluate standing")
			}
		})
	}
}

// A post-service token opens the standing route and nothing else: every
// admin route refuses it (issuer check), and so does a legacy admin route.
func TestStanding_PostServiceTokenOpensNothingElse(t *testing.T) {
	rg := newStandingRig(t)
	tok := rg.postToken(t, append([]string{OpStandingRead}, AdminPermissions...))
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		path := strings.NewReplacer(":id", uuid.NewString(), ":userId", uuid.NewString(), ":mediaId", uuid.NewString()).Replace(ri.Path)
		w := serveAdmin(rg.r, ri.Method, path, `{"status":"reviewing"}`, bearer(tok))
		if w.Code != http.StatusForbidden || adminErrorCode(w) != CodeServiceTokenRejected {
			t.Fatalf("%s %s with a post-service token: status=%d code=%q, want 403 %s", ri.Method, ri.Path, w.Code, adminErrorCode(w), CodeServiceTokenRejected)
		}
	}
	hdr := withKey(bearer(tok))
	if w := serveAdmin(rg.r, http.MethodGet, "/v1/strikes/"+uuid.NewString(), "", hdr); w.Code != http.StatusForbidden {
		t.Fatalf("legacy admin read with a post-service token: status=%d, want 403", w.Code)
	}
}

func TestStanding_BadUserAndUnavailable(t *testing.T) {
	rg := newStandingRig(t)
	hdr := withKey(bearer(rg.postToken(t, []string{OpStandingRead})))
	for _, bad := range []string{"not-a-uuid", uuid.Nil.String()} {
		if w := serveAdmin(rg.r, http.MethodGet, "/v1/internal/standing/"+bad, "", hdr); w.Code != http.StatusBadRequest {
			t.Fatalf("user %q: status=%d, want 400", bad, w.Code)
		}
	}
	rg.fake.err = errors.New("db down")
	w := serveAdmin(rg.r, http.MethodGet, standingURL, "", hdr)
	if w.Code != http.StatusInternalServerError || adminErrorCode(w) != "STANDING_UNAVAILABLE" || w.Header().Get("Cache-Control") != "" {
		t.Fatalf("source error: status=%d code=%q cache=%q, want 500 STANDING_UNAVAILABLE and no cache header", w.Code, adminErrorCode(w), w.Header().Get("Cache-Control"))
	}
	// No source at all (a Handler built with a nil service) is 503, never ok.
	r := newTokenTestRouter(t, New(nil).WithServiceAuth(rg.v))
	if w := serveAdmin(r, http.MethodGet, standingURL, "", hdr); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no source: status=%d, want 503", w.Code)
	}
}

func rogueToken(t *testing.T, issuer, kid string, scope []string) string {
	t.Helper()
	_, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	s, err := servicetoken.NewSignerFromBase64(issuer, kid, priv)
	if err != nil {
		t.Fatal(err)
	}
	return mintFor(t, s, AudienceTrustSafety, scope)
}

func mintFor(t *testing.T, s *servicetoken.Signer, aud string, scope []string) string {
	t.Helper()
	tok, err := s.Mint(aud, "standing", scope, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
