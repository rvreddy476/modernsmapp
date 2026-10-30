package metricsgate

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/api-gateway/pkg/clientip"
)

var scrape = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("gateway_session_revocation_check_failures_total 0\n"))
})

func get(t *testing.T, h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, Path, nil)
	req.RemoteAddr = "10.30.1.2:4000"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestMetricsRefusedThroughTheLoadBalancer(t *testing.T) {
	// The gate runs behind clientip.Middleware in the gateway, which rewrites
	// the forwarding headers; the decision must use the request as it arrived.
	alb, err := clientip.New([]string{"10.30.0.0/16"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []http.Handler{Handler(scrape), clientip.Middleware(alb, Handler(scrape)), clientip.Middleware(clientip.Resolver{}, Handler(scrape))} {
		for _, hdr := range []map[string]string{
			{"X-Forwarded-For": "203.0.113.5"},
			{"X-Forwarded-Proto": "https"},
			{"X-Real-IP": "203.0.113.5"},
			{"Forwarded": "for=203.0.113.5"},
		} {
			if res := get(t, h, hdr); res.Code != http.StatusNotFound {
				t.Errorf("forwarded %v: status %d, want 404 (body %q)", hdr, res.Code, res.Body.String())
			}
		}
		if res := get(t, h, nil); res.Code != http.StatusOK {
			t.Errorf("direct in-cluster scrape: status %d, want 200", res.Code)
		}
	}
}

func TestInternalServerServesOnlyMetrics(t *testing.T) {
	srv := NewServer(":0", scrape)
	for path, want := range map[string]int{Path: http.StatusOK, "/v1/feed/home": http.StatusNotFound, "/": http.StatusNotFound} {
		res := httptest.NewRecorder()
		srv.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != want {
			t.Errorf("%s: status %d, want %d", path, res.Code, want)
		}
	}
	if got := ListenAddr(func(string) string { return " :9090 " }); got != ":9090" {
		t.Errorf("ListenAddr = %q", got)
	}
}
