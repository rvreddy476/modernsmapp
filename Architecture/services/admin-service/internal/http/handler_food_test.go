package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

const foodPrefix = "/v1/admin/food"

// foodCase gives each Feast route a body (and headers) it accepts, chosen so
// the route's declared permission is the one the request needs.
func foodCase(rt productRoute) (string, []reqOpt) {
	switch rt.operation {
	case opFoodRestaurantStatus, opFoodDeliveryPartnerState:
		return `{"status":"SUSPENDED","reason":"expired licence"}`, nil
	case opFoodRefundIssue:
		return `{"amount_paise":100,"reason":"cold food"}`, []reqOpt{withIdempotency("idem-" + uuid.NewString())}
	case opFoodRefundDecide:
		return `{"status":"rejected","reason":"not eligible"}`, nil
	case "food.settlements.generate":
		return `{"period_start":"2026-09-01","period_end":"2026-09-07"}`, []reqOpt{withIdempotency("idem-" + uuid.NewString())}
	case opFoodRestaurantMarkPaid, opFoodDeliveryMarkPaid:
		return `{"reference":"UTR123","reason":"paid by bank"}`, nil
	}
	if rt.method == http.MethodGet {
		return "", nil
	}
	return `{"reason":"checked"}`, nil
}

func TestFoodRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	if len(FoodRoutes) != 49 {
		t.Fatalf("FoodRoutes has %d entries, want 49", len(FoodRoutes))
	}
	for _, rt := range FoodRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			body, opts := foodCase(rt)
			// No second holder: two-person routes execute for the sole holder.
			routeTableCase(t, rg, foodPrefix, service.FoodAdminPrefix, "food", "food", foodAll, rt, body, opts...)
		})
	}
}

func TestFoodRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	decidedStepUp := map[string]bool{opFoodRestaurantStatus: true, opFoodDeliveryPartnerState: true} // SUSPENDED bodies
	for _, rt := range FoodRoutes {
		body, opts := foodCase(rt)
		stepUpCase(t, rg, foodPrefix, foodAll, rt, body, rt.stepUp || decidedStepUp[rt.operation], opts...)
	}
	must := []string{
		"food.restaurant.document.decide", "food.delivery_partner.kyc.read", "food.delivery_partner.document.decide",
		"food.payout_accounts.list", "food.order.cancel", opFoodRefundIssue, opFoodRefundDecide, "food.settlements.generate",
		"food.settlement_file.create", "food.settlement_file.download", "food.coupon.update",
		opFoodRestaurantMarkPaid, opFoodDeliveryMarkPaid,
	}
	for _, op := range must {
		found := false
		for _, rt := range FoodRoutes {
			if rt.operation == op && rt.stepUp {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is not declared step-up", op)
		}
	}
}

func TestFoodStatus_PermissionAndStepUpFollowTheRequestedStatus(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	approver, suspender := uuid.NewString(), uuid.NewString()
	rg.perms.grant(approver, permFoodRestaurantApprove)
	rg.perms.grant(suspender, permFoodRestaurantSuspend)
	path := foodPrefix + "/restaurants/" + uuid.NewString() + "/status"

	// Approving needs approve, no step-up; the token is scoped to approve.
	w := rg.do(http.MethodPatch, path, `{"status":" active "}`, approver, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Scope[0] != permFoodRestaurantApprove ||
		!strings.Contains(hits[0].body, `"status":"ACTIVE"`) {
		t.Fatalf("approve: %d %s hits=%+v", w.Code, w.Body.String(), hits)
	}
	// An approver cannot suspend, however the status is spelled.
	for _, s := range []string{"SUSPENDED", "suspended", " Inactive "} {
		w = rg.do(http.MethodPatch, path, `{"status":"`+s+`"}`, approver, true)
		if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
			t.Fatalf("approver %q: %d %s", s, w.Code, w.Body.String())
		}
	}
	// A suspender needs a step-up, decided before food is called.
	w = rg.do(http.MethodPatch, path, `{"status":"suspended"}`, suspender, false)
	if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("suspend without step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("refused status changes reached food: %+v", hits)
	}
	w = rg.do(http.MethodPatch, path, `{"status":"suspended","reason":"hygiene"}`, suspender, true)
	hits = rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Scope[0] != permFoodRestaurantSuspend ||
		!strings.Contains(hits[0].body, `"status":"SUSPENDED"`) {
		t.Fatalf("suspend: %d hits=%+v", w.Code, hits)
	}
	// No status at all is refused.
	if w := rg.do(http.MethodPatch, path, `{}`, suspender, true); w.Code != http.StatusBadRequest {
		t.Fatalf("empty status: %d", w.Code)
	}
}

func refundPath() (string, string) {
	id := uuid.NewString()
	return foodPrefix + "/orders/" + id + "/refund", id
}

func TestFoodRefund_ThresholdBoundary(t *testing.T) {
	const threshold = 500000
	cases := []struct {
		body      string
		twoPerson bool
	}{
		{`{"amount_paise":499999,"reason":"r"}`, false},
		{`{"amount_paise":500000,"reason":"r"}`, true}, // AT the threshold: two-person
		{`{"amount_paise":500001,"reason":"r"}`, true},
		{`{"amount":4999.99,"reason":"r"}`, false},
		{`{"amount":5000,"reason":"r"}`, true},
		{`{"amount":5000,"amount_paise":500000,"reason":"r"}`, true},
	}
	for _, tc := range cases {
		rg := newProductsRig(t, true, threshold)
		rg.holders.n = 1
		actor := uuid.NewString()
		rg.perms.grant(actor, permFoodRefundIssue)
		path, _ := refundPath()
		w := rg.do(http.MethodPost, path, tc.body, actor, true, withIdempotency("k-1"))
		hits := rg.takeHits()
		audit := rg.takeAudit()
		if tc.twoPerson {
			if w.Code != http.StatusAccepted || len(hits) != 0 || len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomePending {
				t.Fatalf("%s: want pending approval, got %d %s hits=%d audit=%+v", tc.body, w.Code, w.Body.String(), len(hits), audit)
			}
			continue
		}
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].idempotencyKey != "k-1" || len(rg.store.rows) != 0 {
			t.Fatalf("%s: want direct refund, got %d %s hits=%+v", tc.body, w.Code, w.Body.String(), hits)
		}
		if !strings.Contains(hits[0].body, `"amount":4999.99`) {
			t.Fatalf("%s: food body %s", tc.body, hits[0].body)
		}
		// Below the threshold a step-up is still required.
		if w := rg.do(http.MethodPost, path, tc.body, actor, false, withIdempotency("k-2")); !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s without step-up: %d", tc.body, w.Code)
		}
	}
}

func TestFoodRefund_BadAmountsAndMissingKeyAreRefusedBeforeFood(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	actor := uuid.NewString()
	rg.perms.grant(actor, permFoodRefundIssue)
	path, _ := refundPath()
	for _, body := range []string{`{"amount_paise":0}`, `{"amount_paise":-5}`, `{"amount_paise":1.5}`, `{"amount":-1}`,
		`{"amount":10,"amount_paise":999}`, `not json`} {
		if w := rg.do(http.MethodPost, path, body, actor, true, withIdempotency("k")); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := rg.do(http.MethodPost, path, `{"amount_paise":100}`, actor, true); w.Code != http.StatusBadRequest || !hasCode(w, CodeIdempotencyKeyRequired) {
		t.Fatalf("no key: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("refused refunds reached food: %+v", hits)
	}
}

func TestFoodRefund_FullRefundIsBoundedByTheOrderTotalOrTwoPerson(t *testing.T) {
	for _, tc := range []struct {
		name      string
		readOrder bool
		total     string
		twoPerson bool
	}{
		{"no orders.read: amount unknown", false, `{"data":{"final_amount_paise":20000}}`, true},
		{"small order", true, `{"data":{"final_amount_paise":20000}}`, false},
		{"order at the threshold", true, `{"data":{"final_amount_paise":500000}}`, true},
		{"rupee total below", true, `{"data":{"final_amount":4999.5}}`, false},
		{"order unreadable", true, `{"data":{}}`, true},
	} {
		rg := newProductsRig(t, true, 500000)
		rg.holders.n = 1
		actor := uuid.NewString()
		rg.perms.grant(actor, permFoodRefundIssue)
		if tc.readOrder {
			rg.perms.grant(actor, permFoodOrdersRead)
		}
		path, orderID := refundPath()
		rg.on(http.MethodGet, service.FoodAdminPrefix+"/orders/"+orderID, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(tc.total))
		})
		w := rg.do(http.MethodPost, path, `{"reason":"whole order"}`, actor, true, withIdempotency("k"))
		hits := rg.takeHits()
		var refunds int
		for _, h := range hits {
			if h.method == http.MethodPost {
				refunds++
				if strings.Contains(h.body, "amount") {
					t.Fatalf("%s: a full refund sent an amount: %s", tc.name, h.body)
				}
			} else if h.verified.Scope[0] != permFoodOrdersRead {
				t.Fatalf("%s: lookup scope %v", tc.name, h.verified.Scope)
			}
		}
		if tc.twoPerson != (w.Code == http.StatusAccepted) || (tc.twoPerson && refunds != 0) || (!tc.twoPerson && refunds != 1) {
			t.Fatalf("%s: status %d refunds %d %s", tc.name, w.Code, refunds, w.Body.String())
		}
	}
}

func TestFoodRefund_SecondHolderExecutesTheStoredRefundOnce(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	rg.holders.n = 1
	requester, approver := uuid.NewString(), uuid.NewString()
	rg.perms.grant(requester, permFoodRefundIssue)
	rg.perms.grant(approver, permFoodRefundIssue)
	path, orderID := refundPath()

	w := rg.do(http.MethodPost, path, `{"amount_paise":600000,"reason":"wrong order delivered"}`, requester, true, withIdempotency("idem-600"))
	a := decodeApproval(t, w)
	if a.App != "food" || a.Operation != opFoodRefundIssue || a.RequiredPermission != permFoodRefundIssue || a.TargetID != orderID ||
		a.RequestedBy != requester || a.Reason != "wrong order delivered" || !strings.Contains(a.Summary, "₹6,000.00") {
		t.Fatalf("approval %+v", a)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("the first admin's refund executed")
	}
	rg.takeAudit()

	if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"checked"}`, requester, true); !hasCode(w, CodeSelfApproval) {
		t.Fatalf("self-approval: %d %s", w.Code, w.Body.String())
	}
	w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"checked the order"}`, approver, true)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != service.FoodAdminPrefix+"/orders/"+orderID+"/refund" || hits[0].verified.Actor != approver ||
		hits[0].verified.Scope[0] != permFoodRefundIssue || hits[0].idempotencyKey != "idem-600" || !strings.Contains(hits[0].body, `"amount":6000`) {
		t.Fatalf("execution %+v", hits)
	}
	if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"again"}`, approver, true); !strings.Contains(w.Body.String(), `"replayed":true`) {
		t.Fatalf("second approve: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("the refund executed twice")
	}
}

func TestFoodRefundDecide_RejectIsStepUpOnly_ApproveFollowsTheStoredAmount(t *testing.T) {
	listing := func(id string, amount float64) string {
		return fmt.Sprintf(`{"data":{"refunds":[{"id":"%s","amount":%v,"status":"requested"},{"id":"%s","amount":1}]}}`, id, amount, uuid.NewString())
	}
	for _, tc := range []struct {
		name, body string
		canList    bool
		amount     float64
		twoPerson  bool
	}{
		{"reject", `{"status":"rejected"}`, false, 900000, false},
		{"approve below", `{"status":"APPROVED"}`, true, 4999.99, false},
		{"approve at", `{"status":"approved","reason":"verified"}`, true, 5000, true},
		{"approve unknown amount", `{"status":"approved","reason":"verified"}`, false, 1, true},
	} {
		rg := newProductsRig(t, true, 500000)
		rg.holders.n = 1
		actor := uuid.NewString()
		rg.perms.grant(actor, permFoodRefundIssue)
		if tc.canList {
			rg.perms.grant(actor, permFoodRefundsRead)
		}
		refundID := uuid.NewString()
		rg.on(http.MethodGet, service.FoodAdminPrefix+"/refunds", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(listing(refundID, tc.amount)))
		})
		path := foodPrefix + "/refunds/" + refundID + "/decide"
		if w := rg.do(http.MethodPost, path, tc.body, actor, false); !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s without step-up: %d %s", tc.name, w.Code, w.Body.String())
		}
		rg.takeHits()
		w := rg.do(http.MethodPost, path, tc.body, actor, true)
		var decides []productHit
		for _, h := range rg.takeHits() {
			if h.method == http.MethodPost {
				decides = append(decides, h)
			}
		}
		if tc.twoPerson {
			if w.Code != http.StatusAccepted || len(decides) != 0 {
				t.Fatalf("%s: want pending, got %d decides=%d %s", tc.name, w.Code, len(decides), w.Body.String())
			}
			continue
		}
		if w.Code != http.StatusOK || len(decides) != 1 || strings.Contains(decides[0].body, "APPROVED") {
			t.Fatalf("%s: %d decides=%+v", tc.name, w.Code, decides)
		}
	}
	rg := newProductsRig(t, true, 500000)
	actor := uuid.NewString()
	rg.perms.grant(actor, permFoodRefundIssue)
	if w := rg.do(http.MethodPost, foodPrefix+"/refunds/"+uuid.NewString()+"/decide", `{"status":"maybe"}`, actor, true); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown decision: %d", w.Code)
	}
}

func TestFoodMarkPaid_IsAlwaysTwoPerson(t *testing.T) {
	for _, kind := range []struct{ console, product string }{
		{"/settlements/restaurants/", "/settlements/restaurants/"},
		{"/settlements/delivery-partners/", "/settlements/delivery-partners/"},
	} {
		rg := newProductsRig(t, true, 500000)
		rg.holders.n = 1
		requester, approver := uuid.NewString(), uuid.NewString()
		rg.perms.grant(requester, permFoodSettlementMarkPaid)
		rg.perms.grant(approver, permFoodSettlementMarkPaid)
		id := uuid.NewString()
		path := foodPrefix + kind.console + id + "/mark-paid"

		if w := rg.do(http.MethodPost, path, `{"reference":"UTR9"}`, requester, true); w.Code != http.StatusBadRequest || !hasCode(w, CodeReasonRequired) {
			t.Fatalf("no reason: %d %s", w.Code, w.Body.String())
		}
		w := rg.do(http.MethodPost, path, `{"reference":"UTR9","reason":"bank file 12 sent"}`, requester, true)
		a := decodeApproval(t, w)
		if w.Code != http.StatusAccepted || a.RequiredPermission != permFoodSettlementMarkPaid || a.TargetID != id || a.Status != approvals.StatusPending {
			t.Fatalf("submit: %d %+v", w.Code, a)
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatal("mark-paid executed on the first admin's call")
		}
		w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"matched the bank file"}`, approver, true)
		hits := rg.takeHits()
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].path != service.FoodAdminPrefix+kind.product+id+"/mark-paid" ||
			hits[0].verified.Actor != approver || hits[0].verified.Scope[0] != permFoodSettlementMarkPaid || !strings.Contains(hits[0].body, "UTR9") {
			t.Fatalf("approve: %d hits=%+v", w.Code, hits)
		}
	}
}

func TestFoodSettlementDownload_RedirectIsHandedBackNotFollowed(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	actor := uuid.NewString()
	rg.perms.grant(actor, permFoodSettlementRead)
	fileID := uuid.NewString()
	presigned := "https://minio.invalid/settlements/file.csv?X-Amz-Signature=secret"
	rg.on(http.MethodGet, service.FoodAdminPrefix+"/settlements/files/"+fileID+"/download", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, presigned, http.StatusFound)
	})
	w := rg.do(http.MethodGet, foodPrefix+"/settlements/files/"+fileID+"/download", "", actor, true)
	var env struct {
		Data struct {
			DownloadURL string `json:"download_url"`
		} `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil || env.Data.DownloadURL != presigned {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 1 {
		t.Fatalf("hits %d", len(hits))
	}
	a := rg.takeAudit()
	raw, _ := json.Marshal(a)
	if len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || strings.Contains(string(raw), "Signature") || a[0].TargetID != fileID {
		t.Fatalf("audit %s", raw)
	}

	// An inline CSV passes through with its type and disposition.
	rg.on(http.MethodGet, service.FoodAdminPrefix+"/settlements/files/"+fileID+"/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="s.csv"`)
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	})
	w = rg.do(http.MethodGet, foodPrefix+"/settlements/files/"+fileID+"/download", "", actor, true)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/csv" || !strings.Contains(w.Header().Get("Content-Disposition"), "s.csv") || w.Body.String() != "a,b\n1,2\n" {
		t.Fatalf("inline: %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	// A redirect to something that is not a web URL is refused.
	rg.on(http.MethodGet, service.FoodAdminPrefix+"/settlements/files/"+fileID+"/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "javascript:alert(1)")
		w.WriteHeader(http.StatusFound)
	})
	if w = rg.do(http.MethodGet, foodPrefix+"/settlements/files/"+fileID+"/download", "", actor, true); w.Code != http.StatusBadGateway {
		t.Fatalf("bad redirect: %d %s", w.Code, w.Body.String())
	}
}

func TestFoodSettlementGenerate_ForwardsTheIdempotencyKey(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	actor := uuid.NewString()
	rg.perms.grant(actor, permFoodSettlementGenerate)
	body := `{"period_start":"2026-09-01","period_end":"2026-09-07"}`
	if w := rg.do(http.MethodPost, foodPrefix+"/settlements/generate", body, actor, true); !hasCode(w, CodeIdempotencyKeyRequired) {
		t.Fatalf("no key: %d", w.Code)
	}
	w := rg.do(http.MethodPost, foodPrefix+"/settlements/generate", body, actor, true, withIdempotency("gen-1"))
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].idempotencyKey != "gen-1" || hits[0].body != body {
		t.Fatalf("%d %+v", w.Code, hits)
	}
}

func TestProductRoutes_NoKeyIs503AndAudited(t *testing.T) {
	rg := newProductsRig(t, false, 500000)
	actor := uuid.NewString()
	rg.perms.grant(actor, permFoodStatsRead, permCommerceStatsRead, permTrustStatsRead)
	for _, path := range []string{foodPrefix + "/stats", "/v1/admin/commerce/stats", "/v1/admin/trust/stats"} {
		w := rg.do(http.MethodGet, path, "", actor, false)
		if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != 0 {
			t.Fatalf("%s audit %+v", path, a)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("reached a product without a key")
	}
}

func TestMe_ProductNavigationOnlyWithAPermissionInThatApp(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	cases := map[string]struct {
		perms []string
		want  map[string]bool
	}{
		"dating only":   {[]string{"dating:reports.act"}, map[string]bool{"food": false, "commerce": false, "trust_safety": false}},
		"feast":         {[]string{permFoodOrdersRead}, map[string]bool{"food": true, "commerce": false, "trust_safety": false}},
		"mstore":        {[]string{permSellersRead}, map[string]bool{"food": false, "commerce": true, "trust_safety": false}},
		"trust":         {[]string{permTrustAppealsAct}, map[string]bool{"food": false, "commerce": false, "trust_safety": true}},
		"platform-wide": {[]string{"*:audit.read"}, map[string]bool{"food": false, "commerce": false, "trust_safety": false}},
	}
	for name, tc := range cases {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perms...)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		for app, want := range tc.want {
			if got := strings.Contains(w.Body.String(), `"app":"`+app+`"`); got != want {
				t.Fatalf("%s: %s in navigation = %v, want %v (%s)", name, app, got, want, w.Body.String())
			}
		}
	}
}
