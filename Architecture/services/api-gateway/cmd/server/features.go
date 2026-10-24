package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
	"github.com/atpost/api-gateway/pkg/routepolicy"
)

// GET /v1/features (founder decision C, 24 Oct 2026) tells clients which
// parked products this deployment serves, so they hide a closed product's
// every surface instead of showing an error screen.
//
// The map is derived, never configured: a product is enabled only when an
// anonymous request to its prefix would reach its upstream, i.e. the prefix
// is in the route table, GATEWAY_DISABLED_PREFIXES does not close it, the
// dormant-product gate does not close it (dormantProductRefuses, the same
// function the gate calls) and it is not a forbidden path. A pilot allowlist
// does not count: the answer is public and cached, so it is the anonymous
// view, and a pilot user's client keeps its own entry point.
//
// No authentication: jwtExtractMiddleware does not read a credential on this
// path, so a stale cookie cannot 401 it. Rate limiting still applies.
const featuresPath = "/v1/features"

// featureProductPrefixes maps each product key in the response to the
// public prefix that serves it. Adding a key here is the whole change for a
// new parked product.
//
// broadcast_channels is the prefix behind the web messenger's Channels lane
// and Android chat's "Communities" (which ride on broadcast channels);
// communities is the separate community-service product.
var featureProductPrefixes = map[string]string{
	"broadcast_channels": "/v1/broadcast-channels",
	"communities":        "/v1/communities",
	"groups":             "/v1/groups",
	"qa":                 "/v1/qa",
	"pages":              "/v1/pages",
	"memories":           "/v1/memories",
	"food":               "/v1/food",
	"rider":              "/v1/rider",
	"dating":             "/v1/dating",
	"doorstep":           "/v1/doorstep",
}

type featureState struct {
	Enabled bool `json:"enabled"`
}

// featureProducts computes the enabled flag for every product key.
func featureProducts(routes []route, products []dormantProduct, disabled disabledprefixes.Set) map[string]featureState {
	out := make(map[string]featureState, len(featureProductPrefixes))
	for key, prefix := range featureProductPrefixes {
		out[key] = featureState{Enabled: prefixServed(prefix, routes, products, disabled)}
	}
	return out
}

// prefixServed reports whether an anonymous request to prefix passes every
// edge gate in newCoreHandler and matches a route.
func prefixServed(prefix string, routes []route, products []dormantProduct, disabled disabledprefixes.Set) bool {
	if disabled.Matches(prefix) {
		return false
	}
	if dormantProductRefuses(prefix, "", products) {
		return false
	}
	if routepolicy.IsForbidden(prefix) {
		return false
	}
	for _, rt := range routes {
		if prefix == rt.prefix || strings.HasPrefix(prefix, rt.prefix+"/") {
			return true
		}
	}
	return false
}

// featuresBody is the exact response body.
func featuresBody(products map[string]featureState) []byte {
	body, err := json.Marshal(map[string]any{"data": map[string]any{"products": products}})
	if err != nil {
		// A map of bools always marshals; keep the zero answer safe anyway.
		return []byte(`{"data":{"products":{}}}`)
	}
	return body
}

// serveFeatures answers GET/HEAD /v1/features and reports whether it did.
func serveFeatures(w http.ResponseWriter, r *http.Request, body []byte) bool {
	if r.URL.Path != featuresPath {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":{"code":"METHOD_NOT_ALLOWED","message":"Method not allowed"}}`))
		return true
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	// corsMiddleware echoes the caller's Origin, so a shared cache must key
	// on it.
	w.Header().Add("Vary", "Origin")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	return true
}
