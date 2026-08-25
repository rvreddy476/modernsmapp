package http

import (
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-4 / SR-4 — the shared denial decision for profile surfaces.
//
// WHY 404 AND NOT 403
//
// A 403 confirms the profile exists and tells the blocked person they were
// blocked by this specific account. That turns a block into a notification and
// gives a determined harasser a probe: they can enumerate accounts and learn
// exactly who blocked them. 404 is the same answer they would get for an
// account that does not exist, which is the answer that reveals nothing.
//
// The response is deliberately identical, byte for byte, to a genuine
// not-found — a distinguishable message would rebuild the oracle 404 exists to
// remove.

// deniedByBlock reports whether this request must be refused, and writes the
// refusal when it must.
//
// Returns true when the caller should stop. Fail-closed: an unconfigured or
// unreachable block checker denies. Serving profiles without block enforcement
// is not a degraded mode, it is the bug.
func (h *Handler) deniedByBlock(c *gin.Context, targetID uuid.UUID) bool {
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || viewerID == uuid.Nil {
		// Anonymous caller: there is no block relationship to evaluate.
		// Public profiles remain public — the DTO is what protects the
		// sensitive fields for this caller.
		return false
	}
	if viewerID == targetID {
		return false // you can always see yourself
	}

	if h.blocks == nil {
		h.log.Error("block checker not configured; refusing profile read",
			"viewer_id", viewerID, "target_id", targetID,
			"request_id", RequestIDFromContext(c))
		writeProfileNotFound(c)
		return true
	}

	blocked, err := h.blocks.BlockedEitherWay(c.Request.Context(), viewerID, targetID)
	if err != nil {
		// FAIL CLOSED. Failing open would re-expose every blocked user to the
		// person they blocked for the duration of a graph-service incident,
		// silently, with every response still 200.
		h.log.Error("block check failed; refusing profile read", "err", err,
			"viewer_id", viewerID, "target_id", targetID,
			"request_id", RequestIDFromContext(c))
		writeProfileNotFound(c)
		return true
	}
	if blocked {
		writeProfileNotFound(c)
		return true
	}
	return false
}

// writeProfileNotFound emits the SAME body a genuinely missing profile
// produces. Any difference here would let a blocked user distinguish "blocked"
// from "does not exist".
func writeProfileNotFound(c *gin.Context) {
	api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Profile not found", nil, nil)
}

// filterBlockedProfileMap is the batch-lookup form of filterBlockedProfiles.
// A blocked entry is omitted from the map entirely, so the caller sees the
// same shape it would see for a user ID that does not exist.
func (h *Handler) filterBlockedProfileMap(c *gin.Context, m map[uuid.UUID]*PublicProfile) map[uuid.UUID]*PublicProfile {
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || viewerID == uuid.Nil {
		return m
	}
	if len(m) == 0 {
		return m
	}
	if h.blocks == nil {
		h.log.Error("block checker not configured; returning no profiles",
			"viewer_id", viewerID, "request_id", RequestIDFromContext(c))
		return map[uuid.UUID]*PublicProfile{}
	}

	out := make(map[uuid.UUID]*PublicProfile, len(m))
	for id, p := range m {
		if id == viewerID {
			out[id] = p
			continue
		}
		blocked, err := h.blocks.BlockedEitherWay(c.Request.Context(), viewerID, id)
		if err != nil {
			h.log.Error("block check failed on batch lookup; dropping every result",
				"err", err, "viewer_id", viewerID, "request_id", RequestIDFromContext(c))
			return map[uuid.UUID]*PublicProfile{}
		}
		if !blocked {
			out[id] = p
		}
	}
	return out
}

// filterBlockedProfiles removes profiles the viewer must not see from a list
// surface (batch lookup, discovery).
//
// List surfaces need their own handling: denying the whole request because one
// entry is blocked would let a caller probe by bisection, and returning the
// blocked entry would defeat the block. Silently omitting it is the only
// answer that leaks nothing.
//
// On error this returns an EMPTY list, not the unfiltered one. A discovery
// page that fails to load is a visible, temporary problem; a discovery page
// that quietly includes accounts the viewer blocked is not.
func (h *Handler) filterBlockedProfiles(c *gin.Context, profiles []*PublicProfile) []*PublicProfile {
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || viewerID == uuid.Nil {
		return profiles // anonymous: no block relationship to evaluate
	}
	if len(profiles) == 0 {
		return profiles
	}

	if h.blocks == nil {
		h.log.Error("block checker not configured; returning no profiles",
			"viewer_id", viewerID, "request_id", RequestIDFromContext(c))
		return []*PublicProfile{}
	}

	out := make([]*PublicProfile, 0, len(profiles))
	for _, p := range profiles {
		if p == nil || p.UserID == viewerID {
			out = append(out, p)
			continue
		}
		blocked, err := h.blocks.BlockedEitherWay(c.Request.Context(), viewerID, p.UserID)
		if err != nil {
			h.log.Error("block check failed on a list surface; dropping every result",
				"err", err, "viewer_id", viewerID, "request_id", RequestIDFromContext(c))
			return []*PublicProfile{}
		}
		if !blocked {
			out = append(out, p)
		}
	}
	return out
}
