package http

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Who may read whose follower / following lists (GET /followers/:userId,
// GET /following/:userId, GET /:userId/following-ids).
//
// Audit HG4 hid the lists from users the owner has blocked. The security
// sweep of 21 Oct 2026 added two more rules:
//
//   - #8: a PRIVATE account's lists are for the owner and their approved
//     followers. post-service already hides such an account's posts from
//     everyone else (post.go GetPostsByAuthor); the follower lists were
//     served to anyone.
//   - #7: /following-ids, documented "internal" but mounted on the public
//     group, now sits behind this same rule instead of none.
//
// Three kinds of caller:
//
//   - the owner (X-User-Id == owner): always;
//   - another signed-in user: refused if the owner blocked them; refused if
//     the owner is private and they do not follow the owner (an APPROVED
//     follow — a pending request is not one);
//   - no X-User-Id at all: an in-cluster service (search, live, dating)
//     calling without a viewer passes as before, which the gateway's
//     X-Graph-Write-Source stamp distinguishes from a SIGNED-OUT edge
//     request — that one is a stranger, and a stranger reads a public
//     account's lists only.
//
// A refusal is the empty list the routes already answer, never an error:
// nothing on the wire says "blocked" or "private" (permission.go:140-142).
// The block lookup keeps failing OPEN (a store blip must not blank every
// profile screen); the follow lookup for a private account fails CLOSED,
// because an unanswered "does this person follow me?" is not a yes.

// callerAllowedToReadConnections decides for one request and one owner.
func (h *Handler) callerAllowedToReadConnections(c *gin.Context, ownerID uuid.UUID) bool {
	ctx := c.Request.Context()
	edge := c.GetHeader(GraphWriteSourceHeader) != ""
	callerID, signedIn := uuid.Parse(c.GetHeader("X-User-Id"))
	if signedIn != nil {
		// No viewer named. In-cluster: the service reads for the owner.
		if !edge {
			return true
		}
		// Signed-out at the edge: a stranger, who may read a public
		// account's lists only.
		return !h.connections.IsPrivateAccount(ctx, ownerID)
	}
	if callerID == ownerID {
		return true
	}
	blocked, err := h.connections.IsBlockedBy(ctx, ownerID, callerID)
	if err == nil && blocked {
		return false
	}
	if !h.connections.IsPrivateAccount(ctx, ownerID) {
		return true
	}
	follows, err := h.connections.IsFollowing(ctx, callerID, ownerID)
	return err == nil && follows
}
