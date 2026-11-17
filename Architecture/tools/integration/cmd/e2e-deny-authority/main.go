// Command e2e-deny-authority stands in, on the e2e "video" tier only, for the
// content authorities media-service asks besides post-service: chat
// (CHAT_MESSAGE_SERVICE_URL), commerce (COMMERCE_SERVICE_URL) and group
// (GROUP_SERVICE_URL).
//
// Why: media-service's delivery gate allows a protected asset when ANY
// authority allows it, and calls the refusal "unresolved" (503) unless EVERY
// authority answered a clean deny (internal/delivery/authz.go
// AnyContentAuthorizer). The video flows assert that B and a stranger get 404
// for an unapproved, deleted or private video; without these three services
// that would be a 503. Starting the real chat, commerce and group stacks for
// a video test is out of scope, so this answers what they answer for a media
// id they do not hold: 404, on every path. It never allows anything, so it
// cannot make a video playable that post-service refused.
//
// Never deploy it. It listens on 127.0.0.1 only.
//
//	E2E_DENY_PORTS=8092,8109,8090 go run ./cmd/e2e-deny-authority
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const defaultPorts = "8092,8109,8090"

func handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health", "/healthz", "/livez", "/readyz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","role":"e2e-deny-authority"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"not held here (e2e deny authority)"}}`))
	})
}

func ports() []string {
	raw := os.Getenv("E2E_DENY_PORTS")
	if strings.TrimSpace(raw) == "" {
		raw = defaultPorts
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var servers []*http.Server
	for _, p := range ports() {
		addr := net.JoinHostPort("127.0.0.1", p)
		srv := &http.Server{Addr: addr, Handler: handler(), ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, srv)
		go func() {
			log.Printf("e2e-deny-authority listening on %s (answers 404 to every media-access question)", addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("e2e-deny-authority %s: %v", addr, err)
			}
		}()
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
}
