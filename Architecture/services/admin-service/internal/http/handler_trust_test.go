package http

import (
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
	}
	return ""
}

func TestTrustRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(TrustRoutes) != 14 {
		t.Fatalf("TrustRoutes has %d entries, want 14", len(TrustRoutes))
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
		stepUpCase(t, rg, trustPrefix, trustAll, rt, trustCase(rt), rt.operation == "trust.verification_requests.list")
	}
}
