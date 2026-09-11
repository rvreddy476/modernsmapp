package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// The beta line, restated for Phase 3C: the RULES of payment are open, the
// ESTIMATES are open and labelled as such, and everything that moves money
// or exposes admin, tax or entitlement state is closed.

// serveWithBoundary registers `pattern` for `method` behind the boundary
// and serves `path` against it, so a rule is tested the way gin will
// match it: on the registered pattern, not the concrete URL.
func serveWithBoundary(t *testing.T, h *Handler, method, pattern, path string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(h.launchBoundary())
	r.Handle(method, pattern, handler)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestLaunchBoundaryBlocksEveryMutationBeforeHandler(t *testing.T) {
	h := New(nil).WithWritesEnabled(false)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := serveWithBoundary(t, h, method, "/v1/monetization/payouts", "/v1/monetization/payouts", func(c *gin.Context) {
				t.Fatal("financial handler executed while writes were disabled")
			})
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "MONETIZATION_NOT_LAUNCHED") {
				t.Fatalf("unexpected body: %s", w.Body.String())
			}
		})
	}
}

// Estimates open: a creator can see what the fund thinks they have earned
// and the period statements behind it. The route patterns are the ones
// RegisterRoutes uses, parameter and all.
func TestLaunchBoundaryOpensEstimates(t *testing.T) {
	h := New(nil).WithWritesEnabled(false)
	cases := []struct{ pattern, path string }{
		{"/v1/monetization/creator-fund/earnings", "/v1/monetization/creator-fund/earnings?days=30"},
		{"/v1/monetization/creator-fund/statements", "/v1/monetization/creator-fund/statements"},
		{"/v1/monetization/creator-fund/statements/:periodKey", "/v1/monetization/creator-fund/statements/2026-09"},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			w := serveWithBoundary(t, h, http.MethodGet, tc.pattern, tc.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			if w.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s — estimates must be readable in beta", w.Code, w.Body.String())
			}
		})
	}
}

// Closed: admin, tax, entitlements, anything under a path that is not
// registered, and every write — including writes on the open patterns.
func TestLaunchBoundaryClosesAdminTaxEntitlementsAndWrites(t *testing.T) {
	h := New(nil).WithWritesEnabled(false)
	cases := []struct{ method, pattern, path string }{
		{http.MethodGet, "/v1/monetization/admin/fraud-reviews", "/v1/monetization/admin/fraud-reviews"},
		{http.MethodGet, "/v1/monetization/admin/creator-fund/budgets", "/v1/monetization/admin/creator-fund/budgets"},
		{http.MethodGet, "/v1/monetization/admin/creator-fund/earnings/:id", "/v1/monetization/admin/creator-fund/earnings/00000000-0000-0000-0000-000000000000"},
		{http.MethodGet, "/v1/monetization/tax-profile", "/v1/monetization/tax-profile"},
		{http.MethodGet, "/v1/monetization/tds-summary/:year", "/v1/monetization/tds-summary/2026-27"},
		{http.MethodGet, "/v1/monetization/invoices", "/v1/monetization/invoices"},
		{http.MethodGet, "/v1/monetization/entitlements", "/v1/monetization/entitlements"},
		{http.MethodGet, "/v1/monetization/payout-methods", "/v1/monetization/payout-methods"},
		{http.MethodGet, "/v1/monetization/dashboard", "/v1/monetization/dashboard"},
		{http.MethodGet, "/v1/monetization/creator-ledger/extra", "/v1/monetization/creator-ledger/extra"},
		{http.MethodPost, "/v1/monetization/creator-fund/apply", "/v1/monetization/creator-fund/apply"},
		{http.MethodPost, "/v1/monetization/creator-fund/earnings", "/v1/monetization/creator-fund/earnings"},
		{http.MethodPut, "/v1/monetization/admin/creator-fund/rates", "/v1/monetization/admin/creator-fund/rates"},
		{http.MethodPost, "/v1/monetization/payouts", "/v1/monetization/payouts"},
		{http.MethodPost, "/v1/monetization/webhooks/payout", "/v1/monetization/webhooks/payout"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.pattern, func(t *testing.T) {
			w := serveWithBoundary(t, h, tc.method, tc.pattern, tc.path, func(c *gin.Context) { t.Fatal("unlaunched handler executed") })
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// A request that matches no registered route has no FullPath; it fails
// closed rather than reaching gin's 404 with the boundary bypassed.
func TestLaunchBoundaryFailsClosedOnUnregisteredPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil).WithWritesEnabled(false)
	r := gin.New()
	r.Use(h.launchBoundary())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/monetization/creator-fund/earnings/anything", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// The rule list itself, stated so that widening it is a deliberate edit
// of an assertion that says why the line is where it is.
func TestLaunchBoundaryRuleListIsTheBetaLine(t *testing.T) {
	open := []struct{ method, pattern string }{
		{http.MethodGet, "/v1/monetization/creator-ledger"},
		{http.MethodGet, "/v1/monetization/wallet"},
		{http.MethodGet, "/v1/monetization/transactions"},
		{http.MethodGet, "/v1/monetization/payouts"},
		{http.MethodGet, "/v1/monetization/creator-fund/rates"},
		{http.MethodGet, "/v1/monetization/creator-fund/quality-bands"},
		{http.MethodGet, "/v1/monetization/creator-fund/status"},
		{http.MethodGet, "/v1/monetization/creator-fund/earnings"},
		{http.MethodGet, "/v1/monetization/creator-fund/statements"},
		{http.MethodGet, "/v1/monetization/creator-fund/statements/:periodKey"},
	}
	for _, rule := range open {
		if !betaRuleAllows(rule.method, rule.pattern) {
			t.Errorf("%s %s must be open in beta", rule.method, rule.pattern)
		}
	}
	closed := []struct{ method, pattern string }{
		{http.MethodPost, "/v1/monetization/payouts"},
		{http.MethodPost, "/v1/monetization/creator-fund/apply"},
		{http.MethodGet, "/v1/monetization/tds-summary/:year"},
		{http.MethodGet, "/v1/monetization/entitlements"},
		{http.MethodGet, "/v1/monetization/admin/creator-fund/budgets"},
		{http.MethodGet, ""},
	}
	for _, rule := range closed {
		if betaRuleAllows(rule.method, rule.pattern) {
			t.Errorf("%s %q must be closed in beta", rule.method, rule.pattern)
		}
	}
}

// Every rule in the list actually lets a request through when served
// against its own pattern.
func TestLaunchBoundaryAllowsEveryListedRead(t *testing.T) {
	h := New(nil).WithWritesEnabled(false)
	for _, rule := range betaReadOnlyRules {
		path := strings.ReplaceAll(rule.pattern, ":periodKey", "2026-09")
		t.Run(rule.method+" "+rule.pattern, func(t *testing.T) {
			w := serveWithBoundary(t, h, rule.method, rule.pattern, path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			if w.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
