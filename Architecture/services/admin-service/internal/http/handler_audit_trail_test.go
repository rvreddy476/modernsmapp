package http

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type auditTrailRig struct {
	*rig
	got   []postgres.AuditTrailFilter
	page  postgres.AuditTrailPage
	fail  error
	store *memStore
}

func newAuditTrailRig(t *testing.T) *auditTrailRig {
	t.Helper()
	at := &auditTrailRig{page: postgres.AuditTrailPage{Items: []postgres.AuditTrailEntry{}}, store: newMemStore()}
	svc := &stubAdminService{listAuditTrailFn: func(_ context.Context, f postgres.AuditTrailFilter) (postgres.AuditTrailPage, error) {
		at.got = append(at.got, f)
		return at.page, at.fail
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rec := &fakeRecorder{}
	perms := &fakePerms{byUser: map[string]adminauth.Permissions{}}
	gate := NewGate(perms, rec, true)
	holders := &fakeHolders{}
	h := New(svc, gate, approvals.NewService(at.store, holders))
	if err := h.RegisterAllRoutes(r); err != nil {
		t.Fatal(err)
	}
	at.rig = &rig{r: r, gate: gate, rec: rec, perms: perms, holders: holders, store: at.store, seen: &upstreamSeen{}}
	return at
}

func TestAuditTrail_AllAppsOnlyWithTheWildcard(t *testing.T) {
	at := newAuditTrailRig(t)
	at.perms.grant(adminA, "*:audit.read")
	w := at.do(http.MethodGet, "/v1/admin/audit", "", adminA)
	if w.Code != http.StatusOK || len(at.got) != 1 || at.got[0].Apps != nil {
		t.Fatalf("wildcard: %d %s filters=%+v", w.Code, w.Body.String(), at.got)
	}
	if e := at.onlyEntry(t); e.Operation != "audit.trail.read" || e.App != "platform" || e.Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", e)
	}
}

func TestAuditTrail_AppPermissionIsRestrictedToThatApp(t *testing.T) {
	at := newAuditTrailRig(t)
	at.perms.grant(adminA, "dating:audit.read", "dating:reports.act")
	at.perms.grant(adminB, "dating:audit.read", "food:audit.read", "commerce:seller.approve")

	w := at.do(http.MethodGet, "/v1/admin/audit", "", adminA)
	if w.Code != http.StatusOK || len(at.got) != 1 || strings.Join(at.got[0].Apps, ",") != "dating" {
		t.Fatalf("dating auditor: %d filters=%+v", w.Code, at.got)
	}
	// A dating auditor asking for food rows is refused, and nothing is read.
	at.got, at.rec.entries = nil, nil
	w = at.do(http.MethodGet, "/v1/admin/audit?app=food", "", adminA)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) || len(at.got) != 0 {
		t.Fatalf("dating auditor reading food: %d %s filters=%+v", w.Code, w.Body.String(), at.got)
	}
	if e := at.onlyEntry(t); e.Outcome != postgres.AuditOutcomeDenied {
		t.Fatalf("audit %+v", e)
	}
	// Two app grants: both apps, and either may be selected.
	w = at.do(http.MethodGet, "/v1/admin/audit", "", adminB)
	apps := append([]string(nil), at.got[0].Apps...)
	sort.Strings(apps)
	if w.Code != http.StatusOK || strings.Join(apps, ",") != "dating,food" {
		t.Fatalf("two-app auditor: %d apps=%v", w.Code, apps)
	}
	if w = at.do(http.MethodGet, "/v1/admin/audit?app=food", "", adminB); w.Code != http.StatusOK || at.got[1].App != "food" {
		t.Fatalf("app=food: %d %+v", w.Code, at.got)
	}
	// Other permissions do not grant audit reading.
	at.got = nil
	at.perms.grant(nobody, "commerce:seller.approve", "food:orders.read")
	if w = at.do(http.MethodGet, "/v1/admin/audit", "", nobody); w.Code != http.StatusForbidden || len(at.got) != 0 {
		t.Fatalf("non-auditor: %d", w.Code)
	}
}

func TestAuditTrail_FiltersAreParsedAndValidated(t *testing.T) {
	at := newAuditTrailRig(t)
	at.perms.grant(adminA, "*:audit.read")
	actor := uuid.NewString()
	w := at.do(http.MethodGet, "/v1/admin/audit?app=food&actor="+actor+"&operation=food.order.cancel&outcome=denied"+
		"&from=2026-09-01T00:00:00Z&to=2026-09-17T00:00:00%2B05:30&limit=25&cursor=abc", "", adminA)
	if w.Code != http.StatusOK || len(at.got) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	f := at.got[0]
	wantTo, _ := time.Parse(time.RFC3339, "2026-09-17T00:00:00+05:30")
	if f.App != "food" || f.Actor != actor || f.Operation != "food.order.cancel" || f.Outcome != "denied" || f.Limit != 25 ||
		f.Cursor != "abc" || f.From == nil || f.From.Format(time.RFC3339) != "2026-09-01T00:00:00Z" || f.To == nil || !f.To.Equal(wantTo) {
		t.Fatalf("filter %+v", f)
	}
	for _, q := range []string{"app=Food!", "actor=bob", "outcome=maybe", "from=yesterday", "to=2026-09-17", "limit=0", "limit=x"} {
		if w := at.do(http.MethodGet, "/v1/admin/audit?"+q, "", adminA); w.Code != http.StatusBadRequest || !hasCode(w, CodeInvalidFilter) {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
	at.fail = postgres.ErrInvalidAuditCursor
	if w := at.do(http.MethodGet, "/v1/admin/audit?cursor=zzz", "", adminA); w.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", w.Code)
	}
}

func TestAuditTrail_ResponseCarriesIdsOnly(t *testing.T) {
	at := newAuditTrailRig(t)
	at.perms.grant(adminA, "*:audit.read")
	outcome := "success"
	at.page = postgres.AuditTrailPage{Items: []postgres.AuditTrailEntry{{
		ID: uuid.NewString(), App: "food", Operation: "food.order.cancel", Actor: adminB, TargetType: "food_order",
		TargetID: uuid.NewString(), Outcome: &outcome, CreatedAt: time.Now(),
	}}, NextCursor: "next"}
	w := at.do(http.MethodGet, "/v1/admin/audit", "", adminA)
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil || string(env.Data["next_cursor"]) != `"next"` {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var items []map[string]any
	_ = json.Unmarshal(env.Data["items"], &items)
	if len(items) != 1 {
		t.Fatalf("items %s", env.Data["items"])
	}
	for _, k := range []string{"reason", "payload", "email", "name"} {
		if _, ok := items[0][k]; ok {
			t.Fatalf("audit item carries %q: %v", k, items[0])
		}
	}
	for _, k := range []string{"id", "app", "operation", "actor", "target_type", "target_id", "outcome", "status_code", "request_id", "created_at"} {
		if _, ok := items[0][k]; !ok {
			t.Fatalf("audit item lacks %q: %v", k, items[0])
		}
	}
}

// GET /v1/admin/approvals returns the fields the console reads.
func TestApprovalsListShape(t *testing.T) {
	at := newAuditTrailRig(t)
	at.holders.n = 1
	approval := approvals.Approval{
		App: "food", Operation: opFoodRefundIssue, TargetType: "food_order", TargetID: "9a0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10",
		Payload: json.RawMessage(`{"amount_paise":12345678,"order_id":"9a0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10"}`),
		Requester: adminA, RequesterReason: "damaged in transit", RequiredPermission: permFoodRefundIssue,
	}
	if err := at.store.CreateApproval(context.Background(), &approval); err != nil {
		t.Fatal(err)
	}
	at.perms.grant(adminB, permFoodRefundIssue)
	w := at.do(http.MethodGet, "/v1/admin/approvals", "", adminB)
	var env struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil || len(env.Data.Items) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	item := env.Data.Items[0]
	want := map[string]any{
		"id": approval.ID, "status": "pending", "app": "food", "operation": opFoodRefundIssue,
		"summary":      "Refund Feast order 9a0c6c1e for ₹1,23,456.78",
		"requested_by": adminA, "reason": "damaged in transit",
	}
	for k, v := range want {
		if item[k] != v {
			t.Fatalf("%s = %v, want %v (%v)", k, item[k], v, item)
		}
	}
	for _, k := range []string{"requested_at", "expires_at"} {
		s, _ := item[k].(string)
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			t.Fatalf("%s = %v", k, item[k])
		}
	}
}

func TestApprovalSummaries(t *testing.T) {
	for _, c := range []struct {
		a    approvals.Approval
		want string
	}{
		{approvals.Approval{Operation: opCODSettle, TargetID: "7b0c6c1e-0f3a"}, "Settle COD remittance 7b0c6c1e"},
		{approvals.Approval{Operation: opFoodRestaurantMarkPaid, TargetID: "abc"}, "Mark restaurant settlement paid abc"},
		{approvals.Approval{Operation: opFoodRefundIssue, TargetID: "o1", Payload: json.RawMessage(`{}`)}, "Refund Feast order o1 (full refund)"},
		{approvals.Approval{Operation: opFoodRefundDecide, TargetID: "r1", Payload: json.RawMessage(`{"status":"approved"}`)}, "Decide Feast refund request r1 (approved)"},
		{approvals.Approval{Operation: "payments.refund.issue", TargetID: "x"}, "payments refund issue x"},
		{approvals.Approval{Operation: opFoodRefundIssue, Payload: json.RawMessage(`{"amount_paise":500000}`)}, "Refund Feast order for ₹5,000.00"},
		{approvals.Approval{Operation: opFoodRefundIssue, Payload: json.RawMessage(`{"amount_paise":99}`)}, "Refund Feast order for ₹0.99"},
	} {
		if got := approvalSummary(c.a); got != c.want {
			t.Fatalf("summary %q, want %q", got, c.want)
		}
	}
}
