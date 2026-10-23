// monetization-service on the standing route (creator-membership
// eligibility: no active strikes). Configured from the environment exactly as
// deployment does it, so the env names are pinned too. No database.
package http

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type monetizationStandingRig struct {
	r      *gin.Engine
	v      *servicetoken.Verifier
	mon    *servicetoken.Signer // monetization-service, kid m1
	rogue  *servicetoken.Signer // claims monetization-service, unregistered key
	stray  *servicetoken.Signer // a registered caller NOT on StandingCallers
	fake   *fakeStanding
	monPub string
}

// newMonetizationStandingRig registers post-service and monetization-service
// the way docker-compose / values-*.yaml do, plus commerce-service (a
// registered caller holding standing.read that is NOT on the allowlist).
// monOps is monetization-service's registered OPS line.
func newMonetizationStandingRig(t *testing.T, monOps string) *monetizationStandingRig {
	t.Helper()
	keys := func() (string, string) {
		pub, priv, err := servicetoken.GenerateKeypair()
		if err != nil {
			t.Fatal(err)
		}
		return pub, priv
	}
	pPub, _ := keys()
	mPub, mPriv := keys()
	cPub, cPriv := keys()
	_, rPriv := keys()
	env := map[string]string{
		"SERVICE_CALLERS":                            "post-service,monetization-service,commerce-service",
		"SERVICE_CALLER_POST_SERVICE_KID":            "p1",
		"SERVICE_CALLER_POST_SERVICE_PUBKEY":         pPub,
		"SERVICE_CALLER_POST_SERVICE_OPS":            OpStandingRead,
		"SERVICE_CALLER_MONETIZATION_SERVICE_KID":    "m1",
		"SERVICE_CALLER_MONETIZATION_SERVICE_PUBKEY": mPub,
		"SERVICE_CALLER_MONETIZATION_SERVICE_OPS":    monOps,
		"SERVICE_CALLER_COMMERCE_SERVICE_KID":        "c1",
		"SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY":     cPub,
		"SERVICE_CALLER_COMMERCE_SERVICE_OPS":        OpStandingRead,
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
	fake := &fakeStanding{standing: fixtureStanding()}
	return &monetizationStandingRig{
		r:      newTokenTestRouter(t, New(nil).WithServiceAuth(v).WithStandingReader(fake)),
		v:      v,
		mon:    mk("monetization-service", "m1", mPriv),
		rogue:  mk("monetization-service", "m1", rPriv),
		stray:  mk("commerce-service", "c1", cPriv),
		fake:   fake,
		monPub: mPub,
	}
}

// monToken mints what monetization's eligibility client mints: audience
// trust_safety, the one op (internal/client/eligibility in that service).
func monToken(t *testing.T, s *servicetoken.Signer, aud string, scope []string) string {
	t.Helper()
	tok, err := s.Mint(aud, "monetization-service", scope, nil, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestStanding_MonetizationServiceReadsStanding(t *testing.T) {
	rg := newMonetizationStandingRig(t, OpStandingRead)
	w := serveAdmin(rg.r, http.MethodGet, standingURL, "", withKey(bearer(monToken(t, rg.mon, AudienceTrustSafety, []string{OpStandingRead}))))
	if w.Code != http.StatusOK {
		t.Fatalf("monetization-service with %s: status=%d body=%s, want 200", OpStandingRead, w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"active_strikes":[`) || rg.fake.calls != 1 {
		t.Fatalf("body=%s calls=%d, want the standing contract from one evaluation", w.Body.String(), rg.fake.calls)
	}
}

func TestStanding_MonetizationServiceRefusals(t *testing.T) {
	rg := newMonetizationStandingRig(t, OpStandingRead)
	good := monToken(t, rg.mon, AudienceTrustSafety, []string{OpStandingRead})
	cases := []struct {
		name     string
		hdr      map[string]string
		wantCode int
		wantErr  string
	}{
		{"token without the internal key", bearer(good), http.StatusUnauthorized, "UNAUTHORIZED"},
		{"token without the op", withKey(bearer(monToken(t, rg.mon, AudienceTrustSafety, []string{PermStrikesRead}))), http.StatusForbidden, ""},
		{"wrong audience", withKey(bearer(monToken(t, rg.mon, "payments", []string{OpStandingRead}))), http.StatusForbidden, CodeServiceCallerRefused},
		{"unregistered key claiming monetization-service", withKey(bearer(monToken(t, rg.rogue, AudienceTrustSafety, []string{OpStandingRead}))), http.StatusForbidden, CodeServiceCallerRefused},
		{"registered caller not on StandingCallers", withKey(bearer(monToken(t, rg.stray, AudienceTrustSafety, []string{OpStandingRead}))), http.StatusForbidden, CodeServiceCallerRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := rg.fake.calls
			w := serveAdmin(rg.r, http.MethodGet, standingURL, "", tc.hdr)
			if w.Code != tc.wantCode || (tc.wantErr != "" && adminErrorCode(w) != tc.wantErr) {
				t.Fatalf("status=%d code=%q body=%s, want %d %s", w.Code, adminErrorCode(w), w.Body.String(), tc.wantCode, tc.wantErr)
			}
			if rg.fake.calls != before {
				t.Fatal("a refused request must not evaluate standing")
			}
		})
	}
}

// Registered with standing.read only, monetization-service cannot mint its
// way into anything else; and even deliberately over-granted with every admin
// permission, the admin family refuses it by issuer.
func TestStanding_MonetizationServiceTokenOpensNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ops     string
		wantErr string // the registered OPS cap the scope first; past that, the issuer check
	}{
		{"registered for standing.read only", OpStandingRead, CodeAdminPermissionScope},
		{"over-granted with every admin permission", strings.Join(append([]string{OpStandingRead}, AdminPermissions...), ","), CodeServiceTokenRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rg := newMonetizationStandingRig(t, tc.ops)
			// The strongest attempt: every admin permission AND an acting
			// admin named, so only the issuer check stands in the way.
			tok, err := rg.mon.Mint(AudienceTrustSafety, "monetization-service", append([]string{OpStandingRead}, AdminPermissions...),
				nil, time.Minute, servicetoken.WithActor(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, ri := range rg.r.Routes() {
				if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
					continue
				}
				n++
				path := strings.NewReplacer(":id", uuid.NewString(), ":userId", uuid.NewString(), ":mediaId", uuid.NewString()).Replace(ri.Path)
				for _, hdr := range []map[string]string{bearer(tok), withKey(bearer(tok))} {
					w := serveAdmin(rg.r, ri.Method, path, `{"status":"reviewing"}`, hdr)
					if w.Code != http.StatusForbidden || adminErrorCode(w) != tc.wantErr {
						t.Fatalf("%s %s with a monetization-service token: status=%d code=%q, want 403 %s", ri.Method, ri.Path, w.Code, adminErrorCode(w), tc.wantErr)
					}
				}
			}
			if n == 0 {
				t.Fatal("no admin routes registered; the test proves nothing")
			}
			if w := serveAdmin(rg.r, http.MethodGet, "/v1/strikes/"+uuid.NewString(), "", withKey(bearer(tok))); w.Code != http.StatusForbidden {
				t.Fatalf("legacy admin read with a monetization-service token: status=%d, want 403", w.Code)
			}
			// The standing route is GET only.
			for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				if w := serveAdmin(rg.r, m, standingURL, `{}`, withKey(bearer(tok))); w.Code < 400 {
					t.Fatalf("%s %s: status=%d, want a refusal", m, standingURL, w.Code)
				}
			}
			if rg.fake.calls != 0 {
				t.Fatalf("standing evaluated %d times by non-standing requests", rg.fake.calls)
			}
		})
	}
}

// A configuration naming monetization-service without its key refuses boot,
// never "allow everything".
func TestServiceCallersFromEnv_MonetizationServiceNeedsKey(t *testing.T) {
	env := map[string]string{
		"SERVICE_CALLERS":                         "monetization-service",
		"SERVICE_CALLER_MONETIZATION_SERVICE_KID": "m1",
		"SERVICE_CALLER_MONETIZATION_SERVICE_OPS": OpStandingRead,
	}
	if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("monetization-service without a public key: want a configuration error")
	}
}
