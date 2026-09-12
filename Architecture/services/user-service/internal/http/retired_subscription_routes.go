package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Tube channel subscriptions moved to post-service (2026-09-12).
//
// THE DEFECT
//
// post-service owns Tube channels (one per account, the gate for long
// videos) and the videos that subscriptions exist to announce. user-service
// kept a second write path into the same shared `channel_subscriptions`
// table, keyed by the surrogate channel id the Tube API never exposes, with
// no follow edge behind it and a fan-out filter that compared against a
// value ('uploads') the CHECK never allowed, so it selected nobody.
//
// Two writers into one table with two ideas of what a subscription is
// cannot both be right. Founder decision: Subscribe is follow + notify
// behind one button, and unsubscribing removes both. That needs the follow
// written through graph-service BEFORE the row, which only post-service does.
//
// THE FIX
//
// post-service is the canonical writer and reader:
//
//	POST   /v1/channels/{ref}/subscribe
//	DELETE /v1/channels/{ref}/subscribe
//	GET    /v1/channels/{ref}/subscription
//	PATCH  /v1/channels/{ref}/subscription
//	GET    /v1/channels/subscriptions
//
// where {ref} is a handle or the owner's user id. These paths answer 410 and
// name the replacement, so a deployed client fails visibly instead of
// writing a subscription nothing will ever fan out.
//
// The internal fan-out routes (subscriber_fanout_handler.go) stay up until
// notification-service and feed-service switch their base URL; they are
// marked superseded there.

// canonicalSubscriptionsRoute is where the caller's subscriptions now live.
const canonicalSubscriptionsRoute = "/v1/channels/subscriptions"

// retiredSubscriptionRoute answers 410 with Location pointing at the
// post-service surface. canonical is the replacement route pattern for the
// body; the Location header always names the subscriptions list, the one
// replacement address that needs no ref to resolve.
func retiredSubscriptionRoute(canonical string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Location", canonicalSubscriptionsRoute)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "ROUTE_RETIRED",
			"This endpoint has been retired. Tube channel subscriptions are owned by "+
				"post-service: subscribing there follows the owner and records the "+
				"subscription together, which this path never did. Use "+canonical+".",
			map[string]any{"canonical_route": canonical, "location": canonicalSubscriptionsRoute})
	}
}
