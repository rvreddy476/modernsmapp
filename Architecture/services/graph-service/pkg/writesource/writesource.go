// Package writesource owns the attribution rule for mutating graph writes.
//
// Module 3 LB-3. It is a `pkg/` package so the CI-only edge-auth contract
// module can drive THIS decision function against the gateway's REAL stamping
// function. The two are separate implementations in separate services that
// must agree on a header name and a value, and they did not: graph-service
// defaulted to strict mode and required the header while NO caller stamped it,
// so every normal follow and block from the app would have received 403. The
// guard was correct and unreachable at the same time.
//
// Each service's own tests passed. Only a test that runs both real pieces can
// catch that class of disagreement, and neither deployable service may depend
// on the other — hence a neutral module and an importable package, the same
// shape LB-1 established.
package writesource

import (
	"net/http"
	"strings"
)

// Header names the calling service on a mutating graph request.
//
// It must equal api-gateway's edgeheaders.GraphWriteSourceHeader. The
// constants are deliberately separate — neither service depends on the other —
// and the contract test asserts they match.
const Header = "X-Graph-Write-Source"

// Allowed is the closed list of services permitted to mutate the social graph.
//
// Adding an entry is a deliberate act: every writer here can create an edge
// that feed, search and chat will honour, so each has been reviewed for block
// safety.
//
// `identity-profile-service` is deliberately ABSENT. Its graph routes were
// retired in SR-3 precisely because the blocks it wrote were enforced by
// nothing; if it reappears here, the shadow graph is back.
var Allowed = map[string]bool{
	"api-gateway":          true, // end-user actions proxied from the edge
	"graph-service":        true, // internal reconcilers and backfills
	"suggestion-service":   true, // accepted follow suggestions
	"trust-safety-service": true, // enforcement actions (auto-block on abuse)
	"user-service":         true, // account lifecycle (deactivate → sever edges)
	"dating-service":       true, // propagates a dating block into the canonical graph
}

// IsMutation reports whether a method changes graph state.
//
// Reads are never gated: block enforcement in feed, search and chat depends on
// graph reads succeeding, and refusing them would turn an attribution gap into
// a platform-wide safety outage.
func IsMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// Decision is the outcome of evaluating a request's attribution.
type Decision int

const (
	// Permit — an approved caller, or a non-mutating request.
	Permit Decision = iota
	// Refuse — a mutation from an unrecognised or absent source.
	Refuse
	// PermitUnattributed — permissive rollout mode only. The write proceeds
	// and the caller should log loudly; the hole is still open.
	PermitUnattributed
)

// Evaluate decides what to do with a request.
//
// strict=false exists only to roll the guard out without an instant outage if
// a legitimate caller was missed. It is NOT the production setting and it
// leaves the duplicate-writer hole open.
func Evaluate(method, source string, strict bool) Decision {
	if !IsMutation(method) {
		return Permit
	}
	if Allowed[strings.TrimSpace(source)] {
		return Permit
	}
	if strict {
		return Refuse
	}
	return PermitUnattributed
}
