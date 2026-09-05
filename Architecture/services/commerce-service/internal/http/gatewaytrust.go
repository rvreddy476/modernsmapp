package http

// Who is allowed to say who the caller is.
//
// Every authenticated route in this service reads the caller's identity from
// the `X-User-Id` request header (`getUserID`). Nothing verified that header.
// It is a plain string on an inbound request, so *anything that could open a
// TCP connection to this pod* could name any user and be believed:
//
//	curl -H 'X-User-Id: <any uuid>' http://commerce:8080/v1/commerce/seller/products
//
// That is not a hypothetical distinction from the gateway's own security. The
// gateway does its half correctly — `trustedIdentityHeaders` deletes any
// inbound `X-User-Id` and re-derives it from the verified JWT, so a header
// arriving from the internet never survives. But the gateway is not the only
// thing that can reach this service. A pod in the same namespace, a
// `kubectl port-forward`, a second ingress, a service mesh
// misconfiguration, a developer's laptop on the VPN — each of those bypasses
// the edge entirely, and each of them was sufficient to read or modify any
// user's cart, orders, addresses and seller catalogue.
//
// Every ownership check in this service — the cross-seller stock refusal, the
// storefront split, order ownership, address ownership — resolves from that
// header. They are all exactly as strong as the answer to "who is allowed to
// set it", and the answer was "anyone".
//
// ─── WHAT THIS DOES ─────────────────────────────────────────────────────
//
// The gateway already injects `X-Internal-Service-Key` on every proxied
// request (api-gateway `injectInternalKeyMiddleware`), and it strips any
// inbound copy first — the header is in `trustedIdentityHeaders` for the same
// reason `X-User-Id` is. So possession of that key is proof the request came
// through the edge. This middleware requires it.
//
// It is defence in depth, not a replacement for the gateway's JWT
// verification. The gateway decides *which* user; this decides *whether the
// caller is allowed to make that claim at all*.
//
// ─── WHAT IS DELIBERATELY LEFT OPEN ─────────────────────────────────────
//
// Only `/v1/commerce` is gated. Health, readiness and metrics must stay
// reachable by the kubelet and the Prometheus scraper, neither of which holds
// the key. They expose no user data.
//
// ─── THE EMPTY-KEY CASE ─────────────────────────────────────────────────
//
// A middleware that silently allows everything when its secret is unset is
// worse than no middleware: it reports a protection that is not there. So
// `RequireGatewayTrust` refuses to be constructed with an empty key, and
// `cmd/server` refuses to START without one in a deployed environment. In a
// local environment the key is simply absent and the middleware is not
// installed — with a log line saying so in as many words.

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// InternalServiceKeyHeader is the header the gateway injects and this service
// requires. It matches shared/middleware.InjectInternalKey and the gateway's
// injectInternalKeyMiddleware.
const InternalServiceKeyHeader = "X-Internal-Service-Key"

// GatewayTrustPrefix is the surface that requires the key.
const GatewayTrustPrefix = "/v1/commerce"

// RequireGatewayTrust rejects any /v1/commerce request that did not come
// through the gateway.
//
// It panics on an empty key rather than degrading to a no-op. A permissive
// security middleware is a lie told to whoever reads the wiring, and this one
// would be sitting over `X-User-Id`.
func RequireGatewayTrust(key string) gin.HandlerFunc {
	if key == "" {
		panic("commerce: RequireGatewayTrust with an empty key — refusing to install a middleware that allows everything")
	}
	want := []byte(key)
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if p != GatewayTrustPrefix && !strings.HasPrefix(p, GatewayTrustPrefix+"/") {
			c.Next()
			return
		}
		// Constant time: the key is a shared secret, and a byte-by-byte
		// comparison over a network-reachable endpoint leaks it.
		got := []byte(c.GetHeader(InternalServiceKeyHeader))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			// 401, not 404. The path's existence is not the secret — the
			// identity claim is. A client that reached this service without
			// the key needs to know its request was rejected for that reason,
			// not to be told the commerce API does not exist.
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized,
				"UNAUTHORIZED", "this request did not come through the API gateway", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
