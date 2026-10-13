package http

import (
	"net/http"
	"testing"
)

func TestDevelopment2FASwitchFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		env, value string
		mfa, fails bool
	}{
		{"production", "true", false, true}, {"", "true", false, true}, {"local", "true", true, true},
		{"local", "typo", false, true}, {"local", "true", false, false}, {"dev", "", false, false},
	} {
		g := NewGate(&fakePerms{}, &fakeRecorder{}, tc.mfa)
		err := g.ConfigureDevelopment2FA(func(k string) string {
			if k == "ENV" {
				return tc.env
			}
			return tc.value
		})
		if (err != nil) != tc.fails {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestDevelopment2FAPauseKeepsPermissionsAndAudit(t *testing.T) {
	off := false
	rg := newRig(t, rigOpts{requireMFA: &off})
	if err := rg.gate.ConfigureDevelopment2FA(func(k string) string {
		if k == "ENV" {
			return "local"
		}
		return "true"
	}); err != nil {
		t.Fatal(err)
	}
	rg.perms.grant(adminA, commerceAll...)
	w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/kyc/verify", `{}`, adminA, noMFA, noStepUp)
	if w.Code != http.StatusOK {
		t.Fatalf("development action: %d %s", w.Code, w.Body.String())
	}
	if e := rg.onlyEntry(t); e.Actor != adminA || e.StatusCode != 200 {
		t.Fatalf("missing audit: %+v", e)
	}
	rg.rec.entries = nil
	rg.perms.grant(adminB, "dating:reports.read")
	w = rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/kyc/verify", `{}`, adminB, noMFA, noStepUp)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("permissions bypassed: %d", w.Code)
	}
}
