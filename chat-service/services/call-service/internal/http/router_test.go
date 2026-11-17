package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/chat-shared/health"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// probeRouter is the real router (NewRouter, as cmd/server builds it) with
// the auth middleware configured and one readiness check that answers err.
func probeRouter(t *testing.T, dependencyErr error) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ready := health.New(ServiceName, nil)
	ready.Register("postgres", func(context.Context) error { return dependencyErr })
	router, err := NewRouter(
		New(nil, nil).WithCallsEnabled(true).WithInternalServiceKey(testInternalKey),
		RouterDeps{
			Keys:  JWTKeySet{ActiveSecret: "router-test-secret", InternalServiceKey: testInternalKey},
			Ready: ready,
		})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func get(router http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The kubelet calls GET /livez and GET /readyz with no headers. If either is
// refused the pod is restarted or never receives traffic.
func TestProbesAnswerWithoutCredentials(t *testing.T) {
	router := probeRouter(t, nil)
	for _, path := range []string{"/livez", "/readyz"} {
		if rec := get(router, path); rec.Code != http.StatusOK {
			t.Errorf("GET %s with no credentials: status %d body %s, want 200", path, rec.Code, rec.Body.String())
		}
	}
}

func TestReadyzIs503AndLivezStays200WhenADependencyIsDown(t *testing.T) {
	router := probeRouter(t, errors.New("connection refused"))
	if rec := get(router, "/livez"); rec.Code != http.StatusOK {
		t.Errorf("GET /livez with postgres down: status %d, want 200 (liveness never checks a dependency)", rec.Code)
	}
	rec := get(router, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz with postgres down: status %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"postgres":"down"`) {
		t.Errorf("GET /readyz body does not name the failing dependency: %s", rec.Body.String())
	}
}

func TestHealthRouteStillAnswersWithoutCredentials(t *testing.T) {
	if rec := get(probeRouter(t, nil), "/health"); rec.Code != http.StatusOK {
		t.Fatalf("GET /health with no credentials: status %d, want 200", rec.Code)
	}
}

// Opening the probes must not have opened anything else.
func TestProtectedRoutesStillRefuseACallerWithNoCredentials(t *testing.T) {
	router := probeRouter(t, nil)
	id := uuid.NewString()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/calls/" + id},
		{http.MethodPost, "/v1/calls"},
		{http.MethodPost, "/internal/v1/calls/" + id + "/summary"},
		{http.MethodGet, "/v1/calls/livez"},
		{http.MethodGet, "/no-such-route"},
		{http.MethodPost, "/metrics"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no credentials: status %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// Prometheus scrapes GET /metrics on the service port with no credentials
// (charts/atpost-service/templates/servicemonitor.yaml), and the chart's
// alert rules read these two series.
func TestMetricsAnswerWithoutCredentialsAndCountRequestsByRoute(t *testing.T) {
	router := probeRouter(t, nil)
	get(router, "/v1/calls/"+uuid.NewString()) // 401 from the auth middleware
	get(router, "/livez")

	rec := get(router, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics with no credentials: status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`atpost_chat_call_service_http_requests_total{method="GET",path="/v1/calls/:callId",service="chat-call-service",status="401"} 1`,
		`atpost_chat_call_service_http_request_duration_seconds_bucket{method="GET",path="/v1/calls/:callId",service="chat-call-service",status="401",le="+Inf"} 1`,
		"go_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q", want)
		}
	}
	if strings.Contains(body, `path="/livez"`) || strings.Contains(body, `path="/metrics"`) {
		t.Error("probe or scrape requests were counted as HTTP traffic")
	}
}
