// Package edgeheaders owns the request headers only the gateway may set.
//
// Module 3 LB-3. It is a `pkg/` package because the root .gitignore contains a
// bare `server` rule: any NEW file under cmd/server/ is untracked, so code and
// tests placed there are missing from a clean checkout. That trap already cost
// this module once (SR-1's token policy), and the CI gate added then is what
// caught it again here.
package edgeheaders

import (
	"net/http"
	"strings"
)

// GraphWriteSourceHeader names the calling service on a mutating graph
// request.
//
// graph-service refuses a mutation whose source is not an approved caller.
// That is only attribution if a CLIENT cannot supply the value — otherwise any
// caller claims to be `api-gateway` and the guard is decoration. So the header
// is stripped from every inbound request and re-set by the gateway.
//
// The constant is duplicated in graph-service rather than shared through a
// module, because the gateway must not depend on graph-service and vice versa.
// A mismatch is caught by graph-service's guard test and this package's test.
const GraphWriteSourceHeader = "X-Graph-Write-Source"

// GatewayWriteSource is the value graph-service's allowlist recognises for
// end-user actions proxied through the edge.
const GatewayWriteSource = "api-gateway"

// GraphPathPrefix is the route family whose mutations carry attribution.
const GraphPathPrefix = "/v1/graph"

// StampGraphWriteSource attributes a graph mutation to the gateway.
//
// LB-3: graph-service defaults to strict mode and refuses an unattributed
// mutation, and no caller stamped the header — so every normal follow and
// block from the app would have received 403. The guard was correct and
// unreachable at the same time.
//
// This must run AFTER the inbound strip, so a forged client value is
// overwritten rather than merely rejected.
//
// Only graph routes are stamped. Stamping everything would let a service
// downstream of an unrelated route replay the header at graph-service and
// inherit the gateway's attribution.
// Matching is EXACT or on a path-segment boundary, not a raw prefix.
//
// A raw HasPrefix would also match "/v1/graphql", "/v1/graphs" and
// "/v1/graph-export" — the same near-prefix mistake the query-token allowlist
// had in SR-1. Stamping a route that is not graph-service's would hand the
// gateway's attribution to a different upstream, which is precisely the
// confusion the label exists to prevent.
func StampGraphWriteSource(r *http.Request) {
	if isGraphPath(r.URL.Path) {
		r.Header.Set(GraphWriteSourceHeader, GatewayWriteSource)
	}
}

func isGraphPath(path string) bool {
	return path == GraphPathPrefix || strings.HasPrefix(path, GraphPathPrefix+"/")
}
