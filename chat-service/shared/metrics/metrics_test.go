package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

var refuseAll = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
})

// The chart's alert rules read exactly these names and labels
// (charts/atpost-service/templates/prometheusrule.yaml).
func TestExportsTheNamesTheChartRulesRead(t *testing.T) {
	m := NewHTTP("chat-message-service")
	m.Observe(http.MethodGet, "/v1/chat/conversations/:id", 500, 30*time.Millisecond)
	m.Observe(http.MethodGet, "", 404, time.Millisecond)

	body := scrape(t, m.Wrap(refuseAll))
	for _, want := range []string{
		`atpost_chat_message_service_http_requests_total{method="GET",path="/v1/chat/conversations/:id",service="chat-message-service",status="500"} 1`,
		`atpost_chat_message_service_http_requests_total{method="GET",path="unmatched",service="chat-message-service",status="404"} 1`,
		`atpost_chat_message_service_http_request_duration_seconds_bucket{method="GET",path="/v1/chat/conversations/:id",service="chat-message-service",status="500",le="0.05"} 1`,
		`atpost_chat_message_service_http_request_duration_seconds_bucket{method="GET",path="/v1/chat/conversations/:id",service="chat-message-service",status="500",le="0.025"} 0`,
		"go_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q", want)
		}
	}
}

func TestWrapPassesEverythingElseThrough(t *testing.T) {
	h := NewHTTP("svc").Wrap(refuseAll)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/metrics/"},
		{http.MethodGet, "/v1/chat/metrics"},
		{http.MethodPost, Path},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d, want the inner router's 401", tc.method, tc.path, rec.Code)
		}
	}
}

// Two services' metrics in one process (tests build several routers) must
// not collide on a global registry.
func TestEachHTTPMetricsHasItsOwnRegistry(t *testing.T) {
	a, b := NewHTTP("svc"), NewHTTP("svc")
	a.Observe(http.MethodGet, "/x", 200, time.Millisecond)
	if strings.Contains(scrape(t, b.Wrap(refuseAll)), `path="/x"`) {
		t.Fatal("a request recorded on one HTTPMetrics showed up on another")
	}
}
