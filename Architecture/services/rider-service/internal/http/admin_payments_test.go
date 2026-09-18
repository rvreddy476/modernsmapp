// Admin payments-lane routes (refunds, ride-payment lists, fare windows and
// the surge state): declared in the token route table under the payments /
// fares permissions and audited with their own action names. No database:
// a bad body or a missing query proves the token was admitted and the
// handler ran as the signed actor, exactly like the other admin tests.
package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Mutation guard: the refund route refuses a token without the settle
// permission (a payments read is not enough), and a refused call is never
// audited.
func TestAdminToken_RefundNeedsSettlePermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	path := InternalAdminPrefix + "/rides/" + uuid.NewString() + "/refund"
	body := `{"amount_paise":5000,"reason":"captain ended the ride early"}`
	for _, other := range []string{PermPaymentsRead, PermPaymentsReject, PermFaresManage, PermRidesCancel} {
		tok := rg.mint(t, rg.admin, AudienceRider, []string{other}, rg.actor.String())
		w := adminServe(rg.r, http.MethodPost, path, body, bearer(tok))
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("refund with %s: status=%d code=%q, want 403 %s", other, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Fatalf("refused refunds were audited (%d rows)", n)
	}
	if w := adminServe(rg.r, http.MethodPost, path, body, map[string]string{"X-Internal-Service-Key": adminTestInternalKey, "X-User-Id": rg.actor.String(), "X-Scopes": "superadmin"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("refund with the internal key and no token: %d", w.Code)
	}
	// The settle permission admits the call; over a nil store a bad body
	// answers 400 after the actor was resolved, and the row is audited.
	tok := rg.mint(t, rg.admin, AudienceRider, []string{PermPaymentsSettle}, rg.actor.String())
	w := adminServe(rg.r, http.MethodPost, path, `not json`, bearer(tok))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("refund with settle, bad body: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := rg.audit.all()
	if len(rows) != 1 || rows[0].AdminUserID != rg.actor || rows[0].Action != "ride_payment.refund" {
		t.Fatalf("audit rows = %+v", rows)
	}
}

func TestAdminToken_PaymentsLaneRoutesAreAudited(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.NewString()
	cases := []struct {
		method, path, perm, other, body, action string
		want                                    int
	}{
		{http.MethodGet, "/refunds?ride_id=nope", PermPaymentsRead, PermFaresManage, "", "ride_payment.refunds.list", http.StatusBadRequest},
		{http.MethodGet, "/ride-payments?from=yesterday", PermPaymentsRead, PermPaymentsSettle, "", "ride_payment.list", http.StatusBadRequest},
		{http.MethodPost, "/fare-windows", PermFaresManage, PermPaymentsSettle, `not json`, "fare_window.create", http.StatusBadRequest},
		{http.MethodPatch, "/fare-windows/" + id, PermFaresManage, PermCitiesManage, `not json`, "fare_window.update", http.StatusBadRequest},
		{http.MethodPost, "/fare-windows/not-a-uuid/deactivate", PermFaresManage, PermPaymentsRead, `{}`, "fare_window.deactivate", http.StatusBadRequest},
		{http.MethodGet, "/fare-windows?city_id=nope", PermFaresManage, PermRidesRead, "", "fare_window.list", http.StatusBadRequest},
		{http.MethodGet, "/surge", PermFaresManage, PermReportsRead, "", "surge.view", http.StatusBadRequest},
	}
	for _, tc := range cases {
		wrong := rg.mint(t, rg.admin, AudienceRider, []string{tc.other}, rg.actor.String())
		if w := adminServe(rg.r, tc.method, InternalAdminPrefix+tc.path, tc.body, bearer(wrong)); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s with %s: status=%d code=%q, want 403 %s", tc.method, tc.path, tc.other, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
		rg.audit.reset()
		right := rg.mint(t, rg.admin, AudienceRider, []string{tc.perm}, rg.actor.String())
		w := adminServe(rg.r, tc.method, InternalAdminPrefix+tc.path, tc.body, bearer(right))
		if w.Code != tc.want {
			t.Fatalf("%s %s with %s: status=%d body=%s, want %d (admitted)", tc.method, tc.path, tc.perm, w.Code, w.Body.String(), tc.want)
		}
		rows := rg.audit.all()
		if len(rows) != 1 {
			t.Fatalf("%s %s: audit rows=%d, want 1", tc.method, tc.path, len(rows))
		}
		if rows[0].AdminUserID != rg.actor || rows[0].Action != tc.action {
			t.Fatalf("%s %s: audit actor=%s action=%q, want %s %q", tc.method, tc.path, rows[0].AdminUserID, rows[0].Action, rg.actor, tc.action)
		}
	}
}
