package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-shared/health"
	"github.com/gin-gonic/gin"
)

const routerTestInternalKey = "router-test-internal-key"

// newTestRouter is the engine main.go serves, with the internal-key gate on
// (as in every deployed environment) and the real JWT middleware.
func newTestRouter(t *testing.T, postgres health.CheckFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	probes := health.New("profile-service", nil)
	probes.Register("postgres", postgres)
	h := New(&stubProfileService{}, nil).WithInternalKey(routerTestInternalKey)
	r, err := NewRouter(h, RouterConfig{
		Probes: probes,
		Auth:   AuthMiddleware("router-test-jwt-secret"),
		CSRF:   RequireCSRFMiddleware(),
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func probe(r http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func healthy(context.Context) error { return nil }
func down(context.Context) error    { return errors.New("connection refused") }

// The kubelet sends no headers: the probes must sit outside the internal-key
// gate RegisterRoutes installs engine-wide.
func TestProbesAnswer200WithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy)
	for _, path := range []string{"/livez", "/readyz"} {
		if w := probe(r, path); w.Code != http.StatusOK {
			t.Fatalf("GET %s with no credentials = %d, want 200", path, w.Code)
		}
	}
}

func TestReadyzIs503AndLivezStays200WhenPostgresIsDown(t *testing.T) {
	r := newTestRouter(t, down)
	if w := probe(r, "/readyz"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d, want 503", w.Code)
	}
	if w := probe(r, "/livez"); w.Code != http.StatusOK {
		t.Fatalf("GET /livez = %d, want 200: a dependency outage must not restart the pod", w.Code)
	}
}

// CI run 43973930994: the health route answered 401 to a caller with no key.
func TestHealthRouteAnswers200WithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy)
	if w := probe(r, "/v1/profiles/health"); w.Code != http.StatusOK {
		t.Fatalf("GET /v1/profiles/health with no credentials = %d, want 200", w.Code)
	}
}

func TestMetricsAnswer200WithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy)
	probe(r, "/v1/profiles/health")
	w := probe(r, "/metrics")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /metrics with no credentials = %d, want 200", w.Code)
	}
	want := `atpost_identity_profile_service_http_requests_total{method="GET",path="/v1/profiles/health",service="identity-profile-service",status="200"} 1`
	if !strings.Contains(w.Body.String(), want) {
		t.Fatalf("/metrics lacks %q", want)
	}
}

// Opening the probes must not have opened anything else.
func TestGatesStillRefuseAfterProbesWereOpened(t *testing.T) {
	r := newTestRouter(t, healthy)
	const someUser = "/v1/profiles/7b0c9a52-3f0e-4b53-9c36-0a3c9d5a1f11"

	if w := probe(r, someUser); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET %s with no internal key = %d, want 401", someUser, w.Code)
	}
	if w := probe(r, "/no-such-route"); w.Code != http.StatusUnauthorized {
		t.Fatalf("unmatched path with no internal key = %d, want 401", w.Code)
	}
	// Past the internal-key gate, the JWT gate still stands.
	if w := probe(r, "/v1/profiles/me", "X-Internal-Service-Key", routerTestInternalKey); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/profiles/me with the key and no token = %d, want 401", w.Code)
	}
}
