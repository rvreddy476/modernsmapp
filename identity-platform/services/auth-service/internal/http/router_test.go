package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-shared/health"
	"github.com/gin-gonic/gin"
)

// newTestRouter is the engine main.go serves, with the real JWT middleware
// and the two readiness checks main.go registers.
func newTestRouter(t *testing.T, postgres, redis health.CheckFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	probes := health.New("auth-service", nil)
	probes.Register("postgres", postgres)
	probes.Register("redis", redis)
	keys := JWTKeySet{ActiveSecret: "router-test-jwt-secret"}
	r, err := NewRouter(New(&stubAuthService{}, &config.Config{}, nil, nil), RouterConfig{
		Probes:    probes,
		Auth:      AuthMiddlewareWithKeys(keys, nil),
		AdminAuth: AdminAuthMiddlewareWithKeys(keys, nil),
		CSRF:      RequireCSRFMiddleware(),
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func probe(r http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func healthy(context.Context) error { return nil }
func down(context.Context) error    { return errors.New("connection refused") }

// The kubelet sends no headers.
func TestProbesAnswer200WithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy, healthy)
	for _, path := range []string{"/livez", "/readyz"} {
		if w := probe(r, path); w.Code != http.StatusOK {
			t.Fatalf("GET %s with no credentials = %d, want 200", path, w.Code)
		}
	}
}

func TestReadyzIs503AndLivezStays200WhenADependencyIsDown(t *testing.T) {
	cases := map[string][2]health.CheckFunc{
		"postgres": {down, healthy},
		// The rate limiters fail closed without Redis: sign-in cannot be served.
		"redis": {healthy, down},
	}
	for name, checks := range cases {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter(t, checks[0], checks[1])
			if w := probe(r, "/readyz"); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("GET /readyz with %s down = %d, want 503", name, w.Code)
			}
			if w := probe(r, "/livez"); w.Code != http.StatusOK {
				t.Fatalf("GET /livez with %s down = %d, want 200: a dependency outage must not restart the pod", name, w.Code)
			}
		})
	}
}

func TestHealthRouteAnswers200WithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy, healthy)
	if w := probe(r, "/v1/auth/health"); w.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth/health with no credentials = %d, want 200", w.Code)
	}
}

func TestMetricsAnswer200WithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy, healthy)
	probe(r, "/v1/auth/health")
	w := probe(r, "/metrics")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /metrics with no credentials = %d, want 200", w.Code)
	}
	want := `atpost_identity_auth_service_http_requests_total{method="GET",path="/v1/auth/health",service="identity-auth-service",status="200"} 1`
	if !strings.Contains(w.Body.String(), want) {
		t.Fatalf("/metrics lacks %q", want)
	}
}

// Opening the probes must not have opened anything else.
func TestProtectedRouteStillRefusesWithNoCredentials(t *testing.T) {
	r := newTestRouter(t, healthy, healthy)
	if w := probe(r, "/v1/auth/data-export"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/auth/data-export with no token = %d, want 401", w.Code)
	}
}
