// HTTP handler for /v1/dating/people/:userId (lane D10).
package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetPersonCard — GET /v1/dating/people/:userId.
//
// The compact card for one person, readable only when the viewer has a
// current match with them, a live incoming spark from them, or has them in
// their current deck. Every other case — including a block either way, a
// suspended or deleted profile and a stranger — is the same 404
// CANDIDATE_UNAVAILABLE, so the refusal never reveals which.
func (h *Handler) GetPersonCard(c *gin.Context) {
	viewerID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	card, err := h.svc.GetPersonCard(c.Request.Context(), viewerID, targetID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, card, nil)
}
