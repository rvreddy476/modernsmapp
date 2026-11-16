package buildinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/shared/health"
	"github.com/gin-gonic/gin"
)

// The Helm chart's liveness probe calls GET /livez with no credentials
// (charts/atpost-service/values.yaml). This service registers its own probe
// routes instead of the shared ones, and it used to leave /livez out: the
// kubelet got 404 and restarted the pod forever.
func TestLivezIsServedWithoutCredentialsWhileADependencyIsDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := health.New("monetization-service")
	checker.Register("postgres", func(context.Context) error { return errors.New("connection refused") })
	r := gin.New()
	RegisterHealthRoutes(r, checker)

	for _, path := range []string{"/livez", "/healthz"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d with a dependency down, want 200: liveness must not check dependencies", path, w.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("GET %s body is not JSON: %v", path, err)
		}
		if body["status"] != "alive" || body["build_sha"] != SHA {
			t.Fatalf("GET %s body = %v, want status alive and build_sha %q", path, body, SHA)
		}
	}

	// The same outage must show on readiness, or the loop above proves nothing
	// about the difference between the two probes.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d with a dependency down, want 503", w.Code)
	}
}
