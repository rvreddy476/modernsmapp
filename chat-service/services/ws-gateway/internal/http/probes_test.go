package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// probeGateway is the real handler (NewServer(...).Routes(), as cmd/server
// builds it) on a miniredis the test can stop.
func probeGateway(t *testing.T) (nethttp.Handler, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	server := NewServer(rdb, quietLog(), ServerOptions{JWTSecret: "probe-test-secret"})
	t.Cleanup(server.Close)
	return server.Routes(), mr
}

func probeGet(h nethttp.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, path, nil))
	return rec
}

// The kubelet (and the load balancer's health check) call GET /livez and
// GET /readyz with no headers.
func TestProbesAnswerWithoutCredentials(t *testing.T) {
	h, _ := probeGateway(t)
	for _, path := range []string{"/livez", "/readyz"} {
		if rec := probeGet(h, path); rec.Code != nethttp.StatusOK {
			t.Errorf("GET %s with no credentials: status %d body %s, want 200", path, rec.Code, rec.Body.String())
		}
	}
}

// Without Redis a new socket would connect and hear nothing, so the replica
// must leave the load balancer; it must not be restarted.
func TestReadyzIs503AndLivezStays200WhenRedisIsDown(t *testing.T) {
	h, mr := probeGateway(t)
	addr := mr.Addr()
	mr.Close()

	if rec := probeGet(h, "/livez"); rec.Code != nethttp.StatusOK {
		t.Errorf("GET /livez with redis down: status %d, want 200 (liveness never checks a dependency)", rec.Code)
	}
	rec := probeGet(h, "/readyz")
	if rec.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("GET /readyz with redis down: status %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"redis":"down"`) {
		t.Errorf("GET /readyz body does not name the failing dependency: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), addr) {
		t.Errorf("GET /readyz body leaks the redis address: %s", rec.Body.String())
	}
}

func TestHealthRouteStillAnswersWithoutCredentials(t *testing.T) {
	h, _ := probeGateway(t)
	if rec := probeGet(h, "/health"); rec.Code != nethttp.StatusOK {
		t.Fatalf("GET /health with no credentials: status %d, want 200", rec.Code)
	}
}

// Opening the probes must not have opened the socket routes.
func TestWebsocketRoutesStillRefuseACallerWithNoCredentials(t *testing.T) {
	h, _ := probeGateway(t)
	for _, path := range []string{"/v1/ws/connect", "/v1/ws/notifications"} {
		if rec := probeGet(h, path); rec.Code != nethttp.StatusUnauthorized {
			t.Errorf("GET %s with no credentials: status %d, want 401", path, rec.Code)
		}
	}
	// Nothing else became reachable either (the gateway serves no /metrics:
	// its port is on the internet-facing ingress).
	for _, path := range []string{"/metrics", "/livez/x", "/v1/ws/livez"} {
		if rec := probeGet(h, path); rec.Code != nethttp.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}
