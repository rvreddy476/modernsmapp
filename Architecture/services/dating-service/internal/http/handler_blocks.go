// HTTP handlers for /v1/dating/blocks (lane D10).
package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ListBlocks — GET /v1/dating/blocks.
// The people the caller has blocked, newest first, as compact cards with no
// photo. POST /v1/dating/safety/block remains the way to add one.
func (h *Handler) ListBlocks(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	items, err := h.svc.ListBlocks(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// DeleteBlock — DELETE /v1/dating/blocks/:userId.
//
// Unblocks the person. Nothing the block severed comes back: the match it
// closed stays closed and the sparks it deleted stay deleted. A block a
// report raised can be lifted the same way by the reporter; the report and
// its grievance are untouched. Idempotent — unblocking someone who is not
// blocked answers 200 with removed=false.
func (h *Handler) DeleteBlock(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	removed, err := h.svc.Unblock(c.Request.Context(), userID, targetID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UNBLOCK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"unblocked": true, "removed": removed}, nil)
}
