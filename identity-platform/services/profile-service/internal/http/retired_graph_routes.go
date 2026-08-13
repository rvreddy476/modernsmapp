package http

import (
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// Module 3 M3-P0-1 / SR-3 — canonical ownership of the social graph.
//
// THE DEFECT
//
// profile-service kept its OWN follow and block tables (`profile.follows`,
// `profile.blocks`) while graph-service kept the canonical ones. Nothing
// reconciled them. The consequence is not a data-tidiness problem, it is a
// safety bypass:
//
//	A user opens a profile page, presses Block, and profile-service writes
//	`profile.blocks`. Feed, search, chat and notifications all enforce blocks
//	by reading graph-service's GetBlockedAndMuted, which has never heard of
//	that row. The blocked account keeps appearing in the feed, keeps showing
//	up in search, and can still open a conversation.
//
// The user was told they were protected and was not. `GET /me/blocks` then
// read the same shadow table back and confirmed the block, so the interface
// actively reinforced a promise the platform was not keeping.
//
// The read paths are retired for the same reason. A follower list served from
// `profile.follows` disagrees with the canonical graph, and a relationship
// response computed from the shadow tables tells a viewer they are blocked (or
// not) on evidence no other service shares.
//
// THE FIX
//
// Every graph route here answers 410 Gone and names its canonical replacement.
// 410 rather than 404 or a silent proxy, deliberately:
//
//   - 404 would read as "this user has no followers" — a wrong answer that
//     looks like a right one.
//   - A silent proxy would leave the duplicate implementation in place, and
//     the next person to touch this service would wire a new caller to it.
//   - 410 is terminal and self-documenting: a deployed client surfaces a
//     visible failure with the replacement route in the body, instead of
//     quietly writing to a table nobody enforces.
//
// `profile.follows` / `profile.blocks` are NOT dropped. They hold real user
// intent — blocks people meant — and reconcileLegacyBlocks (see
// internal/reconcile) migrates them into the canonical graph under an
// any-block-wins rule. Dropping them would silently un-block people.

// canonicalGraphRoutes maps each retired path to the route that replaces it.
// Kept as data so the retirement is enumerable in a test rather than spread
// across handler bodies.
var canonicalGraphRoutes = map[string]string{
	"POST /v1/profiles/:username/follow":    "POST /v1/graph/follow",
	"DELETE /v1/profiles/:username/follow":  "DELETE /v1/graph/follow",
	"POST /v1/profiles/:username/block":     "POST /v1/graph/block",
	"DELETE /v1/profiles/:username/block":   "DELETE /v1/graph/block",
	"GET /v1/profiles/me/blocks":            "GET /v1/graph/blocks",
	"GET /v1/profiles/:userId/followers":    "GET /v1/graph/users/:userId/followers",
	"GET /v1/profiles/:userId/following":    "GET /v1/graph/users/:userId/following",
	"GET /v1/profiles/:userId/relationship": "GET /v1/graph/relationship/:userId",
}

// retiredGraphRoute answers 410 and points the caller at graph-service.
func retiredGraphRoute(canonical string) gin.HandlerFunc {
	return func(c *gin.Context) {
		api.Error(c.Writer, http.StatusGone, "ROUTE_RETIRED",
			"This endpoint has been retired. The social graph is owned by "+
				"graph-service; this service kept a separate copy that no other "+
				"service enforced, so blocks written here did not protect anyone. "+
				"Use "+canonical+".",
			map[string]any{"canonical_route": canonical}, nil)
	}
}
