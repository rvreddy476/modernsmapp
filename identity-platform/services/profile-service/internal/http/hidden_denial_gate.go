package http

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Account lifecycle — auth-service's 30-day deactivate / scheduled-deletion
// flow (see internal/purge). A hidden account must read as though it does not
// exist to everyone except itself: reversible, never erased, and the whole
// point is that reactivating restores the profile exactly as it was.
//
// This mirrors block_denial_gate.go's shape (same 404-as-missing behavior,
// same self-view carve-out) but the check itself is simpler: unlike a block,
// hidden state is a property of the TARGET, not something relative to who is
// asking, and it is answered by this service's own store rather than an
// external call — so there is no "unconfigured, fail closed" case to handle,
// h.svc.IsHidden is always available on the wired ProfileService.

// deniedByHidden reports whether targetID is hidden and this request must be
// refused, writing the refusal when it must. Returns true when the caller
// should stop.
//
// Self-view is always allowed regardless of hidden state: a user who
// deactivated their own account, or whose 30-day deletion window is still
// running, must still be able to open their own profile (GetMe bypasses this
// gate entirely for the same reason and is never routed through it).
func (h *Handler) deniedByHidden(c *gin.Context, targetID uuid.UUID) bool {
	if viewerID, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil && viewerID == targetID {
		return false
	}

	hidden, err := h.svc.IsHidden(c.Request.Context(), targetID)
	if err != nil {
		// Fail closed, same rationale as the block gate: an unreadable hidden
		// flag must never be treated as "not hidden".
		h.log.Error("hidden check failed; refusing profile read", "err", err,
			"target_id", targetID, "request_id", RequestIDFromContext(c))
		writeProfileNotFound(c)
		return true
	}
	if hidden {
		writeProfileNotFound(c)
		return true
	}
	return false
}

// filterHiddenProfileMap is the batch-lookup form of deniedByHidden. A hidden
// entry is omitted from the map entirely — the same shape the caller would
// see for a user ID that does not exist — except the caller's own entry,
// which is always kept.
func (h *Handler) filterHiddenProfileMap(c *gin.Context, m map[uuid.UUID]*PublicProfile) map[uuid.UUID]*PublicProfile {
	if len(m) == 0 {
		return m
	}
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id"))

	out := make(map[uuid.UUID]*PublicProfile, len(m))
	for id, p := range m {
		if id == viewerID {
			out[id] = p
			continue
		}
		hidden, err := h.svc.IsHidden(c.Request.Context(), id)
		if err != nil {
			h.log.Error("hidden check failed on batch lookup; dropping every result",
				"err", err, "request_id", RequestIDFromContext(c))
			return map[uuid.UUID]*PublicProfile{}
		}
		if !hidden {
			out[id] = p
		}
	}
	return out
}
