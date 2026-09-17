package http

import (
	"net/http"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

func qaCase(rt productRoute) string {
	switch rt.operation {
	case "qa.question.merge":
		return `{"merge_into_id":"` + uuid.NewString() + `","reason":"same question"}`
	case "qa.question.duplicate":
		return `{"duplicate_of_id":"` + uuid.NewString() + `","reason":"asked before"}`
	}
	if rt.method == http.MethodGet {
		return ""
	}
	return `{"reason":"off topic"}`
}

func TestQARoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(QARoutes) != 12 {
		t.Fatalf("QARoutes has %d entries, want 12", len(QARoutes))
	}
	for _, rt := range QARoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, qaPrefix, service.QAAdminPrefix, "qa", "qa", qaAll, rt, qaCase(rt))
		})
	}
}

func TestQARoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range QARoutes {
		if rt.stepUp != (rt.operation == "qa.question.merge") {
			t.Fatalf("%s declares step-up=%v", rt.operation, rt.stepUp)
		}
		stepUpCase(t, rg, qaPrefix, qaAll, rt, qaCase(rt), rt.stepUp)
	}
}

// Every Q&A write needs a reason; a write without one is refused before
// qa-service is called and audited as denied. Reads need none.
func TestQAWrites_ReasonRequiredBeforeTheCall(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, qaAll...)
	long := make([]byte, maxReasonLength+1)
	for i := range long {
		long[i] = 'x'
	}
	for _, rt := range QARoutes {
		path, _ := fill(rt.path)
		if rt.method == http.MethodGet {
			if w := rg.do(rt.method, qaPrefix+path, "", actor, true); w.Code != http.StatusOK {
				t.Fatalf("%s: %d", rt.operation, w.Code)
			}
			rg.takeHits()
			rg.takeAudit()
			continue
		}
		for _, body := range []string{``, `{}`, `{"reason":""}`, `{"reason":"   "}`, `{"reason":"` + string(long) + `"}`, `not json`} {
			w := rg.do(rt.method, qaPrefix+path, body, actor, true)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s %q: %d %s", rt.operation, body, w.Code, w.Body.String())
			}
			if body == `{}` && !hasCode(w, CodeReasonRequired) {
				t.Fatalf("%s: code %s, want %s", rt.operation, w.Body.String(), CodeReasonRequired)
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied {
				t.Fatalf("%s %q audit %+v", rt.operation, body, a)
			}
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s: a write without a reason reached qa-service: %+v", rt.operation, hits)
		}
		w := rg.do(rt.method, qaPrefix+path, qaCase(rt), actor, true)
		hits := rg.takeHits()
		a := rg.takeAudit()
		if w.Code != http.StatusOK || len(hits) != 1 || len(a) != 1 || a[0].Reason == "" {
			t.Fatalf("%s with a reason: %d hits=%d audit=%+v", rt.operation, w.Code, len(hits), a)
		}
	}
}
