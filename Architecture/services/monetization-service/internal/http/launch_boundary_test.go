package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLaunchBoundaryBlocksEveryMutationBeforeHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil).WithWritesEnabled(false)
	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			r := gin.New()
			r.Use(h.launchBoundary())
			r.Handle(method, "/v1/monetization/payouts", func(c *gin.Context) {
				t.Fatal("financial handler executed while writes were disabled")
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/v1/monetization/payouts", nil))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "MONETIZATION_NOT_LAUNCHED") {
				t.Fatalf("unexpected body: %s", w.Body.String())
			}
		})
	}
}

func TestLaunchBoundaryUsesExactReadAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil).WithWritesEnabled(false)
	for _, path := range []string{
		"/v1/monetization/admin/fraud-reviews",
		"/v1/monetization/payout-statements/00000000-0000-0000-0000-000000000000",
		"/v1/monetization/creator-fund/earnings",
		"/v1/monetization/creator-ledger/extra",
	} {
		t.Run(path, func(t *testing.T) {
			r := gin.New()
			r.Use(h.launchBoundary())
			r.GET(path, func(c *gin.Context) { t.Fatal("unlaunched read handler executed") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLaunchBoundaryAllowsOnlyRecordedLedgerReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil).WithWritesEnabled(false)
	for path := range betaReadOnlyPaths {
		t.Run(path, func(t *testing.T) {
			r := gin.New()
			r.Use(h.launchBoundary())
			r.GET(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
