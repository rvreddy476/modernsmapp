package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/shared/health"
	"github.com/gin-gonic/gin"
)

// downChecker is the shared probe set with a dependency that is down, so a
// 200 on /livez proves liveness runs no dependency check.
func downChecker(name string) *health.Checker {
	c := health.New(name)
	c.Register("postgres", func(context.Context) error { return errors.New("connection refused") })
	return c
}

// assertProbesOpen sends each probe with no credentials at all, as the kubelet
// does (charts/atpost-service/values.yaml probes GET /livez and GET /readyz).
func assertProbesOpen(t *testing.T, r *gin.Engine) {
	t.Helper()
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/livez", http.StatusOK},
		{"/healthz", http.StatusOK},
		// 503 is the dependency check answering; 401 or 404 would be a gate.
		{"/readyz", http.StatusServiceUnavailable},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.want {
			t.Fatalf("GET %s without credentials = %d, want %d", tc.path, w.Code, tc.want)
		}
	}
}

// main registers the shared probes and then the handler. The handler installs
// the internal key and, with communities off, the kill switch as ENGINE-wide
// middleware; gin fixes a route's chain at registration, so the probes
// registered first stay open. Both gates are on here.
func TestLivezIsOpenWithoutKeyWhileADependencyIsDown(t *testing.T) {
	r := gin.New()
	downChecker("channel-service").RegisterRoutes(r)
	New(nil).WithInternalKey("probe-test-internal-key").WithCommunitiesEnabled(false).RegisterRoutes(r)

	assertProbesOpen(t, r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/broadcast-channels/my", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/broadcast-channels/my without key = %d, want 401: the gate is not installed, so this test proves nothing", w.Code)
	}
}
