package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/shared/health"
	"github.com/gin-gonic/gin"
)

const probeTestKey = "probe-test-internal-key"

func probeEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	checker := health.New("admin-service")
	checker.Register("postgres", func(context.Context) error { return errors.New("connection refused") })
	r := gin.New()
	installProbesThenInternalKey(r, checker, probeTestKey)
	// Stands in for every route main registers after the gate.
	r.GET("/v1/after-the-gate", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func probeGet(r *gin.Engine, path, key string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// The kubelet calls /livez with no headers. It must answer 200 without the
// internal key and while a dependency is down.
func TestLivezIsOpenWithoutTheInternalKeyWhileADependencyIsDown(t *testing.T) {
	r := probeEngine(t)
	for _, path := range []string{"/livez", "/healthz"} {
		if got := probeGet(r, path, ""); got != http.StatusOK {
			t.Fatalf("GET %s without key = %d, want 200", path, got)
		}
	}
}

// /readyz must reach the dependency checks without the key: 503 for the
// outage, never 401 from the gate.
func TestReadyzIsOpenWithoutTheInternalKey(t *testing.T) {
	if got := probeGet(probeEngine(t), "/readyz", ""); got != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz without key = %d, want 503 from the failing dependency", got)
	}
}

// Opening the probes must not open anything else.
func TestRoutesAfterTheProbesStillNeedTheInternalKey(t *testing.T) {
	r := probeEngine(t)
	if got := probeGet(r, "/v1/after-the-gate", ""); got != http.StatusUnauthorized {
		t.Fatalf("no key: %d, want 401", got)
	}
	if got := probeGet(r, "/v1/after-the-gate", "wrong"); got != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d, want 401", got)
	}
	if got := probeGet(r, "/v1/after-the-gate", probeTestKey); got != http.StatusOK {
		t.Fatalf("right key: %d, want 200", got)
	}
}
