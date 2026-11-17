package http

import (
	"context"
	nethttp "net/http"

	"github.com/atpost/chat-shared/health"
)

// ServiceName is the chart's service.name (deploy/services/chat-ws-gateway).
const ServiceName = "chat-ws-gateway"

// newReadiness is what "ready to take a new websocket" means here: Redis
// answers. Every frame a socket receives arrives over Redis pub/sub, and
// presence and the per-user connection cap live there too, so a socket
// accepted without Redis would connect and then hear nothing. It is also the
// only connection cmd/server refuses to boot without.
//
// Left out on purpose:
//   - chat_db, graph-service, message-service, post-service, live-service:
//     each backs one optional feature and boot only warns when it is missing.
//   - the per-process socket cap: a full replica already answers new upgrades
//     with 503, and failing readiness would take it out of the load balancer
//     and drop the sockets it is serving.
func newReadiness(s *Server) *health.Checker {
	c := health.New(ServiceName, s.log)
	c.Register("redis", func(ctx context.Context) error { return s.rdb.Ping(ctx).Err() })
	return c
}

// withProbes puts GET /livez and /readyz in front of everything else the
// gateway serves (the mux, the websocket auth inside handleWS, request
// logging), so the kubelet and the load balancer, which send no credentials,
// are never refused and never logged.
func (s *Server) withProbes(next nethttp.Handler) nethttp.Handler {
	return s.ready.Wrap(next)
}
