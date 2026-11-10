package http

// Blocks on the profile reads (security walk, 10 Nov 2026).
//
// When B blocks A, A could still read B's profile by id or handle: posts and
// lists were already withheld across a block, the profile object was not. A
// block in EITHER direction now makes the profile read, and every
// sub-resource read behind requireServableUser, answer exactly what a missing
// account answers (respondUserNotFound), so the blocked party cannot tell
// "blocked" from "no such account".
//
// The block facts come from the same graph-service relationship call the
// About audience already uses (fetchViewerRelationship, read through
// aboutRelationship.blockedEitherWay); there is no second block store.
//
// Who is checked: a request carrying a gateway-set X-User-Id that is not the
// owner. The owner always sees their own profile. In-cluster callers that
// name no user (message-service, post-service mentions, group-service's
// handle check, live eligibility) are not viewers and are not checked. A
// signed-out read cannot be tied to anyone, so it is not checked either, the
// same as public posts.
//
// A graph failure fails CLOSED with 503 PROFILE_UNAVAILABLE for that viewer,
// matching post-service, which denies on an unavailable graph. A 503 says
// nothing about a block: every signed-in viewer gets it during an outage.

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// blockChecker answers "has either of these two blocked the other".
type blockChecker interface {
	BlockedEitherWay(ctx context.Context, viewerID, ownerID uuid.UUID) (bool, error)
}

// graphBlocks is the production blockChecker: the handler's own graph call.
type graphBlocks struct{ h *Handler }

func (g graphBlocks) BlockedEitherWay(ctx context.Context, viewerID, ownerID uuid.UUID) (bool, error) {
	rel, err := g.h.fetchViewerRelationship(ctx, viewerID, ownerID)
	if err != nil {
		return false, err
	}
	return rel.blockedEitherWay(), nil
}

// WithBlockChecker replaces the block source (tests).
func (h *Handler) WithBlockChecker(b blockChecker) *Handler {
	h.blocks = b
	return h
}

// CodeProfileUnavailable is the 503 a signed-in viewer gets when the block
// check cannot be answered.
const CodeProfileUnavailable = "PROFILE_UNAVAILABLE"

// admitProfileRead is the one gate in front of another account's profile
// data: the account must be servable (profile_visibility.go) and no block
// may stand between the viewer and the owner. It writes the response and
// returns false when the read must stop.
func (h *Handler) admitProfileRead(c *gin.Context, u *store.User) bool {
	if !h.servable(c, u) {
		respondUserNotFound(c)
		return false
	}
	blocked, err := h.viewerBlockedFromProfile(c, u.ID)
	if err != nil {
		slog.Warn("user-service: profile block check failed; refusing the read",
			"reason", err.Error(), "path", c.FullPath())
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			CodeProfileUnavailable, "Profile could not be loaded right now; try again", nil)
		return false
	}
	if blocked {
		respondUserNotFound(c)
		return false
	}
	return true
}

// viewerBlockedFromProfile reports whether the request's signed-in viewer and
// ownerID are separated by a block in either direction.
func (h *Handler) viewerBlockedFromProfile(c *gin.Context, ownerID uuid.UUID) (bool, error) {
	raw := strings.TrimSpace(c.GetHeader("X-User-Id"))
	if raw == "" || h.blocks == nil {
		return false, nil
	}
	viewerID, err := uuid.Parse(raw)
	if err != nil || viewerID == ownerID {
		// A malformed identity is not a viewer (mayReadPrivateProfile also
		// refuses it private fields); the owner is never blocked from self.
		return false, nil
	}
	return h.blocks.BlockedEitherWay(c.Request.Context(), viewerID, ownerID)
}
