package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/google/uuid"
)

const commercePrefix = "/v1/admin/commerce"

func commerceCase(rt productRoute) string {
	switch {
	case rt.operation == opCODSettle:
		return `{"reason":"batch paid"}`
	case rt.method == http.MethodGet:
		return ""
	}
	return `{"reason":"checked","notes":"n"}`
}

func TestCommerceRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(CommerceRoutes) != 23 {
		t.Fatalf("CommerceRoutes has %d entries, want 23", len(CommerceRoutes))
	}
	for _, rt := range CommerceRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, commercePrefix, service.CommerceAdminPrefix, "commerce", "commerce", commerceAll, rt, commerceCase(rt))
		})
	}
	for _, rt := range CatalogueRoutes {
		rt.permission, rt.path, rt.upstream = permCatalogueEdit, "/catalogue"+rt.path, rt.path
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			body := ""
			if rt.method != http.MethodGet {
				body = "{}"
			}
			routeTableCase(t, rg, commercePrefix, service.CommerceAdminPrefix, "commerce", "commerce", commerceAll, rt, body)
		})
	}
}

func TestCommerceRoutes_StepUpAndTwoPerson(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range CommerceRoutes {
		stepUpCase(t, rg, commercePrefix, commerceAll, rt, commerceCase(rt), rt.stepUp)
	}
	want := map[string]bool{opSellerSuspend: true, opSellerUnsuspend: true, opSellerKYCVerify: true, opPayoutsPending: true, opCODSettle: true}
	for _, rt := range CommerceRoutes {
		if rt.stepUp != want[rt.operation] {
			t.Fatalf("%s step-up = %v, want %v", rt.operation, rt.stepUp, want[rt.operation])
		}
		if rt.twoPerson != (rt.operation == opCODSettle) {
			t.Fatalf("%s two-person = %v", rt.operation, rt.twoPerson)
		}
	}
	publish := false
	for _, rt := range CatalogueRoutes {
		if rt.stepUp != (rt.path == "/attribute-schema/publish") {
			t.Fatalf("catalogue %s %s step-up = %v", rt.method, rt.path, rt.stepUp)
		}
		publish = publish || rt.stepUp
	}
	if !publish {
		t.Fatal("attribute-schema publish is not step-up")
	}
}

// The console (apps/admin/src/hooks/useAdminCommerce.ts) reads these paths and
// shapes; switching commerce to the token family must not change them.
func TestCommerceConsolePathsAndShapesAreUnchanged(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/sellers/queue"}, {http.MethodGet, "/products/queue"}, {http.MethodGet, "/payouts/pending"},
		{http.MethodPost, "/sellers/:sellerId/approve"}, {http.MethodPost, "/sellers/:sellerId/reject"},
		{http.MethodPost, "/sellers/:sellerId/suspend"}, {http.MethodPost, "/sellers/:sellerId/kyc/verify"},
		{http.MethodPost, "/products/:productId/approve"}, {http.MethodPost, "/products/:productId/reject"},
		{http.MethodPost, "/sellers/:sellerId/unsuspend"}, {http.MethodPost, "/sellers/:sellerId/request-changes"},
		{http.MethodPost, "/products/:productId/request-changes"}, {http.MethodGet, "/sellers/:sellerId"},
		{http.MethodPost, "/cod-remittances/:remittanceId/settle"},
	} {
		if _, ok := rg.gate.Requirement(c.method, commercePrefix+c.path); !ok {
			t.Fatalf("console route %s %s is gone", c.method, c.path)
		}
	}

	actor := uuid.NewString()
	rg.perms.grant(actor, commerceAll...)
	seller := "s-" + uuid.NewString()[:8]

	// Queues keep their data envelope and the default paging they always sent.
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/queue", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"s1"}]}`))
	})
	w := rg.do(http.MethodGet, commercePrefix+"/sellers/queue", "", actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || w.Body.String() != `{"data":[{"id":"s1"}]}` || hits[0].query != "limit=20&offset=0" {
		t.Fatalf("queue: %d %s query=%q", w.Code, w.Body.String(), hits[0].query)
	}
	w = rg.do(http.MethodGet, commercePrefix+"/payouts/pending?limit=7&x=1", "", actor, true)
	if hits = rg.takeHits(); hits[0].query != "limit=7" {
		t.Fatalf("payouts query %q", hits[0].query)
	}

	// Approve/reject/suspend answered with a status and no body; they still do,
	// and commerce receives only the fields it reads.
	for _, c := range []struct {
		path, body, sent string
		statusOnly       bool
	}{
		{"/sellers/" + seller + "/approve", `{"notes":"ok","reason":"x","extra":1}`, `{"notes":"ok"}`, true},
		{"/sellers/" + seller + "/reject", `{"reason":"fake docs","notes":"n","changes":"c"}`, `{"reason":"fake docs","notes":"n"}`, true},
		{"/sellers/" + seller + "/suspend", `{"reason":"fraud"}`, `{"reason":"fraud"}`, true},
		{"/sellers/" + seller + "/request-changes", `{"changes":"gstin","reason":"r"}`, `{"changes":"gstin"}`, true},
		{"/products/p-1/approve", `{"notes":"fine"}`, `{"notes":"fine"}`, true},
		{"/products/p-1/reject", `{"reason":"counterfeit","notes":"n"}`, `{"reason":"counterfeit"}`, true},
		{"/sellers/" + seller + "/unsuspend", `{"reason":"cleared"}`, `{"reason":"cleared"}`, false},
		{"/products/p-1/request-changes", `{"changes":"photos"}`, `{"changes":"photos"}`, false},
		{"/sellers/" + seller + "/kyc/verify", `{"reason":"docs in"}`, `{}`, false},
	} {
		w := rg.do(http.MethodPost, commercePrefix+c.path, c.body, actor, true)
		hits := rg.takeHits()
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].body != c.sent || hits[0].path != service.CommerceAdminPrefix+c.path {
			t.Fatalf("%s: %d hits=%+v", c.path, w.Code, hits)
		}
		if c.statusOnly != (w.Body.Len() == 0) {
			t.Fatalf("%s: body %q, status-only=%v", c.path, w.Body.String(), c.statusOnly)
		}
	}
	// Unsafe ids never reach commerce.
	for _, id := range []string{"..", "a%2Fb", "a?b"} {
		if w := rg.do(http.MethodPost, commercePrefix+"/sellers/"+id+"/approve", `{}`, actor, true); w.Code == http.StatusOK {
			t.Fatalf("id %q: %d", id, w.Code)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("unsafe ids reached commerce: %+v", hits)
	}
	if strings.Contains(w.Body.String(), "internal") {
		t.Fatal("leaked an internal path")
	}
}
