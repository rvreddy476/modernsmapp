package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/runmode"
	"github.com/gin-gonic/gin"
)

// Reviewer correction B (12 Sep 2026): enabling MONETIZATION_WRITES_ENABLED
// for the January remediation would start the accrual, settlement and
// eligibility workers, any of which could claim a row the operator is
// about to correct. Maintenance mode is the mutual exclusion: no worker,
// no Kafka client, admin routes only — and (correction C) those admin
// routes require the internal service key on top of the scope header,
// because the scope header is an operator identity claim, not
// authentication.
func TestMaintenanceModeStartsNoWorkersAndClosesNonAdminWrites(t *testing.T) {
	// 1. The process side: nothing in the background, and the two
	//    contradictory configurations refuse to boot.
	mode, err := runmode.Resolve(runmode.Config{Maintenance: true, InternalKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if mode.StartWorkers || mode.StartKafka || mode.RequireRedis {
		t.Fatalf("maintenance mode would start something: %+v", mode)
	}
	if !mode.AdminKeyRequired {
		t.Fatalf("maintenance mode must require the admin key: %+v", mode)
	}
	if _, err := runmode.Resolve(runmode.Config{Maintenance: true, WritesEnabled: true, PayoutsEnabled: true, InternalKey: "k"}); err == nil {
		t.Fatal("maintenance mode booted with payouts enabled")
	}
	if _, err := runmode.Resolve(runmode.Config{Maintenance: true}); err == nil {
		t.Fatal("maintenance mode booted without INTERNAL_SERVICE_KEY")
	}
	if !strings.Contains(mode.BootLine(), "maintenance=true") || !strings.Contains(mode.BootLine(), "workers=false") {
		t.Fatalf("boot line does not say so: %s", mode.BootLine())
	}

	// 2. The HTTP side. writesEnabled is deliberately true here too:
	//    maintenance wins.
	const key = "test-internal-key"
	h := New(nil).WithInternalKey(key).WithWritesEnabled(true).WithMaintenance(true)

	// Every non-admin financial write answers 503 MAINTENANCE before the
	// handler runs.
	writes := []struct{ method, pattern, path string }{
		{http.MethodPost, "/v1/monetization/payouts", "/v1/monetization/payouts"},
		{http.MethodPost, "/v1/monetization/creator-fund/apply", "/v1/monetization/creator-fund/apply"},
		{http.MethodPost, "/v1/monetization/tips", "/v1/monetization/tips"},
		{http.MethodPost, "/v1/monetization/subscribe/:creatorId", "/v1/monetization/subscribe/00000000-0000-0000-0000-000000000001"},
		{http.MethodPost, "/v1/monetization/internal/charge-and-credit", "/v1/monetization/internal/charge-and-credit"},
		{http.MethodPost, "/v1/monetization/webhooks/payout", "/v1/monetization/webhooks/payout"},
		{http.MethodPost, "/v1/monetization/refunds", "/v1/monetization/refunds"},
		{http.MethodPatch, "/v1/monetization/disputes/:id", "/v1/monetization/disputes/1"},
		{http.MethodPost, "/v1/monetization/payout-methods", "/v1/monetization/payout-methods"},
		{http.MethodGet, "/v1/monetization/tds-summary/:year", "/v1/monetization/tds-summary/2026-27"},
	}
	for _, tc := range writes {
		t.Run("closed "+tc.method+" "+tc.pattern, func(t *testing.T) {
			w := serveWithBoundary(t, h, tc.method, tc.pattern, tc.path, func(c *gin.Context) { t.Fatal("non-admin write executed in maintenance mode") })
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"MAINTENANCE"`) {
				t.Fatalf("status=%d body=%s, want 503 MAINTENANCE", w.Code, w.Body.String())
			}
		})
	}

	// The beta reads stay open (a creator can still see the estimate).
	w := serveWithBoundary(t, h, http.MethodGet, "/v1/monetization/creator-fund/earnings", "/v1/monetization/creator-fund/earnings", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if w.Code != http.StatusNoContent {
		t.Fatalf("estimate read in maintenance: status=%d", w.Code)
	}

	// An admin route without the internal key is refused (401) whatever
	// the scope header says: a container that merely reaches the port
	// cannot call a correction.
	adminPattern := "/v1/monetization/admin/creator-fund/earnings/:id/reverse"
	adminPath := "/v1/monetization/admin/creator-fund/earnings/00000000-0000-0000-0000-000000000001/reverse"
	serve := func(hdr map[string]string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(h.launchBoundary())
		r.Handle(http.MethodPost, adminPattern, handler)
		req := httptest.NewRequest(http.MethodPost, adminPath, strings.NewReader(`{"reason":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	executed := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	notExecuted := func(c *gin.Context) { t.Fatal("admin handler executed without the internal key") }

	if w := serve(map[string]string{"X-Scopes": "admin", "X-User-Id": "7cd6ea3a-9c80-4f20-806f-5d08de0f914b"}, notExecuted); w.Code != http.StatusUnauthorized {
		t.Fatalf("admin route without key: status=%d body=%s, want 401", w.Code, w.Body.String())
	}
	if w := serve(map[string]string{"X-Scopes": "admin", "X-Internal-Service-Key": "wrong"}, notExecuted); w.Code != http.StatusUnauthorized {
		t.Fatalf("admin route with wrong key: status=%d, want 401", w.Code)
	}
	// With the key the boundary lets it through to the handler...
	if w := serve(map[string]string{"X-Internal-Service-Key": key, "X-Scopes": "admin"}, executed); w.Code != http.StatusNoContent {
		t.Fatalf("admin route with key: status=%d body=%s", w.Code, w.Body.String())
	}
	// ...and the real handler still demands the scope: key alone is 403.
	// (getAdminID runs before any service call, so a nil service is safe.)
	if w := serve(map[string]string{"X-Internal-Service-Key": key}, h.ReverseCreatorFundEarning); w.Code != http.StatusForbidden {
		t.Fatalf("admin route with key but no scope: status=%d body=%s, want 403", w.Code, w.Body.String())
	}

	// A maintenance handler with no key configured fails closed on admin
	// routes rather than opening them (runmode refuses this at boot; the
	// handler does not rely on that).
	noKey := New(nil).WithMaintenance(true)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(noKey.launchBoundary())
	r.Handle(http.MethodPost, adminPattern, notExecuted)
	req := httptest.NewRequest(http.MethodPost, adminPath, nil)
	req.Header.Set("X-Scopes", "admin")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "MAINTENANCE_KEY_UNSET") {
		t.Fatalf("admin route with no key configured: status=%d body=%s", w.Code, w.Body.String())
	}
}

// Outside maintenance mode nothing changes: the existing beta line and the
// writes-on behaviour are what they were.
func TestMaintenanceOffLeavesTheBoundaryAlone(t *testing.T) {
	h := New(nil).WithWritesEnabled(true).WithMaintenance(false)
	w := serveWithBoundary(t, h, http.MethodPost, "/v1/monetization/payouts", "/v1/monetization/payouts", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if w.Code != http.StatusNoContent {
		t.Fatalf("writes on, maintenance off: status=%d", w.Code)
	}
	if isAdminPattern("") || isAdminPattern("/v1/monetization/payouts") || !isAdminPattern("/v1/monetization/admin/creator-fund/budgets") {
		t.Fatal("isAdminPattern")
	}
}
