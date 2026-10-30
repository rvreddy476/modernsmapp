// Package metricsgate keeps the gateway's Prometheus endpoint off the
// internet.
//
// Audit L1: /metrics was served on the public listener before any route or
// auth check, and the prod ingress sends `/` to the gateway, so anyone could
// read runtime and revocation counters (for example the revocation-check
// failure counter, which tells an attacker when consumer revocation is
// failing open).
//
// Two layers:
//
//   - METRICS_ADDR (e.g. ":9090") starts a separate listener that serves
//     only /metrics, and the public listener stops serving it altogether.
//     That needs the Service and ServiceMonitor to name the new port.
//   - Without METRICS_ADDR, the public listener still answers /metrics, but
//     only to a request that arrived WITHOUT any proxy forwarding header.
//     Everything through the ALB (or ingress-nginx) carries X-Forwarded-For
//     and X-Forwarded-Proto, which a client cannot remove, so the internet
//     gets 404 while an in-cluster Prometheus scraping the pod directly
//     still works.
//
// Lives under pkg/ because a new file under cmd/server/ is git-ignored.
package metricsgate

import (
	"net/http"
	"strings"
	"time"

	"github.com/atpost/api-gateway/pkg/clientip"
)

// EnvListenAddr names the internal metrics listener address.
const EnvListenAddr = "METRICS_ADDR"

// Path is the scrape path.
const Path = "/metrics"

// Handler serves h only to requests that did not come through a proxy, and
// answers every other request with the same 404 an unknown path gets.
func Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if clientip.ArrivedForwarded(r) {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ListenAddr returns the internal listener address, or "" when unset.
func ListenAddr(getenv func(string) string) string {
	return strings.TrimSpace(getenv(EnvListenAddr))
}

// NewServer is the internal listener: /metrics and nothing else.
func NewServer(addr string, h http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(Path, h)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
