// Admin-service token tests (admin console Wave 2 — Feast). No database:
// a recording fake store proves admission and the actor handed to the store;
// refusals are proven by the auth layer's own status and code. Audit rows and
// stats counts are in admin_token_integration_test.go.
package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const adminTestInternalKey = "food-admin-token-test-key"

// adminFakeStore records the actor each admin write receives.
type adminFakeStore struct {
	service.Store
	actors []uuid.UUID
}

func (f *adminFakeStore) AdminSetRestaurantStatus(_ context.Context, adminID, _ uuid.UUID, _, _ string) error {
	f.actors = append(f.actors, adminID)
	return nil
}

func (f *adminFakeStore) AdminSetDeliveryPartnerStatus(_ context.Context, adminID, _ uuid.UUID, _, _ string) error {
	f.actors = append(f.actors, adminID)
	return nil
}

func (f *adminFakeStore) SetTicketStatus(_ context.Context, adminID, _ uuid.UUID, _ string) error {
	f.actors = append(f.actors, adminID)
	return nil
}

func (f *adminFakeStore) AdminGetOrder(context.Context, uuid.UUID) (*postgres.Order, error) {
	return &postgres.Order{}, nil
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
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": PermOrdersRead + "," + PermStatsRead,
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
		r:        adminTokenRouter(st, v),
		v:        v,
		st:       st,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

func adminTokenRouter(st service.Store, v *servicetoken.Verifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(service.New(st)).WithInternalKey(adminTestInternalKey).WithServiceAuth(v).RegisterRoutes(r)
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

// legacyAdmin is what the gateway would send for an admin today, key included.
func legacyAdmin(user uuid.UUID, scopes string) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": adminTestInternalKey,
		"X-User-Id":              user.String(),
		"X-Scopes":               scopes,
	}
}

// Ticket status: the handler's path to the store records the actor.
func ticketStatusPath(prefix string) string {
	return prefix + "/support/tickets/" + uuid.NewString() + "/status"
}

func TestAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, rg.actor.String())
	for _, prefix := range []string{InternalAdminPrefix, "/v1/food/admin"} {
		// A forged gateway identity and the key ride along; the actor is act.
		hdr := legacyAdmin(uuid.New(), "superadmin")
		hdr[ServiceAuthHeader] = "Bearer " + tok
		rg.st.actors = nil
		w := adminServe(rg.r, http.MethodPost, ticketStatusPath(prefix), `{"status":"resolved"}`, hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s, want 200", prefix, w.Code, w.Body.String())
		}
		if len(rg.st.actors) != 1 || rg.st.actors[0] != rg.actor {
			t.Fatalf("%s: store actors=%v, want [%s]", prefix, rg.st.actors, rg.actor)
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
		{"wrong audience", rg.mint(t, rg.admin, "dating", []string{PermTicketsAct}, actor), 0, CodeServiceTokenRejected},
		{"payments audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, []string{PermTicketsAct}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceFood, []string{PermOrdersRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceFood, []string{PermTicketsAct}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceFood, []string{PermTicketsAct}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceFood, []string{PermOrdersRead}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			path := "/orders/" + uuid.NewString()
			if tc.name != "registered non-admin caller" {
				path = "/support/tickets/" + uuid.NewString() + "/status"
			}
			for _, prefix := range []string{InternalAdminPrefix, "/v1/food/admin"} {
				hdr := legacyAdmin(uuid.New(), "superadmin")
				hdr[ServiceAuthHeader] = "Bearer " + tc.tok
				method := http.MethodPost
				if strings.HasPrefix(path, "/orders/") {
					method = http.MethodGet
				}
				rg.st.actors = nil
				w := adminServe(rg.r, method, prefix+path, `{"status":"resolved"}`, hdr)
				if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
					t.Fatalf("%s%s: status=%d code=%q body=%s, want 403 %s", prefix, path, w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
				}
				if len(rg.st.actors) != 0 {
					t.Fatalf("%s: a refused request reached the store", prefix)
				}
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
		hdr := legacyAdmin(forged, "superadmin admin moderator")
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Actor-Id"] = forged.String()
		w := adminServe(rg.r, ri.Method, path, `{"status":"resolved"}`, hdr)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRoutePermissions()) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(adminRoutePermissions()))
	}
	if len(rg.st.actors) != 0 {
		t.Fatalf("a request without a token reached the store")
	}
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

// adminRoutePermissions is the route table under test: method + suffix →
// the permission the route's gate names first.
func adminRoutePermissions() map[string]string {
	return map[string]string{
		"GET /stats":                                                  PermStatsRead,
		"GET /dashboard":                                              PermStatsRead,
		"GET /restaurants/pending":                                    PermRestaurantApprove,
		"POST /restaurants/:restaurantId/approve":                     PermRestaurantApprove,
		"POST /restaurants/:restaurantId/reject":                      PermRestaurantApprove,
		"PATCH /restaurants/:restaurantId/status":                     PermRestaurantSuspend,
		"GET /delivery-partners/pending":                              PermRiderApprove,
		"POST /delivery-partners/:partnerId/approve":                  PermRiderApprove,
		"POST /delivery-partners/:partnerId/reject":                   PermRiderApprove,
		"PATCH /delivery-partners/:partnerId/status":                  PermRiderSuspend,
		"POST /restaurants/:restaurantId/documents/:docId/decide":     PermDocumentsReview,
		"GET /delivery-partners/:partnerId/kyc":                       PermDocumentsReview,
		"POST /delivery-partners/:partnerId/documents/:docId/decide":  PermDocumentsReview,
		"GET /payout-accounts":                                        PermPayoutAccountsRead,
		"GET /orders":                                                 PermOrdersRead,
		"GET /orders/:orderId":                                        PermOrdersRead,
		"POST /orders/:orderId/cancel":                                PermOrdersCancel,
		"POST /orders/:orderId/refund":                                PermRefundIssue,
		"GET /refunds":                                                PermRefundsRead,
		"POST /refunds/:refundId/decide":                              PermRefundIssue,
		"POST /settlements/generate":                                  PermSettlementGenerate,
		"GET /settlements/restaurants":                                PermSettlementRead,
		"POST /settlements/restaurants/:settlementId/mark-paid":       PermSettlementMarkPaid,
		"GET /settlements/delivery-partners":                          PermSettlementRead,
		"POST /settlements/delivery-partners/:settlementId/mark-paid": PermSettlementMarkPaid,
		"POST /settlements/files":                                     PermSettlementGenerate,
		"GET /settlements/files":                                      PermSettlementRead,
		"GET /settlements/files/:id/download":                         PermSettlementRead,
		"GET /moderation/queue":                                       PermMenuModerate,
		"POST /moderation/menu-items/:itemId":                         PermMenuModerate,
		"DELETE /item-reviews/:reviewId":                              PermReviewsModerate,
		"GET /support/tickets":                                        PermTicketsAct,
		"POST /support/tickets/:ticketId/status":                      PermTicketsAct,
		"GET /coupons":                                                PermCouponsManage,
		"POST /coupons":                                               PermCouponsManage,
		"PATCH /coupons/:couponId":                                    PermCouponsManage,
		"GET /service-areas":                                          PermServiceAreasManage,
		"POST /service-areas":                                         PermServiceAreasManage,
		"PATCH /service-areas/:areaId":                                PermServiceAreasManage,
		"GET /reports/restaurant-sla":                                 PermReportsRead,
		"GET /reports/delivery-sla":                                   PermReportsRead,
		"GET /reports/payment-recon":                                  PermReportsRead,
		"GET /reports/refunds":                                        PermReportsRead,
		"GET /reports/coupon-abuse":                                   PermReportsRead,
		"GET /reports/compliance":                                     PermReportsRead,
		"GET /reports/orders":                                         PermReportsRead,
		"GET /reports/revenue":                                        PermReportsRead,
		"GET /fraud/top":                                              PermFraudRead,
		"GET /audit-logs":                                             PermAuditRead,
	}
}

// secondaryPermission is the narrower permission a mixed route also admits.
var secondaryPermission = map[string]string{
	"/restaurants/:restaurantId/status":    PermRestaurantApprove,
	"/delivery-partners/:partnerId/status": PermRiderApprove,
}

func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	want := adminRoutePermissions()
	seen := map[string]int{}
	for _, ri := range rg.r.Routes() {
		var suffix, family string
		switch {
		case strings.HasPrefix(ri.Path, InternalAdminPrefix+"/"):
			suffix, family = strings.TrimPrefix(ri.Path, InternalAdminPrefix), "internal"
		case strings.HasPrefix(ri.Path, "/v1/food/admin/"):
			suffix, family = strings.TrimPrefix(ri.Path, "/v1/food/admin"), "legacy"
		default:
			continue
		}
		perm, ok := want[ri.Method+" "+suffix]
		if !ok {
			t.Fatalf("undeclared admin route %s %s", ri.Method, ri.Path)
		}
		seen[family]++
		// Every OTHER permission, but not this one (nor a mixed route's
		// secondary) → refused by scope.
		var others []string
		for _, p := range AdminPermissions {
			if p != perm && p != secondaryPermission[suffix] {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceFood, others, rg.actor.String())
		hdr := bearer(tok)
		hdr["X-Internal-Service-Key"] = adminTestInternalKey
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), `{"status":"SUSPENDED"}`, hdr)
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
	}
	if seen["internal"] != len(want) || seen["legacy"] != len(want)-1 {
		t.Fatalf("saw internal=%d legacy=%d admin routes, want %d and %d", seen["internal"], seen["legacy"], len(want), len(want)-1)
	}
}

// Suspending takes a restaurant or rider off the platform: approve is not
// enough, and suspend does not cover making one ACTIVE.
func TestAdminToken_StatusNarrowsPerAction(t *testing.T) {
	rg := newAdminTokenRig(t)
	cases := []struct {
		path             string
		approve, suspend string
	}{
		{InternalAdminPrefix + "/restaurants/" + uuid.NewString() + "/status", PermRestaurantApprove, PermRestaurantSuspend},
		{InternalAdminPrefix + "/delivery-partners/" + uuid.NewString() + "/status", PermRiderApprove, PermRiderSuspend},
	}
	for _, tc := range cases {
		approveTok := rg.mint(t, rg.admin, AudienceFood, []string{tc.approve}, rg.actor.String())
		suspendTok := rg.mint(t, rg.admin, AudienceFood, []string{tc.suspend}, rg.actor.String())
		for _, status := range []string{"SUSPENDED", "CLOSED"} {
			body := `{"status":"` + status + `"}`
			if w := adminServe(rg.r, http.MethodPatch, tc.path, body, bearer(approveTok)); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
				t.Fatalf("%s %s with approve: status=%d code=%q, want 403", tc.path, status, w.Code, errorCode(t, w))
			}
			if w := adminServe(rg.r, http.MethodPatch, tc.path, body, bearer(suspendTok)); w.Code != http.StatusOK {
				t.Fatalf("%s %s with suspend: status=%d body=%s, want 200", tc.path, status, w.Code, w.Body.String())
			}
		}
		body := `{"status":"ACTIVE"}`
		if w := adminServe(rg.r, http.MethodPatch, tc.path, body, bearer(suspendTok)); w.Code != http.StatusForbidden {
			t.Fatalf("%s ACTIVE with only suspend: status=%d, want 403", tc.path, w.Code)
		}
		if w := adminServe(rg.r, http.MethodPatch, tc.path, body, bearer(approveTok)); w.Code != http.StatusOK {
			t.Fatalf("%s ACTIVE with approve: status=%d body=%s, want 200", tc.path, w.Code, w.Body.String())
		}
	}
	for _, a := range rg.st.actors {
		if a != rg.actor {
			t.Fatalf("store actor %s, want act %s", a, rg.actor)
		}
	}
}

// The LEGACY path is unchanged for a request with no token.
func TestAdminToken_LegacyScopesStillWork(t *testing.T) {
	rg := newAdminTokenRig(t)
	user := uuid.New()
	w := adminServe(rg.r, http.MethodPost, ticketStatusPath("/v1/food/admin"), `{"status":"resolved"}`, legacyAdmin(user, "admin"))
	if w.Code != http.StatusOK {
		t.Fatalf("legacy admin: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if len(rg.st.actors) != 1 || rg.st.actors[0] != user {
		t.Fatalf("legacy actors=%v, want [%s]", rg.st.actors, user)
	}
	if w := adminServe(rg.r, http.MethodGet, "/v1/food/admin/orders/"+uuid.NewString(), "", legacyAdmin(user, "moderator")); w.Code != http.StatusForbidden {
		t.Fatalf("legacy moderator: status=%d, want 403 (admin scope required)", w.Code)
	}
	noKey := legacyAdmin(user, "admin")
	delete(noKey, "X-Internal-Service-Key")
	if w := adminServe(rg.r, http.MethodGet, "/v1/food/admin/orders/"+uuid.NewString(), "", noKey); w.Code == http.StatusOK {
		t.Fatalf("legacy without the internal key: status=%d, want refused", w.Code)
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceFood, []string{PermOrdersRead}, rg.actor.String())
	r := adminTokenRouter(&adminFakeStore{}, nil)
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/orders/"+uuid.NewString(), "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	if v, err := ServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil, nil", v, err)
	}
	pub, _, _ := servicetoken.GenerateKeypair()
	for name, env := range map[string]map[string]string{
		"missing key": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermOrdersRead},
		"missing ops": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
	} {
		if _, err := ServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("%s: want a configuration error", name)
		}
	}
}
