package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
)

// The chart's probes call /livez and /readyz with no headers. Behind the
// rate limiters a probe could be answered 429, or wait on the Redis limiter;
// a liveness probe that fails restarts the pod. edgeChain serves the two
// paths ahead of the whole chain.

// refuseAll stands in for a rate limiter that is refusing everything (a
// shared per-IP bucket that is empty) and records what reached it.
func refuseAll(reached *[]string) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*reached = append(*reached, r.URL.Path)
			w.WriteHeader(http.StatusTooManyRequests)
		})
	}
}

func probeEdge(t *testing.T, reached *[]string) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	routes := []route{newRoute("/v1/posts", target, edgeTestInternalKey)}
	core := newCoreHandler(routes, nil, true, nil, disabledprefixes.Set{})
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	return edgeChain(keys, devTestPolicy(), refuseAll(reached), nil, core)
}

func edgeStatus(h http.Handler, method, path string) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w.Code
}

func TestKubeletProbesAreServedAheadOfTheRateLimiter(t *testing.T) {
	var reached []string
	gw := probeEdge(t, &reached)
	for _, path := range []string{"/livez", "/readyz"} {
		if got := edgeStatus(gw, http.MethodGet, path); got != http.StatusOK {
			t.Errorf("GET %s with the limiter refusing everything = %d, want 200", path, got)
		}
	}
	if len(reached) != 0 {
		t.Fatalf("probes reached the rate limiter: %v", reached)
	}
}

// Only the two probe paths, and only as reads, skip the chain: everything
// else still meets the limiter, the client-facing health aliases included.
func TestOnlyTheKubeletProbesSkipTheEdgeChain(t *testing.T) {
	var reached []string
	gw := probeEdge(t, &reached)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/health"},
		{http.MethodGet, "/v1/health"},
		{http.MethodGet, "/v1/posts"},
		{http.MethodGet, "/livez/extra"},
		{http.MethodGet, "/v1/livez"},
		{http.MethodPost, "/livez"},
		{http.MethodPost, "/readyz"},
	} {
		if got := edgeStatus(gw, tc.method, tc.path); got != http.StatusTooManyRequests {
			t.Errorf("%s %s = %d, want 429 from the limiter (it must not skip the chain)", tc.method, tc.path, got)
		}
	}
	if len(reached) != 7 {
		t.Fatalf("limiter saw %d of 7 requests: %v", len(reached), reached)
	}
}
