package main

import (
	"net/http"
	"strconv"
)

// handleProbe answers Kubernetes /livez + /readyz probes and the legacy
// /health + /v1/health back-compat aliases. Returns true when it served the
// request so the caller can short-circuit. routeCount is consulted by
// /readyz: a gateway with no upstream routes registered fails the readiness
// check, which keeps the ALB from sending live traffic to a misconfigured
// pod.
func handleProbe(w http.ResponseWriter, r *http.Request, routeCount int) bool {
	switch r.URL.Path {
	case "/livez":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"alive"}`))
		return true
	case "/readyz":
		w.Header().Set("Content-Type", "application/json")
		if routeCount == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"down","reason":"no routes registered"}`))
			return true
		}
		_, _ = w.Write([]byte(`{"status":"ready","routes":` + strconv.Itoa(routeCount) + `}`))
		return true
	case "/health", "/v1/health":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return true
	}
	return false
}

// isKubeletProbe reports a request for /livez or /readyz, the two paths the
// chart's probes call (charts/atpost-service/values.yaml). edgeChain serves
// them without the token, rate-limit and session-revocation layers: both
// answers are fixed strings that touch no upstream, and behind the limiters a
// probe could be answered 429 (the per-IP bucket) or made to wait on the
// Redis limiter, and a liveness probe that times out restarts the pod.
// /health and /v1/health stay inside the chain: clients call those.
func isKubeletProbe(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/livez" || r.URL.Path == "/readyz"
}
