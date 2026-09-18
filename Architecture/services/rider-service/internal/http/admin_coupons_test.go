// Admin coupon and outstanding routes: declared in the token route table
// under the fare-rules / payments permissions, audited with their own action
// names. No database: a bad body proves the token was admitted and the
// handler ran as the signed actor, exactly like the other admin tests.
package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestAdminToken_CouponAndOutstandingRoutesAreAudited(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.NewString()
	cases := []struct {
		method, path, perm, other, action string
	}{
		{http.MethodPost, "/coupons", PermFaresManage, PermPaymentsSettle, "coupon.create"},
		{http.MethodPatch, "/coupons/" + id, PermFaresManage, PermRidesRead, "coupon.update"},
		{http.MethodPost, "/outstanding/" + id + "/waive", PermPaymentsSettle, PermPaymentsRead, "outstanding.waive"},
	}
	for _, tc := range cases {
		wrong := rg.mint(t, rg.admin, AudienceRider, []string{tc.other}, rg.actor.String())
		if w := adminServe(rg.r, tc.method, InternalAdminPrefix+tc.path, `not json`, bearer(wrong)); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s with %s: status=%d code=%q, want 403 %s", tc.method, tc.path, tc.other, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
		rg.audit.reset()
		right := rg.mint(t, rg.admin, AudienceRider, []string{tc.perm}, rg.actor.String())
		w := adminServe(rg.r, tc.method, InternalAdminPrefix+tc.path, `not json`, bearer(right))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s with %s: status=%d body=%s, want 400 (admitted, bad body)", tc.method, tc.path, tc.perm, w.Code, w.Body.String())
		}
		rows := rg.audit.all()
		if len(rows) != 1 {
			t.Fatalf("%s %s: audit rows=%d, want 1", tc.method, tc.path, len(rows))
		}
		if rows[0].AdminUserID != rg.actor || rows[0].Action != tc.action {
			t.Fatalf("%s %s: audit actor=%s action=%q, want %s %q", tc.method, tc.path, rows[0].AdminUserID, rows[0].Action, rg.actor, tc.action)
		}
	}
	// The public validate route is outside both admin families: no token,
	// no audit.
	rg.audit.reset()
	if w := adminServe(rg.r, http.MethodGet, "/v1/rider/coupons/validate", "", map[string]string{"X-Internal-Service-Key": adminTestInternalKey}); w.Code != http.StatusBadRequest {
		t.Fatalf("validate without code: %d", w.Code)
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Fatalf("public route audited (%d rows)", n)
	}
}
