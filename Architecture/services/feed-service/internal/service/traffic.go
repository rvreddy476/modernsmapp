package service

import (
	"context"
	"net/http"
	"time"

	"github.com/atpost/shared/httpclient"
)

// Background traffic gets its own HTTP clients (audit H2, 2026-10-30).
//
// Each upstream used to have ONE client and one circuit breaker, shared by
// the request path and the Kafka fan-out. A large fan-out that hit a run of
// graph-service errors opened feed->graph for 30 s, and the request path's
// fail-closed filters (block/mute, can-view, following) then failed every
// home, reels and Tube page on the pod while graph-service was healthy.
//
// Work done for a Kafka event (fan-out pagination, the unfollow claim
// check, the channel subscriber pages, the close-friends settings read) and
// the background mutual-follows warmer now run on separate clients with
// separate breakers ("feed->graph:fanout" and so on). The consumer marks
// its context with WithBackgroundTraffic; every call site picks its client
// with graphFor / postFor / userFor, so a fetcher shared by both paths
// needs no second copy. The breakers' handling of client cancellations is
// shared/httpclient's business, not this file's.

type backgroundTrafficKey struct{}

// WithBackgroundTraffic marks ctx as background (Kafka / warmer) work, so
// upstream calls made under it use the fan-out clients and breakers.
func WithBackgroundTraffic(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundTrafficKey{}, true)
}

// IsBackgroundTraffic reports whether ctx was marked by WithBackgroundTraffic.
func IsBackgroundTraffic(ctx context.Context) bool { return isBackgroundTraffic(ctx) }

func isBackgroundTraffic(ctx context.Context) bool {
	v, _ := ctx.Value(backgroundTrafficKey{}).(bool)
	return v
}

// fanoutClients are the background-traffic clients, one breaker each.
type fanoutClients struct {
	graph *http.Client
	post  *http.Client
	user  *http.Client
}

func newFanoutClients() fanoutClients {
	// Longer timeouts than the request path: nobody is waiting on a
	// screen, and a slow page is better than a retried event.
	return fanoutClients{
		graph: httpclient.NewWithBreaker(10*time.Second, "feed->graph:fanout"),
		post:  httpclient.NewWithBreaker(10*time.Second, "feed->post:fanout"),
		user:  httpclient.NewWithBreaker(10*time.Second, "feed->user:fanout"),
	}
}

// pick returns the background client when ctx is background traffic and
// one is configured, else the request-path client.
func pick(ctx context.Context, request, background *http.Client) *http.Client {
	if background != nil && isBackgroundTraffic(ctx) {
		return background
	}
	return request
}

func (s *Service) graphFor(ctx context.Context) *http.Client {
	return pick(ctx, s.graphClient, s.fanout.graph)
}

func (s *Service) postFor(ctx context.Context) *http.Client {
	return pick(ctx, s.postClient, s.fanout.post)
}

func (s *Service) userFor(ctx context.Context) *http.Client {
	return pick(ctx, s.userClient, s.fanout.user)
}
