// Strike routes (Copyright Match plan T5-5), no database: the legacy
// issuing route is retired whatever the headers say, the token routes need
// strikes.manage (route table in admin_token_test.go), and a bad request is
// refused before the service is reached. Issue/void against the database
// are in strikes_integration_test.go.
package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

const strikesPath = InternalAdminPrefix + "/strikes"

func TestStrikes_LegacyIssueRouteIsRetired(t *testing.T) {
	rg := newAdminTokenRig(t)
	body := `{"user_id":"` + uuid.NewString() + `","reason":"spam","severity":"strike","idempotency_key":"k"}`
	hdrs := []map[string]string{
		gatewayAdmin(uuid.New(), "admin superadmin"),
		gatewayAdmin(uuid.New(), "admin moderator strikes.manage"),
		withKey(bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStrikesManage}, rg.actor.String()))),
	}
	for i, hdr := range hdrs {
		w := serveAdmin(rg.r, http.MethodPost, "/v1/strikes", body, hdr)
		if w.Code != http.StatusGone || adminErrorCode(w) != CodeStrikeRouteRetired {
			t.Fatalf("headers %d: status=%d code=%q, want 410 %s", i, w.Code, adminErrorCode(w), CodeStrikeRouteRetired)
		}
	}
	// Without the key it does not even get that far.
	if w := serveAdmin(rg.r, http.MethodPost, "/v1/strikes", body, map[string]string{"X-Scopes": "admin"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status=%d, want 401", w.Code)
	}
}

func TestStrikes_IssueRefusesBadRequestsBeforeTheService(t *testing.T) {
	rg := newAdminTokenRig(t) // nil service: reaching it would panic → 500 from Recovery
	tok := bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStrikesManage}, rg.actor.String()))
	user := uuid.NewString()
	cases := []struct{ name, body string }{
		{"not json", `{`},
		{"missing user", `{"reason":"spam","severity":"strike","idempotency_key":"k"}`},
		{"bad user", `{"user_id":"nope","reason":"spam","severity":"strike","idempotency_key":"k"}`},
		{"missing reason", `{"user_id":"` + user + `","severity":"strike","idempotency_key":"k"}`},
		{"bad severity", `{"user_id":"` + user + `","reason":"spam","severity":"ban","idempotency_key":"k"}`},
		{"missing idempotency key", `{"user_id":"` + user + `","reason":"spam","severity":"strike"}`},
		{"blank idempotency key", `{"user_id":"` + user + `","reason":"spam","severity":"strike","idempotency_key":"  "}`},
		{"bad case id", `{"user_id":"` + user + `","reason":"spam","severity":"strike","idempotency_key":"k","case_id":"x"}`},
		{"bad content id", `{"user_id":"` + user + `","reason":"spam","severity":"strike","idempotency_key":"k","content_id":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveAdmin(rg.r, http.MethodPost, strikesPath, tc.body, tok)
			if w.Code != http.StatusBadRequest || adminErrorCode(w) != "BAD_REQUEST" {
				t.Fatalf("status=%d code=%q body=%s, want 400 BAD_REQUEST", w.Code, adminErrorCode(w), w.Body.String())
			}
		})
	}
	// Void: bad path user, bad body, bad strike id.
	voidCases := []struct{ path, body string }{
		{strikesPath + "/nope/void", `{"strike_id":"` + uuid.NewString() + `","reason":"r"}`},
		{strikesPath + "/" + user + "/void", `{"reason":"r"}`},
		{strikesPath + "/" + user + "/void", `{"strike_id":"x","reason":"r"}`},
		{strikesPath + "/" + user + "/void", `{"strike_id":"` + uuid.NewString() + `"}`},
	}
	for i, tc := range voidCases {
		w := serveAdmin(rg.r, http.MethodPost, tc.path, tc.body, tok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("void case %d: status=%d body=%s, want 400", i, w.Code, w.Body.String())
		}
	}
}

// The gateway key plus forged identity headers never reach the token
// routes, and a token with every OTHER permission is refused
// (TestAdminToken_EveryInternalRouteNeedsItsPermission covers the table);
// this pins the strike routes specifically against a strikes.read token.
func TestStrikes_ReadPermissionCannotIssueOrVoid(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStrikesRead}, rg.actor.String()))
	user := uuid.NewString()
	if w := serveAdmin(rg.r, http.MethodPost, strikesPath, `{}`, tok); w.Code != http.StatusForbidden || adminErrorCode(w) != CodeAdminPermissionScope {
		t.Fatalf("issue with strikes.read: status=%d code=%q", w.Code, adminErrorCode(w))
	}
	if w := serveAdmin(rg.r, http.MethodPost, strikesPath+"/"+user+"/void", `{}`, tok); w.Code != http.StatusForbidden || adminErrorCode(w) != CodeAdminPermissionScope {
		t.Fatalf("void with strikes.read: status=%d code=%q", w.Code, adminErrorCode(w))
	}
}
