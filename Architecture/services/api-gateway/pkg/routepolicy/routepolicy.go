// Package routepolicy names the upstreams the edge must never proxy.
//
// Commerce P0 LB-1. The gateway injects `X-Internal-Service-Key` into every
// request it forwards (see injectInternalKeyMiddleware). That header is the
// only thing payments-service checked, so proxying `/v1/payments` handed
// every authenticated end user full payment authority: create an intent for
// any amount, PATCH it to `succeeded`, and refund it. The chain needed no
// PSP contact at all.
//
// The first instinct is to move those routes under `/internal/` and lean on
// requireAdminForInternalPaths. That is not a fix. `/internal/` is an
// ADMIN-CLIENT surface — it gates on an `admin` or `moderator` scope in a
// user's JWT — which is a different thing from a service-only surface. It
// narrows who can steal money; it does not stop money being stealable from
// a browser.
//
// So the boundary is drawn here instead: payments is not reachable from the
// edge at all, by anyone, at any scope. Commerce calls it in-cluster with an
// audience-scoped service token (shared/servicetoken), and the PSP webhook
// arrives on its own ingress authenticated by signature.
//
// This package lives under pkg/ deliberately. The repository's root
// .gitignore contains a bare `server` rule, so any NEW file under
// cmd/server/ is untracked and would vanish on a clean checkout — a trap
// this codebase has already been bitten by twice. Code that must survive
// review has to live outside cmd/server/.
package routepolicy

import (
	"fmt"
	"sort"
	"strings"
)

// ForbiddenPrefixes are path prefixes the edge must never forward.
//
// Adding an entry here is a security decision; removing one is a bigger
// security decision. GuardRouteTable turns both into a startup failure
// rather than a silent regression.
var ForbiddenPrefixes = []string{
	// Payment authority. Every write here can move money, and the service
	// trusts the gateway-injected key. See LB-1..LB-5.
	"/v1/payments",
}

// ForbiddenTargets are upstreams the edge must never forward to, whatever
// prefix is pointed at them.
//
// Review §6.4. GuardRouteTable classified only the route's PREFIX and ignored
// its TARGET, so the guard's claim to be a backstop was overstated: a future
//
//	{"/v1/money", env("PAYMENTS_SERVICE_URL", …)}
//
// passes the prefix check cleanly and restores the whole LB-1 exploit under a
// different path. The prefix list stops the obvious re-add; this stops the
// rename. Matched as a substring of the target URL because the target is a
// URL with a scheme, host and port, and the service name is the stable part
// of it across dev, staging and production.
var ForbiddenTargets = []string{
	"payments-service",
}

// Route is the minimal shape of a gateway route entry, so this package can
// validate the table without importing cmd/server.
type Route struct {
	Prefix string
	Target string
}

// GuardRouteTable reports every route that must not exist.
//
// main.go calls this at boot and refuses to start on a violation. The
// alternative — a code review that notices someone re-adding
// `{"/v1/payments", …}` — has already failed once in this repository's
// history, which is why the check is executable.
func GuardRouteTable(routes []Route) error {
	var bad []string
	for _, r := range routes {
		flagged := false
		for _, f := range ForbiddenPrefixes {
			if matches(r.Prefix, f) {
				bad = append(bad, fmt.Sprintf("%s -> %s (forbidden prefix %s)", r.Prefix, r.Target, f))
				flagged = true
				break
			}
		}
		if flagged {
			continue
		}
		// Review §6.4: the TARGET matters as much as the prefix. Renaming
		// the path does not change what the upstream can do with the
		// gateway-injected key.
		for _, t := range ForbiddenTargets {
			if strings.Contains(r.Target, t) {
				bad = append(bad, fmt.Sprintf("%s -> %s (forbidden upstream %s)", r.Prefix, r.Target, t))
				break
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf(
		"routepolicy: the gateway must not proxy service-authority upstreams, but the route table contains: %s. "+
			"These endpoints inherit X-Internal-Service-Key from the edge and would grant every authenticated user "+
			"the ability to create, complete and refund payments. Call them in-cluster with a service token instead",
		strings.Join(bad, ", "))
}

// IsForbidden reports whether a request path targets a forbidden upstream.
//
// The gateway also checks this per request, so a path that slips past route
// matching (a future catch-all, a rewrite, a default upstream) still cannot
// reach payments. Belt and braces: the route table is the control, this is
// the backstop.
func IsForbidden(path string) bool {
	for _, f := range ForbiddenPrefixes {
		if matches(path, f) {
			return true
		}
	}
	return false
}

// matches is exact-or-segment-boundary, never a raw prefix.
//
// A bare strings.HasPrefix would treat "/v1/paymentsomething" as forbidden
// and, worse, invite the mirror-image bug elsewhere: the repository has
// already had a near-prefix defect where "/v1/graph" matched "/v1/graphql"
// and handed one service's attribution to another. Same discipline here.
func matches(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
