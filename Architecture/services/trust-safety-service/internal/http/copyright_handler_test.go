// Copyright hold routes without a database (plan section 6.4): the token
// family's 401/403 (route table in admin_token_test.go), a bad request
// refused before the service (400), a reason outside the closed table
// refused before the service (422), and a well-formed request reaching
// the service (503 when no store is configured). Against the database:
// copyright_integration_test.go.
//
// Step-up is admin-service's gate (stepUp: true on the route), not a
// claim this side can verify; see copyright_handler.go.
package http

import (
	"net/http"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/google/uuid"
)

const copyrightCasesPath = InternalAdminPrefix + "/copyright/cases"

func validCreateBody() string {
	return `{"subject_post_id":"` + uuid.NewString() + `","subject_author_id":"` + uuid.NewString() + `","reason_code":"removal_upheld"}`
}

func TestCopyright_NoTokenWrongPermissionOtherCaller(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.NewString()
	routes := []struct{ method, path, body string }{
		{http.MethodPost, copyrightCasesPath, validCreateBody()},
		{http.MethodGet, copyrightCasesPath + "/" + id, ""},
		{http.MethodPost, copyrightCasesPath + "/" + id + "/place", `{"reason_code":"reinstated_on_review"}`},
		{http.MethodPost, copyrightCasesPath + "/" + id + "/release", `{"reason_code":"claim_withdrawn"}`},
	}
	strikes := bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStrikesManage, PermReportsAct, PermAppealsAct}, rg.actor.String()))
	other := bearer(rg.mint(t, rg.notifier, AudienceTrustSafety, []string{PermCopyrightAct}, rg.actor.String()))
	noActor := bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermCopyrightAct}, ""))
	for _, rt := range routes {
		// The gateway key plus a forged superadmin identity: no token, no entry.
		if w := serveAdmin(rg.r, rt.method, rt.path, rt.body, gatewayAdmin(uuid.New(), "superadmin admin")); w.Code != http.StatusUnauthorized || adminErrorCode(w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s no token: %d %s", rt.method, rt.path, w.Code, adminErrorCode(w))
		}
		if w := serveAdmin(rg.r, rt.method, rt.path, rt.body, strikes); w.Code != http.StatusForbidden || adminErrorCode(w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s strikes.manage: %d %s", rt.method, rt.path, w.Code, adminErrorCode(w))
		}
		if w := serveAdmin(rg.r, rt.method, rt.path, rt.body, other); w.Code != http.StatusForbidden || adminErrorCode(w) != CodeServiceTokenRejected {
			t.Fatalf("%s %s other caller: %d %s", rt.method, rt.path, w.Code, adminErrorCode(w))
		}
		if w := serveAdmin(rg.r, rt.method, rt.path, rt.body, noActor); w.Code != http.StatusForbidden || adminErrorCode(w) != CodeAdminActorRequired {
			t.Fatalf("%s %s no actor: %d %s", rt.method, rt.path, w.Code, adminErrorCode(w))
		}
	}
}

func TestCopyright_BadRequestsAndReasonsRefusedBeforeTheService(t *testing.T) {
	rg := newAdminTokenRig(t) // nil service: reaching it would panic → 500
	tok := bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermCopyrightAct}, rg.actor.String()))
	post, author := uuid.NewString(), uuid.NewString()
	bad := []struct{ name, body string }{
		{"not json", `{`},
		{"missing post", `{"subject_author_id":"` + author + `","reason_code":"removal_upheld"}`},
		{"bad post", `{"subject_post_id":"x","subject_author_id":"` + author + `","reason_code":"removal_upheld"}`},
		{"nil post", `{"subject_post_id":"` + uuid.Nil.String() + `","subject_author_id":"` + author + `","reason_code":"removal_upheld"}`},
		{"missing author", `{"subject_post_id":"` + post + `","reason_code":"removal_upheld"}`},
		{"bad author", `{"subject_post_id":"` + post + `","subject_author_id":"x","reason_code":"removal_upheld"}`},
		{"missing reason", `{"subject_post_id":"` + post + `","subject_author_id":"` + author + `"}`},
		{"bad case id", `{"subject_post_id":"` + post + `","subject_author_id":"` + author + `","reason_code":"removal_upheld","case_id":"x"}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if w := serveAdmin(rg.r, http.MethodPost, copyrightCasesPath, tc.body, tok); w.Code != http.StatusBadRequest || adminErrorCode(w) != "BAD_REQUEST" {
				t.Fatalf("status=%d code=%q body=%s", w.Code, adminErrorCode(w), w.Body.String())
			}
		})
	}
	// 422: a reason outside the closed table, or a release reason on a place.
	for _, reason := range []string{"spam", "claim_withdrawn", "", "REMOVAL_UPHELD"} {
		body := `{"subject_post_id":"` + post + `","subject_author_id":"` + author + `","reason_code":"` + reason + `"}`
		w := serveAdmin(rg.r, http.MethodPost, copyrightCasesPath, body, tok)
		want := http.StatusUnprocessableEntity
		if reason == "" {
			want = http.StatusBadRequest // binding:"required"
		}
		if w.Code != want {
			t.Fatalf("create reason %q: status=%d body=%s, want %d", reason, w.Code, w.Body.String(), want)
		}
	}
	id := uuid.NewString()
	for _, tc := range []struct{ path, reason string }{
		{"/release", "removal_upheld"}, {"/release", "reinstated_on_review"}, {"/release", "x"},
		{"/place", "claim_withdrawn"}, {"/place", "rule75_restore"}, {"/place", "reversed_on_review"},
	} {
		w := serveAdmin(rg.r, http.MethodPost, copyrightCasesPath+"/"+id+tc.path, `{"reason_code":"`+tc.reason+`"}`, tok)
		if w.Code != http.StatusUnprocessableEntity || adminErrorCode(w) != CodeCopyrightReasonInvalid {
			t.Fatalf("%s with %q: status=%d code=%q", tc.path, tc.reason, w.Code, adminErrorCode(w))
		}
	}
	// Transitions: bad path id, bad body.
	for _, tc := range []struct{ path, body string }{
		{copyrightCasesPath + "/nope/release", `{"reason_code":"claim_withdrawn"}`},
		{copyrightCasesPath + "/" + uuid.Nil.String() + "/release", `{"reason_code":"claim_withdrawn"}`},
		{copyrightCasesPath + "/" + id + "/release", `{`},
		{copyrightCasesPath + "/" + id + "/release", `{}`},
		{copyrightCasesPath + "/" + id + "/place", `{}`},
		{copyrightCasesPath + "/nope", ""},
	} {
		method := http.MethodPost
		if tc.body == "" {
			method = http.MethodGet
		}
		if w := serveAdmin(rg.r, method, tc.path, tc.body, tok); w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: status=%d body=%s, want 400", method, tc.path, w.Code, w.Body.String())
		}
	}
}

// A well-formed request passes every guard and reaches the service; with
// no copyright store configured the service says so (503), which proves
// the request was admitted and validated.
func TestCopyright_WellFormedRequestReachesTheService(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := newTokenTestRouter(t, New(service.New(nil, nil)).WithServiceAuth(rg.v))
	tok := bearer(rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermCopyrightAct}, rg.actor.String()))
	id := uuid.NewString()
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, copyrightCasesPath, validCreateBody()},
		{http.MethodGet, copyrightCasesPath + "/" + id, ""},
		{http.MethodPost, copyrightCasesPath + "/" + id + "/place", `{"reason_code":"reinstated_on_review"}`},
		{http.MethodPost, copyrightCasesPath + "/" + id + "/release", `{"reason_code":"rule75_restore"}`},
	} {
		if w := serveAdmin(r, tc.method, tc.path, tc.body, tok); w.Code != http.StatusServiceUnavailable || adminErrorCode(w) != CodeCopyrightUnavailable {
			t.Fatalf("%s %s: status=%d code=%q body=%s, want 503 %s", tc.method, tc.path, w.Code, adminErrorCode(w), w.Body.String(), CodeCopyrightUnavailable)
		}
	}
}
