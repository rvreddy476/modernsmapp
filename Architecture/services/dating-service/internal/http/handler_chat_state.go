package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Audit H2 backstop: chat-service's message-service asks whether a match's
// members may still chat before it accepts a message in a dating
// conversation that chat has not closed yet. Service-only (the
// /v1/dating/internal family): a service token scoped to OpMatchChatState,
// or the legacy internal key, and never a request carrying a gateway user
// identity.
const (
	// OpMatchChatState reads one match's chat state.
	OpMatchChatState = "dating:match.chat_state"
	// InternalMatchChatStatePath is GET; the match id is not personal data.
	InternalMatchChatStatePath = "/v1/dating/internal/matches/:id/chat-state"
)

// matchChatStateResponse is the body's data. Reason never says which member
// failed a rule.
type matchChatStateResponse struct {
	MatchID string `json:"match_id"`
	Open    bool   `json:"open"`
	Reason  string `json:"reason,omitempty"`
}

// GetMatchChatState — GET /v1/dating/internal/matches/:id/chat-state.
// 200 {open, reason}; 404 NOT_FOUND for an unknown match (chat treats it as
// closed); 500 on a lookup failure (chat refuses the send and retries).
// reason "pending_review" is a reversible hold (the match stays open; chat
// answers 409 MATCH_ON_HOLD and caches it briefly); every other reason is a
// closure (409 MATCH_CLOSED).
func (h *Handler) GetMatchChatState(c *gin.Context) {
	matchID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	st, err := h.svc.MatchChatState(c.Request.Context(), matchID)
	if err != nil {
		if errors.Is(err, store.ErrMatchNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "match not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, matchChatStateResponse{
		MatchID: st.MatchID.String(), Open: st.Open, Reason: st.Reason,
	}, nil)
}
