// Package disabledprefixes closes gateway route prefixes whose upstream is
// not deployed in a given environment.
//
// First production deployment (W7, 3 Oct 2026): the route table proxies
// /v1/memories, /v1/flags (+ /v1/admin/flags) and /v1/reviewer to services
// that will not exist in the production cluster at launch, and /v1/food to
// one that is deployed but gated. httputil.ReverseProxy answers a missing
// upstream with a bare "502 Bad Gateway" text body after a DNS failure or a
// connect timeout — slow, un-JSON, and indistinguishable from an outage.
//
// GATEWAY_DISABLED_PREFIXES (comma list of route prefixes) makes the edge
// answer those paths itself, before proxying, with
//
//	404 {"error":{"code":"NOT_AVAILABLE","message":"..."}}
//
// 404 rather than 503 for the same reason the dormant-product gate uses it:
// an edge client should not learn whether a product exists behind a closed
// door, and a 503 would page an on-call for a service that is not supposed
// to be there. /v1/live keeps its own 410 (serveLiveV1Retired runs first).
//
// Every entry must be a prefix the route table knows (exactly, or a path
// under one), so a typo in the value is a boot error rather than a route
// quietly left open. The package lives under pkg/ so it is tracked: the root
// .gitignore has a bare `server` rule that has swallowed new cmd/server/
// files before.
package disabledprefixes

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// EnvVar is the comma-separated list of route prefixes to close.
const EnvVar = "GATEWAY_DISABLED_PREFIXES"

// Code is the error code in the JSON body.
const Code = "NOT_AVAILABLE"

// body is the exact response. Pinned by the tests so a client can rely on it.
const body = `{"error":{"code":"` + Code + `","message":"This API is not available in this deployment"}}`

// Set is the parsed, validated list of closed prefixes. The zero value
// closes nothing.
type Set struct {
	prefixes []string
}

// Parse validates raw (the EnvVar value) against known, the gateway's route
// prefixes. Entries are trimmed; empty entries are ignored; a trailing slash
// is dropped. An entry that is neither a known prefix nor a path under one
// is an error.
func Parse(raw string, known []string) (Set, error) {
	var out []string
	var unknown []string
	seen := map[string]struct{}{}
	for _, entry := range strings.Split(raw, ",") {
		p := normalise(entry)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			return Set{}, fmt.Errorf("%s: %q must be an absolute path such as /v1/memories", EnvVar, strings.TrimSpace(entry))
		}
		if !underAny(p, known) {
			unknown = append(unknown, p)
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		quoted := make([]string, len(unknown))
		for i, u := range unknown {
			quoted[i] = fmt.Sprintf("%q", u)
		}
		return Set{}, fmt.Errorf("%s: %s is not a route prefix the gateway proxies (nor a path under one); "+
			"check the spelling against routeDefinitions in cmd/server/main.go", EnvVar, strings.Join(quoted, ", "))
	}
	sort.Strings(out)
	return Set{prefixes: out}, nil
}

// Prefixes returns the closed prefixes, sorted, for the boot log.
func (s Set) Prefixes() []string { return append([]string(nil), s.prefixes...) }

// Matches reports whether path is one of the closed prefixes or under one.
// Segment-boundary matching: closing /v1/flags never touches /v1/flagship.
func (s Set) Matches(path string) bool { return underAny(path, s.prefixes) }

// Serve answers a closed path and reports whether it did. Any method.
func (s Set) Serve(w http.ResponseWriter, r *http.Request) bool {
	if !s.Matches(r.URL.Path) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(body))
	return true
}

func normalise(entry string) string {
	p := strings.TrimSpace(entry)
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func underAny(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
