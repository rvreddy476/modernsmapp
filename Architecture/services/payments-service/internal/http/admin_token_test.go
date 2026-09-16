package http

// The admin console family against an in-memory admin service: who is
// admitted, who is refused, that the actor is the signed act and never a
// header, and that admin-service never reaches the money family.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/config"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeAdmin struct {
	mu        sync.Mutex
	resolves  []postgres.ResolveRefundInput
	updates   []postgres.ApplicationPresentationInput
	statsApps []string
	lookups   []string
	refunds   []postgres.NeedsAttentionFilter
}

func (f *fakeAdmin) AdminStats(_ context.Context, app string, _ time.Duration) (*postgres.AdminStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsApps = append(f.statsApps, app)
	return &postgres.AdminStats{Applications: map[string]postgres.AdminStatCounts{}}, nil
}

func (f *fakeAdmin) ListRefundsNeedingAttention(_ context.Context, fl postgres.NeedsAttentionFilter) ([]postgres.NeedsAttentionRefund, *postgres.RefundCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = append(f.refunds, fl)
	return nil, nil, nil
}

func (f *fakeAdmin) AdminGetRefund(_ context.Context, id uuid.UUID, app string) (*postgres.AdminRefund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, "refund:"+app)
	if app == "other" {
		return nil, postgres.ErrRefundCommandNotFound
	}
	return &postgres.AdminRefund{ID: id, ApplicationID: "feast"}, nil
}

func (f *fakeAdmin) ResolveRefundCommand(_ context.Context, in postgres.ResolveRefundInput) (*postgres.RefundResolution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves = append(f.resolves, in)
	return &postgres.RefundResolution{CommandID: in.CommandID, Status: "resolved", Resolution: in.Resolution}, nil
}

func (f *fakeAdmin) AdminGetIntent(_ context.Context, id uuid.UUID, app string) (*postgres.AdminIntentDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, "intent:"+app)
	if app == "other" {
		return nil, postgres.ErrIntentNotFound
	}
	return &postgres.AdminIntentDetail{Intent: postgres.AdminIntent{ID: id}, Refunds: []postgres.AdminRefund{}}, nil
}

func (f *fakeAdmin) AdminListIntents(_ context.Context, fl postgres.AdminIntentFilter) ([]postgres.AdminIntent, *postgres.RefundCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, "intents:"+fl.ApplicationID)
	return nil, nil, nil
}

func (f *fakeAdmin) AdminReconciliation(_ context.Context, app string, _ time.Duration, _ int) (*postgres.Reconciliation, error) {
	return &postgres.Reconciliation{}, nil
}

func (f *fakeAdmin) ListApplications(_ context.Context) ([]postgres.Application, error) {
	return []postgres.Application{{Key: "feast"}, {Key: "mstore"}}, nil
}

func (f *fakeAdmin) UpdateApplicationPresentation(_ context.Context, in postgres.ApplicationPresentationInput) (*postgres.ApplicationWrite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, in)
	return &postgres.ApplicationWrite{Application: postgres.Application{Key: in.Key}, Changed: true}, nil
}

func (f *fakeAdmin) AdminPaymentAudit(_ context.Context, _ postgres.PaymentAuditFilter) ([]postgres.PaymentAuditEntry, int64, error) {
	return []postgres.PaymentAuditEntry{}, 0, nil
}

func (f *fakeAdmin) AdminApplicationAudit(_ context.Context, _ string, _ int64, _ int) ([]postgres.ApplicationAuditEntry, int64, error) {
	return []postgres.ApplicationAuditEntry{}, 0, nil
}

// adminRig is a router with the admin family, an admin-service caller holding
// every admin permission, and a commerce caller holding its money ops.
type adminRig struct {
	r        *gin.Engine
	fake     *fakeAdmin
	svc      *fakeService
	verifier *servicetoken.Verifier
	admin    *servicetoken.Signer
	commerce tokenCaller
}

func newAdminRig(t *testing.T) *adminRig {
	t.Helper()
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.RegisterBase64(IssuerAdminService, "a1", pub, AdminPermissions, nil); err != nil {
		t.Fatal(err)
	}
	commerce := newTokenCaller(t, v, "commerce-service", "c1",
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate}, []string{servicetoken.RefOrder})
	return buildAdminRig(t, v, signer, commerce)
}

func buildAdminRig(t *testing.T, v *servicetoken.Verifier, signer *servicetoken.Signer, commerce tokenCaller) *adminRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	fake := &fakeAdmin{}
	svc := newFake()
	r := gin.New()
	h := New(svc).WithServiceAuth(v).WithInternalKey(testInternalKey).
		WithCallerApplications(map[string][]string{"commerce-service": {"mstore"}}).
		WithAdmin(fake, 10*time.Minute)
	if err := h.RegisterRoutes(r); err != nil {
		t.Fatal(err)
	}
	return &adminRig{r: r, fake: fake, svc: svc, verifier: v, admin: signer, commerce: commerce}
}

func mintHeader(t *testing.T, s *servicetoken.Signer, aud string, scope []string, actor string, ttl time.Duration) map[string]string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", scope, nil, ttl, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

func (a *adminRig) as(t *testing.T, actor uuid.UUID, perms ...string) map[string]string {
	return mintHeader(t, a.admin, servicetoken.AudiencePayments, perms, actor.String(), time.Minute)
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Error.Code
}

const resolveBody = `{"resolution":"written_off","note":"customer refunded by bank transfer"}`

func TestAdminToken_Admitted_ActorIsTheSignedAct(t *testing.T) {
	rig := newAdminRig(t)
	actor := uuid.New()
	cmd := uuid.New()
	hdr := rig.as(t, actor, PermRefundIssue)
	// A forged operator header rides along and must be ignored.
	hdr["X-User-Id"] = uuid.NewString()
	hdr["X-Internal-Service-Key"] = testInternalKey

	w := do(rig.r, http.MethodPost, InternalAdminPrefix+"/refunds/"+cmd.String()+"/resolve?application_id=feast", []byte(resolveBody), hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if len(rig.fake.resolves) != 1 {
		t.Fatalf("resolves = %d, want 1", len(rig.fake.resolves))
	}
	in := rig.fake.resolves[0]
	if in.OperatorID != actor.String() || in.ActorID == nil || *in.ActorID != actor {
		t.Fatalf("operator = %q actor = %v, want the act %s", in.OperatorID, in.ActorID, actor)
	}
	if in.Credential != "service_token:admin-service" || in.ApplicationID != "feast" || in.OwnerDomain != "" || in.CommandID != cmd {
		t.Fatalf("input = %+v", in)
	}

	w = do(rig.r, http.MethodPatch, InternalAdminPrefix+"/applications/feast",
		[]byte(`{"display_name":"Feast","enabled_methods":["upi","card"]}`), rig.as(t, actor, PermApplicationsManage))
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", w.Code, w.Body.String())
	}
	if u := rig.fake.updates[0]; u.OperatorID != actor.String() || u.Credential != "service_token:admin-service" ||
		u.DisplayName == nil || *u.DisplayName != "Feast" || len(u.EnabledMethods) != 2 || u.MerchantDisplayName != nil {
		t.Fatalf("update = %+v", u)
	}

	for _, tc := range []struct{ perm, path string }{
		{PermStatsRead, "/stats"},
		{PermRefundsRead, "/refunds/needs-attention"},
		{PermRefundsRead, "/refunds/" + uuid.NewString()},
		{PermIntentsRead, "/intents"},
		{PermIntentsRead, "/intents/" + uuid.NewString()},
		{PermReconciliationRead, "/reconciliation"},
		{PermApplicationsRead, "/applications"},
		{PermAuditRead, "/audit/payments"},
		{PermAuditRead, "/audit/applications"},
	} {
		if w := do(rig.r, http.MethodGet, InternalAdminPrefix+tc.path, nil, rig.as(t, actor, tc.perm)); w.Code != http.StatusOK {
			t.Errorf("GET %s with %s = %d body=%s", tc.path, tc.perm, w.Code, w.Body.String())
		}
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rig := newAdminRig(t)
	actor := uuid.New()
	stats := InternalAdminPrefix + "/stats"
	resolve := InternalAdminPrefix + "/refunds/" + uuid.NewString() + "/resolve"

	stranger, err := func() (*servicetoken.Signer, error) {
		_, priv, err := servicetoken.GenerateKeypair()
		if err != nil {
			return nil, err
		}
		return servicetoken.NewSignerFromBase64(IssuerAdminService, "unknown-kid", priv)
	}()
	if err != nil {
		t.Fatal(err)
	}
	// commerce registered with an admin permission by mistake: still refused,
	// because the admin family admits admin-service only.
	misCommerce := newTokenCaller(t, rig.verifier, "commerce-misconfigured", "cm1", []string{PermStatsRead}, []string{servicetoken.RefOrder})

	cases := []struct {
		name, method, path, body string
		hdr                      map[string]string
		status                   int
		code                     string
	}{
		{"no credential", http.MethodGet, stats, "", nil, 401, CodeAdminTokenRequired},
		{"internal key and X-User-Id only", http.MethodGet, stats, "",
			map[string]string{"X-Internal-Service-Key": testInternalKey, "X-User-Id": actor.String()}, 401, CodeAdminTokenRequired},
		{"wrong audience", http.MethodGet, stats, "",
			mintHeader(t, rig.admin, "food", []string{PermStatsRead}, actor.String(), time.Minute), 403, CodeServiceTokenRejected},
		{"missing act", http.MethodGet, stats, "",
			mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermStatsRead}, "", time.Minute), 403, CodeAdminActorRequired},
		{"malformed act", http.MethodGet, stats, "",
			mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermStatsRead}, "root", time.Minute), 403, CodeAdminActorRequired},
		{"nil act", http.MethodGet, stats, "",
			mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermStatsRead}, uuid.Nil.String(), time.Minute), 403, CodeAdminActorRequired},
		{"op not in scope", http.MethodPost, resolve, resolveBody, rig.as(t, actor, PermRefundsRead), 403, CodeAdminPermissionScope},
		{"unknown caller key", http.MethodGet, stats, "",
			mintHeader(t, stranger, servicetoken.AudiencePayments, []string{PermStatsRead}, actor.String(), time.Minute), 403, CodeServiceTokenRejected},
		{"commerce token on the admin family", http.MethodGet, stats, "", rig.commerce.header(t, servicetoken.OpIntentRead), 403, CodeAdminPermissionScope},
		{"another service holding an admin permission", http.MethodGet, stats, "", misCommerce.header(t, PermStatsRead), 403, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(rig.r, tc.method, tc.path, []byte(tc.body), tc.hdr)
			if w.Code != tc.status || errCode(t, w.Body.Bytes()) != tc.code {
				t.Fatalf("got %d %s, want %d %s; body=%s", w.Code, errCode(t, w.Body.Bytes()), tc.status, tc.code, w.Body.String())
			}
		})
	}
	if len(rig.fake.resolves) != 0 || len(rig.fake.statsApps) != 0 {
		t.Fatalf("a refused call reached the service: resolves=%d stats=%d", len(rig.fake.resolves), len(rig.fake.statsApps))
	}

	t.Run("expired", func(t *testing.T) {
		hdr := rig.as(t, actor, PermStatsRead)
		rig.verifier.SetClock(func() time.Time { return time.Now().Add(10 * time.Minute) })
		defer rig.verifier.SetClock(time.Now)
		w := do(rig.r, http.MethodGet, stats, nil, hdr)
		if w.Code != http.StatusForbidden || errCode(t, w.Body.Bytes()) != CodeServiceTokenRejected {
			t.Fatalf("got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("no verifier configured", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		h := New(newFake()).WithInternalKey(testInternalKey).WithAdmin(&fakeAdmin{}, time.Minute)
		if err := h.RegisterRoutes(r); err != nil {
			t.Fatal(err)
		}
		w := do(r, http.MethodGet, stats, nil, rig.as(t, actor, PermStatsRead))
		if w.Code != http.StatusUnauthorized || errCode(t, w.Body.Bytes()) != CodeServiceCredentialRequired {
			t.Fatalf("got %d body=%s", w.Code, w.Body.String())
		}
	})
}

// TestAdminToken_NeverReachesTheMoneyFamily registers admin-service, wrongly,
// with every money op and a reference type. Its tokens are still refused on
// every service route, and nothing reaches the service.
func TestAdminToken_NeverReachesTheMoneyFamily(t *testing.T) {
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	ops := append([]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate, OpRefundAdmin, OpApplicationAdmin}, AdminPermissions...)
	if err := v.RegisterBase64(IssuerAdminService, "a1", pub, ops, []string{servicetoken.RefOrder}); err != nil {
		t.Fatal(err)
	}
	commerce := newTokenCaller(t, v, "commerce-service", "c1", ops[:3], []string{servicetoken.RefOrder})
	rig := buildAdminRig(t, v, signer, commerce)

	tok, err := signer.Mint(servicetoken.AudiencePayments, "admin-console", ops, []string{servicetoken.RefOrder}, time.Minute,
		servicetoken.WithActor(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{ServiceAuthHeader: "Bearer " + tok}
	body, _ := json.Marshal(map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": "order", "reference_id": uuid.New(),
		"amount_minor": 1000, "method": "upi", "idempotency_key": "order:" + uuid.NewString(), "application_id": "mstore",
	})
	id := uuid.NewString()
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, internalIntents, string(body)},
		{http.MethodGet, internalIntents + "/" + id, ""},
		{http.MethodPost, internalIntents + "/" + id + "/refund", `{"amount_minor":100}`},
		{http.MethodGet, "/v1/payments/internal/refunds/needs-attention", ""},
		{http.MethodPut, "/v1/payments/internal/applications/feast", `{}`},
	} {
		w := do(rig.r, tc.method, tc.path, []byte(tc.body), hdr)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403; body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if len(rig.svc.initiations) != 0 {
		t.Fatalf("admin-service opened %d intents", len(rig.svc.initiations))
	}

	// The same misconfiguration is refused at boot.
	if err := ValidateCallerPolicy(IssuerAdminService, ops, nil); err == nil {
		t.Fatal("boot accepted admin-service holding payments:intent.create")
	}
}

func TestValidateCallerPolicy(t *testing.T) {
	cases := []struct {
		name      string
		caller    string
		ops, refs []string
		ok        bool
	}{
		{"admin-service with admin permissions", IssuerAdminService, AdminPermissions, nil, true},
		{"admin-service with intent create", IssuerAdminService, []string{PermStatsRead, servicetoken.OpIntentCreate}, nil, false},
		{"admin-service with refund admin", IssuerAdminService, []string{OpRefundAdmin}, nil, false},
		{"admin-service with reference types", IssuerAdminService, []string{PermStatsRead}, []string{"order"}, false},
		{"admin-service with no ops", IssuerAdminService, nil, nil, false},
		{"commerce as today", "commerce-service", moneyOps, []string{"order"}, true},
		{"commerce with refund admin as today", "commerce-service", []string{servicetoken.OpIntentRead, OpRefundAdmin, OpApplicationAdmin}, []string{"order"}, true},
		{"commerce with an admin permission", "commerce-service", []string{servicetoken.OpIntentRead, PermRefundsRead}, []string{"order"}, false},
		{"commerce with no reference types", "commerce-service", moneyOps, nil, false},
	}
	for _, tc := range cases {
		err := ValidateCallerPolicy(tc.caller, tc.ops, tc.refs)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestAdmin_ApplicationConfinement(t *testing.T) {
	rig := newAdminRig(t)
	actor := uuid.New()
	id := uuid.NewString()

	if w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/intents/"+id+"?application_id=other", nil, rig.as(t, actor, PermIntentsRead)); w.Code != http.StatusNotFound {
		t.Fatalf("other application's intent = %d, want 404", w.Code)
	}
	if w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/refunds/"+id+"?application_id=other", nil, rig.as(t, actor, PermRefundsRead)); w.Code != http.StatusNotFound {
		t.Fatalf("other application's refund = %d, want 404", w.Code)
	}
	if w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/intents?application_id=Bad!", nil, rig.as(t, actor, PermIntentsRead)); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed application_id = %d, want 400", w.Code)
	}
	do(rig.r, http.MethodGet, InternalAdminPrefix+"/intents?application_id=feast", nil, rig.as(t, actor, PermIntentsRead))
	do(rig.r, http.MethodGet, InternalAdminPrefix+"/stats?application_id=feast", nil, rig.as(t, actor, PermStatsRead))
	do(rig.r, http.MethodGet, InternalAdminPrefix+"/refunds/needs-attention?application_id=feast", nil, rig.as(t, actor, PermRefundsRead))
	if got := strings.Join(rig.fake.lookups, ","); !strings.HasSuffix(got, "intents:feast") {
		t.Fatalf("lookups = %s", got)
	}
	if rig.fake.statsApps[0] != "feast" || rig.fake.refunds[0].ApplicationID != "feast" || rig.fake.refunds[0].OwnerDomain != "" {
		t.Fatalf("stats=%v refunds=%+v", rig.fake.statsApps, rig.fake.refunds)
	}

	w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/applications?application_id=feast", nil, rig.as(t, actor, PermApplicationsRead))
	var env struct {
		Data struct {
			Items []postgres.Application `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data.Items) != 1 || env.Data.Items[0].Key != "feast" {
		t.Fatalf("applications = %s", w.Body.String())
	}

	if w := do(rig.r, http.MethodPatch, InternalAdminPrefix+"/applications/mstore?application_id=feast",
		[]byte(`{"display_name":"X"}`), rig.as(t, actor, PermApplicationsManage)); w.Code != http.StatusNotFound {
		t.Fatalf("patch of another application = %d, want 404", w.Code)
	}
	for _, body := range []string{`{}`, `{"status":"disabled"}`, `{"enabled_methods":[]}`, `{"enabled_methods":["bitcoin"]}`, `{"display_name":"  "}`} {
		if w := do(rig.r, http.MethodPatch, InternalAdminPrefix+"/applications/feast", []byte(body), rig.as(t, actor, PermApplicationsManage)); w.Code != http.StatusBadRequest {
			t.Errorf("patch %s = %d, want 400", body, w.Code)
		}
	}
	if len(rig.fake.updates) != 0 {
		t.Fatalf("a refused patch reached the service: %+v", rig.fake.updates)
	}
}

func TestCallerApplications_SkipsAdminService(t *testing.T) {
	env := map[string]string{
		"SERVICE_CALLERS": "commerce-service,admin-service",
		"SERVICE_CALLER_COMMERCE_SERVICE_APPLICATIONS": "mstore",
	}
	got := config.CallerApplications(func(k string) string { return env[k] })
	if _, ok := got["admin-service"]; ok || len(got["commerce-service"]) != 1 {
		t.Fatalf("CallerApplications = %v", got)
	}
}
