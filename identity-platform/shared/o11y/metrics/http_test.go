package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMetricsCarryTheNamesTheChartRulesRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New("identity-user-service")
	r := gin.New()
	r.GET(Path, m.Handler())
	r.Use(m.Middleware())
	r.GET("/v1/users/:userId", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{"/v1/users/1", "/v1/users/2", "/nope"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, Path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`atpost_identity_user_service_http_requests_total{method="GET",path="/v1/users/:userId",service="identity-user-service",status="200"} 2`,
		`atpost_identity_user_service_http_requests_total{method="GET",path="unmatched",service="identity-user-service",status="404"} 1`,
		`atpost_identity_user_service_http_request_duration_seconds_bucket{method="GET",path="/v1/users/:userId",service="identity-user-service",status="200",le="0.005"}`,
		"go_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	// The scrape itself is registered ahead of the middleware: not counted.
	if strings.Contains(body, `path="/metrics"`) {
		t.Errorf("/metrics counted its own scrape")
	}
}

// Two routers in one process (tests) must not collide on a global registry.
func TestNewTwiceDoesNotPanic(t *testing.T) {
	New("identity-user-service")
	New("identity-user-service")
}
