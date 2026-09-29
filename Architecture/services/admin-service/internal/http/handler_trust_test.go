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

const trustPrefix = "/v1/admin/trust"

func trustCase(rt productRoute) string {
	switch rt.operation {
	case opTrustAppealDecide:
		return `{"status":"upheld","note":"policy applies"}`
	case opTrustGrievanceUpdate:
		return `{"status":"acknowledged"}`
	case "trust.report.update":
		return `{"status":"reviewing"}`
	case opTrustStrikeIssue:
		return `{"user_id":"` + nobody + `","reason":"repeat spam after a warning","severity":"strike","idempotency_key":"click-1"}`
	case opTrustStrikeVoid:
		return `{"strike_id":"` + nobody + `","reason":"issued against the wrong account"}`
	case opTrustCopyrightCaseCreate:
		return `{"subject_post_id":"` + nobody + `","subject_author_id":"` + nobody + `","reason_code":"removal_upheld"}`
	case opTrustCopyrightCasePlace:
		return `{"reason_code":"reinstated_on_review"}`
	case opTrustCopyrightCaseRelease:
		return `{"reason_code":"claim_withdrawn"}`
	}
	return ""
}

func TestTrustRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(TrustRoutes) != 20 {
		t.Fatalf("TrustRoutes has %d entries, want 20", len(TrustRoutes))
	}
	for _, rt := range TrustRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, trustPrefix, service.TrustSafetyAdminPrefix, "trust_safety", "trust_safety", trustAll, rt, trustCase(rt))
		})
	}
}

// A read trust-safety admits under several permissions is scoped to the one
// the admin actually holds, never to one they lack.
func TestTrustAlternativeReads_ScopeToTheHeldPermission(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range TrustRoutes {
		for _, alt := range rt.alternatives {
			actor := uuid.NewString()
			rg.perms.grant(actor, alt)
			path, _ := fill(rt.path)
			w := rg.do(rt.method, trustPrefix+path, "", actor, false)
			hits := rg.takeHits()
			if w.Code != http.StatusOK || len(hits) != 1 || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != alt {
				t.Fatalf("%s as %s: %d hits=%+v", rt.operation, alt, w.Code, hits)
			}
		}
	}
}

func TestTrustOutcomeGates_DecidedBeforeTheProxyCall(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, trustAll...)
	for _, c := range []struct {
		path, body string
		stepUp     bool
		forwarded  string
	}{
		{"/appeals/", `{"status":"overturned","note":"mistake"}`, true, `"status":"overturned"`},
		{"/appeals/", `{"status":" OVERTURNED "}`, true, `"status":"overturned"`},
		{"/appeals/", `{"status":"upheld"}`, false, `"status":"upheld"`},
		{"/appeals/", `{"status":"under_review"}`, false, `"status":"under_review"`},
		{"/grievances/", `{"status":"resolved","resolution_notes":"refunded"}`, true, `"status":"resolved"`},
		{"/grievances/", `{"status":"Rejected"}`, true, `"status":"rejected"`},
		{"/grievances/", `{"status":"acknowledged"}`, false, `"status":"acknowledged"`},
		{"/grievances/", `{"assigned_to":"` + nobody + `"}`, false, `"assigned_to"`},
	} {
		path := trustPrefix + c.path + uuid.NewString()
		// Without a step-up: refused before trust-safety is called when the
		// outcome needs one.
		w := rg.do(http.MethodPatch, path, c.body, actor, false)
		hits := rg.takeHits()
		audit := rg.takeAudit()
		if c.stepUp {
			if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) || len(hits) != 0 {
				t.Fatalf("%s %s without step-up: %d hits=%d", c.path, c.body, w.Code, len(hits))
			}
			if len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomeDenied {
				t.Fatalf("%s %s audit %+v", c.path, c.body, audit)
			}
		} else if w.Code != http.StatusOK || len(hits) != 1 {
			t.Fatalf("%s %s needs no step-up: %d %s", c.path, c.body, w.Code, w.Body.String())
		}
		// With a step-up it goes through, carrying the normalised outcome.
		w = rg.do(http.MethodPatch, path, c.body, actor, true)
		hits = rg.takeHits()
		if w.Code != http.StatusOK || len(hits) != 1 || !strings.Contains(hits[0].body, c.forwarded) {
			t.Fatalf("%s %s with step-up: %d hits=%+v", c.path, c.body, w.Code, hits)
		}
		rg.takeAudit()
	}
	// Unknown outcomes are refused, and never reach trust-safety.
	for _, c := range []struct{ path, body string }{
		{"/appeals/", `{"status":"expired"}`}, {"/appeals/", `{}`}, {"/grievances/", `{"status":"closed"}`},
	} {
		if w := rg.do(http.MethodPatch, trustPrefix+c.path+uuid.NewString(), c.body, actor, true); w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: %d", c.path, c.body, w.Code)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("invalid outcomes reached trust-safety: %+v", hits)
	}
}

func TestTrustRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range TrustRoutes {
		wantStepUp := rt.operation == "trust.verification_requests.list" || rt.operation == opTrustStrikeIssue || rt.operation == opTrustStrikeVoid ||
			rt.operation == opTrustCopyrightCaseCreate || rt.operation == opTrustCopyrightCasePlace || rt.operation == opTrustCopyrightCaseRelease
		stepUpCase(t, rg, trustPrefix, trustAll, rt, trustCase(rt), wantStepUp)
	}
}

const strikeUser = "44444444-4444-4444-8444-444444444444"

// Issuing a strike forwards the normalised body (severity lower-cased, the
// console's idempotency_key intact, empty optionals dropped), targets the
// struck user in the audit row, and passes trust-safety's 201 / 200 through.
func TestTrustStrikeIssue_BodyForwardedAndAudited(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permTrustStrikesManage)
	contentID := uuid.NewString()
	body := `{"user_id":"` + strikeUser + `","reason":"  repeat spam after a warning ","severity":" Severe_Strike ",` +
		`"idempotency_key":"click-42","content_type":"post","content_id":"` + contentID + `","case_id":"","strike_group":""}`
	created := true
	rg.on(http.MethodPost, service.TrustSafetyAdminPrefix+"/strikes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if created {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"` + nobody + `","severity":"severe_strike"}}`))
	})

	w := rg.do(http.MethodPost, trustPrefix+"/strikes", body, actor, true)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"severity":"severe_strike"`) {
		t.Fatalf("issue: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != service.TrustSafetyAdminPrefix+"/strikes" || hits[0].verified.Scope[0] != permTrustStrikesManage {
		t.Fatalf("hits %+v", hits)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(hits[0].body), &sent); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"user_id": strikeUser, "reason": "repeat spam after a warning", "severity": "severe_strike",
		"idempotency_key": "click-42", "content_type": "post", "content_id": contentID,
	} {
		if sent[k] != want {
			t.Fatalf("forwarded %s = %v, want %v (body %s)", k, sent[k], want, hits[0].body)
		}
	}
	for _, absent := range []string{"case_id", "strike_group"} {
		if _, ok := sent[absent]; ok {
			t.Fatalf("empty %s was forwarded: %s", absent, hits[0].body)
		}
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Operation != opTrustStrikeIssue || a[0].TargetType != "user" || a[0].TargetID != strikeUser ||
		a[0].Reason != "repeat spam after a warning" || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].StatusCode != http.StatusCreated {
		t.Fatalf("audit %+v", a)
	}
	if a[0].Payload["severity"] != "severe_strike" || a[0].Payload["idempotency_key"] != "click-42" || a[0].Payload["content_id"] != contentID {
		t.Fatalf("audit payload %+v", a[0].Payload)
	}

	// A replay of the same key: trust-safety answers 200, and so does the console.
	created = false
	if w := rg.do(http.MethodPost, trustPrefix+"/strikes", body, actor, true); w.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	rg.takeHits()
	rg.takeAudit()
}

// A malformed issue is refused before the step-up check and never reaches
// trust-safety; the refusal is audited as denied.
func TestTrustStrikeIssue_RefusedBeforeTheProxyCall(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permTrustStrikesManage)
	ok := func(over string) string {
		return `{"user_id":"` + strikeUser + `","reason":"repeat spam","severity":"strike","idempotency_key":"k"` + over + `}`
	}
	for _, c := range []struct{ name, body, code string }{
		{"not json", `{`, CodeInvalidBody},
		{"no user", `{"reason":"x","severity":"strike","idempotency_key":"k"}`, CodeInvalidID},
		{"bad user", `{"user_id":"nope","reason":"x","severity":"strike","idempotency_key":"k"}`, CodeInvalidID},
		{"no reason", `{"user_id":"` + strikeUser + `","reason":"  ","severity":"strike","idempotency_key":"k"}`, CodeInvalidAction},
		{"bad severity", `{"user_id":"` + strikeUser + `","reason":"x","severity":"ban","idempotency_key":"k"}`, CodeInvalidAction},
		{"no key", `{"user_id":"` + strikeUser + `","reason":"x","severity":"strike"}`, CodeIdempotencyKeyRequired},
		{"long key", `{"user_id":"` + strikeUser + `","reason":"x","severity":"strike","idempotency_key":"` + strings.Repeat("k", 201) + `"}`, CodeIdempotencyKeyRequired},
		{"bad content id", ok(`,"content_id":"post-1"`), CodeInvalidID},
		{"bad case id", ok(`,"case_id":"case-1"`), CodeInvalidID},
	} {
		// Without a step-up: the body is judged first, so the answer is the
		// body's refusal, not STEP_UP_REQUIRED.
		w := rg.do(http.MethodPost, trustPrefix+"/strikes", c.body, actor, false)
		if w.Code != http.StatusBadRequest || !hasCode(w, c.code) {
			t.Fatalf("%s: %d %s, want 400 %s", c.name, w.Code, w.Body.String(), c.code)
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s reached trust-safety: %+v", c.name, hits)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied {
			t.Fatalf("%s audit %+v", c.name, a)
		}
	}
}

// Voiding targets the user in the path, records the strike id and reason, and
// forwards the body; trust-safety's 404 (not that user's strike) and 410 (the
// retired route) reach the console unchanged and are audited as failures.
func TestTrustStrikeVoid_ForwardedAndUpstreamStatusesPassThrough(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permTrustStrikesManage)
	strikeID := uuid.NewString()
	path := trustPrefix + "/strikes/" + strikeUser + "/void"
	upstream := service.TrustSafetyAdminPrefix + "/strikes/" + strikeUser + "/void"
	body := `{"strike_id":"` + strikeID + `","reason":" issued against the wrong account "}`

	rg.on(http.MethodPost, upstream, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"strike":{"id":"` + strikeID + `","voided_at":"2026-09-29T00:00:00Z"},"changed":true}}`))
	})
	w := rg.do(http.MethodPost, path, body, actor, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"changed":true`) {
		t.Fatalf("void: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != upstream || hits[0].verified.Scope[0] != permTrustStrikesManage ||
		!strings.Contains(hits[0].body, `"strike_id":"`+strikeID+`"`) || !strings.Contains(hits[0].body, `"reason":"issued against the wrong account"`) {
		t.Fatalf("hits %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Operation != opTrustStrikeVoid || a[0].TargetType != "user" || a[0].TargetID != strikeUser ||
		a[0].Reason != "issued against the wrong account" || a[0].Payload["strike_id"] != strikeID || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}

	for code, status := range map[string]int{"NOT_FOUND": http.StatusNotFound, "STRIKE_ROUTE_RETIRED": http.StatusGone} {
		rg.on(http.MethodPost, upstream, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"from trust-safety"}}`))
		})
		w := rg.do(http.MethodPost, path, body, actor, true)
		if w.Code != status || !hasCode(w, code) {
			t.Fatalf("%s: console answered %d %s", code, w.Code, w.Body.String())
		}
		rg.takeHits()
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != status {
			t.Fatalf("%s: audit %+v", code, a)
		}
	}

	// Malformed void bodies are refused before the call.
	for _, c := range []struct{ body, code string }{
		{`{"reason":"x"}`, CodeInvalidID},
		{`{"strike_id":"` + strikeID + `"}`, CodeInvalidAction},
		{`nope`, CodeInvalidBody},
	} {
		if w := rg.do(http.MethodPost, path, c.body, actor, true); w.Code != http.StatusBadRequest || !hasCode(w, c.code) {
			t.Fatalf("%s: %d %s, want 400 %s", c.body, w.Code, w.Body.String(), c.code)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("refused voids reached trust-safety: %+v", hits)
	}
	rg.takeAudit()
}
