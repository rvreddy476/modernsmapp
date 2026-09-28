package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// requirePostReadable is the post detail's read gate (visibility incl.
// private shares, then the age gate; service.PostReadGate) for a route that
// serves or writes something about one post (2026-09-29, the leak list from
// the Creator Hub audit). A refusal is answered exactly as
// GET /v1/posts/:postId answers it — 404 NOT_FOUND, 401
// AGE_RESTRICTED_SIGN_IN, 403 AGE_RESTRICTED / AGE_UNVERIFIED — and false
// is returned; the caller stops.
//
// DELETEs of the caller's own state (unreact, undo tune, clear progress,
// unsave) are not gated: they reveal nothing and a viewer who has lost
// access must still be able to clear what they left behind.
func (h *Handler) requirePostReadable(c *gin.Context, postID uuid.UUID, viewerID *uuid.UUID) bool {
	err := h.svc.PostReadGate(c.Request.Context(), postID, viewerID)
	if err == nil {
		return true
	}
	if !writeReadGateError(c, err) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
	return false
}
