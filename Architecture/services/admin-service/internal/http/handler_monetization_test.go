package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

const monPrefix = "/v1/admin/monetization"

// monCase gives each monetization route a body it accepts.
func monCase(rt productRoute) string {
	switch rt.operation {
	case opMonRefundIssue:
		return `{"transaction_id":"` + uuid.NewString() + `","amount_paise":100,"reason":"duplicate charge"}`
	case opMonRatesSet:
		return `{"content_type":"reel","rpm_paise":1200,"reason":"new quarter"}`
	case opMonBandsSet:
		return `{"content_type":"reel","floor_bps":5000,"ceiling_bps":15000,"reason":"new quarter"}`
	case opMonBudgetSet:
		return `{"period_key":"2026-09","cap_paise":10000000,"reason":"september cap"}`
	}
	if rt.method == http.MethodGet {
		return ""
	}
	return `{"status":"approved","reason":"checked"}`
}

func TestMonetizationRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	if len(MonetizationRoutes) != 23 {
		t.Fatalf("MonetizationRoutes has %d entries, want 23", len(MonetizationRoutes))
	}
	for _, rt := range MonetizationRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			// No second holder: two-person routes execute for the sole holder.
			routeTableCase(t, rg, monPrefix, service.MonetizationAdminPrefix, "monetization", "monetization", monAll, rt, monCase(rt))
		})
	}
}

func TestMonetizationRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	for _, rt := range MonetizationRoutes {
		// Every write is step-up; no read is.
		want := rt.method != http.MethodGet
		if rt.stepUp != want {
			t.Fatalf("%s declared stepUp=%v, want %v", rt.operation, rt.stepUp, want)
		}
		stepUpCase(t, rg, monPrefix, monAll, rt, monCase(rt), want)
	}
}

func TestMonetizationFundChanges_AreAlwaysTwoPerson_ExecutedOnceBySecondHolder(t *testing.T) {
	always := map[string]string{
		opMonRatesSet: permMonFundRates, opMonBandsSet: permMonFundRates, opMonBudgetSet: permMonFundBudget,
		opMonSettleDay: permMonFundSettle, opMonSettlePeriod: permMonFundSettle, opMonSettleCreator: permMonFundSettle,
		opMonEarningReverse: permMonFundReverse,
	}
	seen := 0
	for _, rt := range MonetizationRoutes {
		perm, ok := always[rt.operation]
		if !ok {
			if rt.twoPerson {
				t.Fatalf("%s is two-person but not expected to be", rt.operation)
			}
			continue
		}
		seen++
		if !rt.twoPerson || !rt.stepUp || rt.permission != perm {
			t.Fatalf("%s: twoPerson=%v stepUp=%v permission=%s", rt.operation, rt.twoPerson, rt.stepUp, rt.permission)
		}
		t.Run(rt.operation, func(t *testing.T) {
			rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
			rg.holders.n = 1
			requester, approver := uuid.NewString(), uuid.NewString()
			rg.perms.grant(requester, perm)
			rg.perms.grant(approver, perm)
			path, _ := fill(rt.path)
			query := ""
			if rt.operation == opMonSettleDay {
				query = "?day=2026-09-15"
			}
			body := `{"reason":"month end"}`
			if b := monCase(rt); strings.Contains(b, "reason") {
				body = b
			}
			w := rg.do(rt.method, monPrefix+path+query, body, requester, true)
			if w.Code != http.StatusAccepted {
				t.Fatalf("first call: %d %s", w.Code, w.Body.String())
			}
			a := decodeApproval(t, w)
			if a.App != "monetization" || a.Operation != rt.operation || a.RequiredPermission != perm || a.RequestedBy != requester {
				t.Fatalf("approval %+v", a)
			}
			if hits := rg.takeHits(); len(hits) != 0 {
				t.Fatalf("the first admin's call reached monetization: %+v", hits)
			}
			if audit := rg.takeAudit(); len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomePending {
				t.Fatalf("pending audit %+v", audit)
			}
			// Without step-up nothing is submitted.
			if w := rg.do(rt.method, monPrefix+path+query, body, requester, false); !hasCode(w, adminauth.CodeStepUpRequired) {
				t.Fatalf("without step-up: %d", w.Code)
			}
			rg.takeAudit()

			w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"checked"}`, approver, true)
			if w.Code != http.StatusOK {
				t.Fatalf("approve: %d %s", w.Code, w.Body.String())
			}
			hits := rg.takeHits()
			if len(hits) != 1 || hits[0].method != rt.method || hits[0].path != service.MonetizationAdminPrefix+path ||
				hits[0].verified.Actor != approver || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != perm {
				t.Fatalf("execution %+v", hits)
			}
			if query != "" && "?"+hits[0].query != query {
				t.Fatalf("stored query %q, want %q", hits[0].query, query)
			}
			if !strings.Contains(hits[0].body, `"reason"`) {
				t.Fatalf("stored body not replayed: %s", hits[0].body)
			}
			if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"again"}`, approver, true); !strings.Contains(w.Body.String(), `"replayed":true`) {
				t.Fatalf("second approve: %d %s", w.Code, w.Body.String())
			}
			if hits := rg.takeHits(); len(hits) != 0 {
				t.Fatal("executed twice")
			}
		})
	}
	if seen != len(always) {
		t.Fatalf("found %d of %d two-person routes", seen, len(always))
	}
}

func TestMonetizationRefund_ThresholdBoundary(t *testing.T) {
	const threshold = 500000
	for _, tc := range []struct {
		paise     string
		twoPerson bool
	}{
		{"499999", false},
		{"500000", true}, // AT the threshold: two-person
		{"500001", true},
	} {
		rg := newProductsRig(t, true, threshold)
		rg.holders.n = 1
		actor := uuid.NewString()
		rg.perms.grant(actor, permMonRefundIssue)
		txn := uuid.NewString()
		body := `{"transaction_id":"` + txn + `","amount_paise":` + tc.paise + `,"reason":"double charge"}`
		w := rg.do(http.MethodPost, monPrefix+"/refunds", body, actor, true)
		hits := rg.takeHits()
		audit := rg.takeAudit()
		if tc.twoPerson {
			if w.Code != http.StatusAccepted || len(hits) != 0 || len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomePending {
				t.Fatalf("%s: want pending, got %d %s hits=%d audit=%+v", tc.paise, w.Code, w.Body.String(), len(hits), audit)
			}
			a := decodeApproval(t, w)
			if a.TargetID != txn || !strings.Contains(a.Summary, "Refund monetization transaction") {
				t.Fatalf("%s: approval %+v", tc.paise, a)
			}
			continue
		}
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].path != service.MonetizationAdminPrefix+"/refunds" ||
			!strings.Contains(hits[0].body, `"amount_paise":`+tc.paise) || len(rg.store.rows) != 0 {
			t.Fatalf("%s: want direct refund, got %d %s hits=%+v", tc.paise, w.Code, w.Body.String(), hits)
		}
		if len(audit) != 1 || audit[0].Payload["refund_threshold_paise"] != int64(threshold) {
			t.Fatalf("%s: audit %+v", tc.paise, audit)
		}
		if w := rg.do(http.MethodPost, monPrefix+"/refunds", body, actor, false); !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s without step-up: %d", tc.paise, w.Code)
		}
	}
}

func TestMonetizationRefund_BadBodiesAreRefusedBeforeMonetization(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	actor := uuid.NewString()
	rg.perms.grant(actor, permMonRefundIssue)
	for _, body := range []string{
		``,
		`{"transaction_id":"` + uuid.NewString() + `","reason":"r"}`,
		`{"transaction_id":"` + uuid.NewString() + `","amount_paise":0,"reason":"r"}`,
		`{"transaction_id":"` + uuid.NewString() + `","amount_paise":12.5,"reason":"r"}`,
		`{"transaction_id":"nope","amount_paise":100,"reason":"r"}`,
	} {
		if w := rg.do(http.MethodPost, monPrefix+"/refunds", body, actor, true); w.Code != http.StatusBadRequest {
			t.Fatalf("%q: %d %s", body, w.Code, w.Body.String())
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("reached monetization: %+v", hits)
	}
}

func TestMonetizationNotLaunched_IsSurfacedAsAState(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	notLaunched := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"MONETIZATION_NOT_LAUNCHED","message":"Money actions are not available in this beta."}}`))
	}
	for _, rt := range MonetizationRoutes {
		rg.on(rt.method, service.MonetizationAdminPrefix+rt.path, notLaunched)
	}
	actor := uuid.NewString()
	rg.perms.grant(actor, monAll...)

	// A read: 200 with the state, audited as a failure with the upstream 503.
	w := rg.do(http.MethodGet, monPrefix+"/stats", "", actor, false)
	var env struct {
		Data NotLaunchedState `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil || env.Data.State != "not_launched" ||
		env.Data.Code != CodeMonetizationNotLaunched || env.Data.App != "monetization" {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != 503 || a[0].Payload["state"] != "not_launched" {
		t.Fatalf("read audit %+v", a)
	}

	// A write: 503 MONETIZATION_NOT_LAUNCHED with details.state, never a 2xx.
	path, _ := fill("/wallet/:userId/freeze")
	rg.on(http.MethodPost, service.MonetizationAdminPrefix+path, notLaunched)
	w = rg.do(http.MethodPost, monPrefix+path, `{"reason":"fraud"}`, actor, true)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeMonetizationNotLaunched) || !strings.Contains(w.Body.String(), `"state":"not_launched"`) {
		t.Fatalf("write: %d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].Payload["state"] != "not_launched" {
		t.Fatalf("write audit %+v", a)
	}

	// A two-person write run by the sole holder: the same write answer.
	w = rg.do(http.MethodPost, monPrefix+"/creator-fund/settle-period", `{"reason":"month end"}`, actor, true)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeMonetizationNotLaunched) || !strings.Contains(w.Body.String(), `"state":"not_launched"`) {
		t.Fatalf("sole-holder write: %d %s", w.Code, w.Body.String())
	}
	rg.takeAudit()

	// Maintenance is not the not-launched state: passed through as it is.
	rg.on(http.MethodGet, service.MonetizationAdminPrefix+"/disputes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"MAINTENANCE","message":"m"}}`))
	})
	if w := rg.do(http.MethodGet, monPrefix+"/disputes", "", actor, false); w.Code != http.StatusServiceUnavailable || !hasCode(w, "MAINTENANCE") {
		t.Fatalf("maintenance: %d %s", w.Code, w.Body.String())
	}
}

func TestMonetizationExecutor_RefusesAStoredPathOfAnotherRoute(t *testing.T) {
	if storedPathMatches("/creator-fund/earnings/:id/reverse", "/wallet/"+uuid.NewString()+"/freeze") ||
		storedPathMatches("/creator-fund/earnings/:id/reverse", "/creator-fund/earnings/../reverse") ||
		!storedPathMatches("/creator-fund/earnings/:id/reverse", "/creator-fund/earnings/"+uuid.NewString()+"/reverse") {
		t.Fatal("stored path check")
	}
}
