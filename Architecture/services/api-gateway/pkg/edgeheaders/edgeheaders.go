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
const GraphWriteSourceHeader = "X-Graph-Write-Source"

// RiderWriteSourceHeader names the calling service on a rider request.
const RiderWriteSourceHeader = "X-Rider-Write-Source"

// GatewayWriteSource is the value allowlists recognise for
// end-user actions proxied through the edge.
const GatewayWriteSource = "api-gateway"

// GraphPathPrefix is the route family whose mutations carry attribution.
const GraphPathPrefix = "/v1/graph"

// RiderPathPrefix is the route family for mobility requests.
const RiderPathPrefix = "/v1/rider"

// StampGraphWriteSource attributes a graph mutation to the gateway.
func StampGraphWriteSource(r *http.Request) {
	if isGraphPath(r.URL.Path) {
		r.Header.Set(GraphWriteSourceHeader, GatewayWriteSource)
	}
}

// StampRiderWriteSource attributes a rider request to the gateway.
func StampRiderWriteSource(r *http.Request) {
	if isRiderPath(r.URL.Path) {
		r.Header.Set(RiderWriteSourceHeader, GatewayWriteSource)
	}
}

func isGraphPath(path string) bool {
	return path == GraphPathPrefix || strings.HasPrefix(path, GraphPathPrefix+"/")
}

func isRiderPath(path string) bool {
	return path == RiderPathPrefix || strings.HasPrefix(path, RiderPathPrefix+"/")
}
