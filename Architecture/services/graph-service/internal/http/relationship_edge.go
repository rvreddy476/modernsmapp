package http

import (
	"github.com/atpost/graph-service/internal/service"
	"github.com/atpost/graph-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The blocked party never learns of the block (security walk, 10 Nov 2026).
//
// The two relationship read routes returned target_blocked_viewer: true (and
// its legacy twin) to the person who had been blocked. That undid the
// anti-enumeration the rest of the graph already keeps: a follow to a blocker
// answers a quiet {"status":"requested"} (quietFollowOutcome) and a
// permission check never says "blocked".
//
// So on EDGE traffic the target's block of the viewer is reported as no
// block: target_blocked_viewer and the legacy key that carries the same fact
// on that route are false. The keys stay in the response so clients that
// read them (Android ProfileRelationship.isBlockedByTarget) parse unchanged.
// What remains is what a stranger sees: graph removes follow edges and
// pending requests when a block lands, so follows / followed_by /
// follow_request_status already read like "no relationship". A fake follow
// to a blocker writes nothing, so follow_request_status stays "none" exactly
// as it does after any unanswered attempt that left no row.
//
// The viewer's OWN block (viewer_blocked_target) is theirs to know and undo
// and is untouched.
//
// In-cluster callers (no X-Graph-Write-Source header: user-service, post,
// feed, message-service, dating, live) still get both directions. They
// enforce the symmetric block rule and must see the fact.

// isEdgeRequest reports whether the request came through the api-gateway,
// which stamps X-Graph-Write-Source on every /v1/graph request after
// stripping any client value (boundActor relies on the same signal).
func isEdgeRequest(c *gin.Context) bool {
	return c.GetHeader(GraphWriteSourceHeader) != ""
}

// edgeRelationship is the single-pair answer for the viewer at the edge. It
// copies: the service may hand back a pointer it also caches.
func edgeRelationship(rel *service.Relationship) *service.Relationship {
	if rel == nil {
		return nil
	}
	out := *rel
	// Single route: legacy "blocked" = the TARGET blocked the viewer.
	out.Blocked = false
	out.TargetBlockedViewer = false
	return &out
}

// edgeRelationshipBatch is the batch answer for the viewer at the edge.
func edgeRelationshipBatch(in map[uuid.UUID]store.Relationship) map[uuid.UUID]store.Relationship {
	out := make(map[uuid.UUID]store.Relationship, len(in))
	for id, rel := range in {
		// Batch route: legacy "blocked_by" = the TARGET blocked the viewer.
		rel.BlockedBy = false
		rel.TargetBlockedViewer = false
		out[id] = rel
	}
	return out
}
