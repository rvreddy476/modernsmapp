// Internal dating-match routes called by the dating service: creating
// the match conversation, the opening answer, read receipts, match state
// and turns, plus the check that refuses end-user identity headers on them.

package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateDatingMatchConversation — POST /internal/v1/chat/conversations/dating-match
// Service-only: dating-service calls it in-cluster with X-Internal-Service-Key
// and no user identity. Body: {user_a, user_b, match_id}. Idempotent on
// match_id; a repeat naming a different pair is refused (409).
type createDatingMatchRequest struct {
	UserA   string `json:"user_a" binding:"required"`
	UserB   string `json:"user_b" binding:"required"`
	MatchID string `json:"match_id" binding:"required"`
	// FirstMoverIDs (dating mechanic M5): who may send the first message.
	// Absent or empty: anyone.
	FirstMoverIDs []string `json:"first_mover_ids,omitempty"`
	// ReceiptsGated / CallAfterExchange (dating mechanic M9).
	ReceiptsGated     bool `json:"receipts_gated,omitempty"`
	CallAfterExchange bool `json:"call_after_exchange,omitempty"`
}

// datingReadReceiptsRequest is the body of the read-receipts route. Until
// empty or absent: no receipts.
type datingReadReceiptsRequest struct {
	MatchID string `json:"match_id" binding:"required"`
	UserID  string `json:"user_id" binding:"required"`
	Until   string `json:"until,omitempty"`
}

// datingOpeningAnswerRequest is the body of the opening-answer route.
type datingOpeningAnswerRequest struct {
	MatchID        string `json:"match_id" binding:"required"`
	SenderID       string `json:"sender_id" binding:"required"`
	Text           string `json:"text" binding:"required"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

// userIdentityHeaders mark a request as an end user's. The api-gateway sets
// X-User-Id / X-Verified-User-Id / X-Scopes (and X-Admin-Role) only from a
// verified token, and it also injects X-Internal-Service-Key on every request
// it proxies — so the key alone never proves a service caller. A service call
// carries none of these, nor a user bearer token.
var userIdentityHeaders = []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role", "Authorization"}

// carriesUserIdentity reports whether the request holds any end-user identity.
func carriesUserIdentity(c *gin.Context) bool {
	for _, name := range userIdentityHeaders {
		if strings.TrimSpace(c.GetHeader(name)) != "" {
			return true
		}
	}
	return false
}

func (h *Handler) CreateDatingMatchConversation(c *gin.Context) {
	// Service-only. Three gates, in order: the key must be configured (fail
	// closed), the request must carry no user identity, and it must present
	// the internal key. The route is also outside the gateway's /v1/chat
	// prefix, so the edge cannot reach it at all.
	if h.internalServiceKey == "" {
		// Misconfigured deployment — fail closed.
		h.log.Warn("dating-match conversation: INTERNAL_SERVICE_KEY not configured; refusing request",
			"request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return
	}
	if carriesUserIdentity(c) {
		h.log.Warn("dating-match conversation: refused request carrying user identity",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		h.log.Warn("dating-match conversation: bad or missing internal service key",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return
	}

	var body createDatingMatchRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	userA, err := uuid.Parse(body.UserA)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_a", nil, nil)
		return
	}
	userB, err := uuid.Parse(body.UserB)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_b", nil, nil)
		return
	}
	matchID, err := uuid.Parse(body.MatchID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid match_id", nil, nil)
		return
	}
	firstMovers := make([]uuid.UUID, 0, len(body.FirstMoverIDs))
	for _, raw := range body.FirstMoverIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid first_mover_ids", nil, nil)
			return
		}
		firstMovers = append(firstMovers, id)
	}
	resp, err := h.svc.CreateDatingMatchConversation(c.Request.Context(), userA, userB, matchID, service.DatingMatchOptions{
		FirstMovers: firstMovers, ReceiptsGated: body.ReceiptsGated, CallAfterExchange: body.CallAfterExchange,
	})
	if errors.Is(err, store.ErrDatingMatchPairMismatch) {
		h.log.Warn("dating-match conversation: match_id already bound to a different pair", "match_id", matchID,
			"request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusConflict, "MATCH_PAIR_MISMATCH", "match_id already has a conversation for a different pair", nil, nil)
		return
	}
	if err != nil {
		h.log.Warn("dating-match conversation create failed", "err", err, "match_id", matchID,
			"request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create the dating-match conversation", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// DatingOpeningAnswer — POST /internal/v1/chat/conversations/dating-match/opening-answer
//
// Service-only (dating mechanic M5). Body {match_id, sender_id, text,
// idempotency_key}. Posts the waiting member's opening answer as the first
// message of a first-move conversation. Refused (409) in every other case:
// a first mover, a conversation that already has a message, no first-move
// rule, or a closed match.
func (h *Handler) DatingOpeningAnswer(c *gin.Context) {
	if h.internalServiceKey == "" {
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return
	}
	if carriesUserIdentity(c) {
		h.log.Warn("dating opening answer: refused request carrying user identity",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return
	}
	var body datingOpeningAnswerRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	matchID, err := uuid.Parse(body.MatchID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid match_id", nil, nil)
		return
	}
	senderID, err := uuid.Parse(body.SenderID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid sender_id", nil, nil)
		return
	}
	msg, err := h.svc.SendDatingOpeningAnswer(c.Request.Context(), matchID, senderID, body.Text, body.IdempotencyKey)
	switch {
	case errors.Is(err, service.ErrDatingConversationNotFound):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "no conversation for this match", nil, nil)
		return
	case errors.Is(err, service.ErrOpeningAnswerNotAllowed), errors.Is(err, service.ErrMatchClosed):
		api.Error(c.Writer, http.StatusConflict, "OPENING_ANSWER_NOT_ALLOWED", err.Error(), nil, nil)
		return
	case err != nil:
		if writeMessagingError(c, err) {
			return
		}
		h.log.Warn("dating opening answer failed", "err", err, "match_id", matchID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "could not send the opening answer", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, msg, nil)
}

// DatingReadReceipts — POST /internal/v1/chat/conversations/dating-match/read-receipts
//
// Service-only (dating mechanic M9). Body {match_id, user_id, until}: the
// member sees read receipts in the match's conversation until `until`
// (RFC 3339); empty means not at all.
func (h *Handler) DatingReadReceipts(c *gin.Context) {
	if h.internalServiceKey == "" {
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return
	}
	if carriesUserIdentity(c) {
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return
	}
	var body datingReadReceiptsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	matchID, err := uuid.Parse(body.MatchID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid match_id", nil, nil)
		return
	}
	userID, err := uuid.Parse(body.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_id", nil, nil)
		return
	}
	var until *time.Time
	if body.Until != "" {
		t, err := time.Parse(time.RFC3339, body.Until)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "until must be RFC 3339", nil, nil)
			return
		}
		until = &t
	}
	err = h.svc.SetDatingReceiptsUntil(c.Request.Context(), matchID, userID, until)
	switch {
	case errors.Is(err, service.ErrDatingConversationNotFound):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "no conversation member for this match", nil, nil)
		return
	case err != nil:
		h.log.Warn("dating read receipts update failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update read receipts", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"updated": true}, nil)
}

// DatingMatchState — POST /internal/v1/chat/dating-match/state
// Service-only: graph-service calls it in-cluster with X-Internal-Service-Key
// and no user identity. Body: {user_a, user_b}. Answers whether the pair holds
// an OPEN source_app='dating' conversation (closed_at IS NULL, neither side
// departed) — the same row that gates messages, so chat and calls agree.
//
// POST, not GET: user ids never belong in a query string or an access log.
type datingMatchStateRequest struct {
	UserA string `json:"user_a" binding:"required"`
	UserB string `json:"user_b" binding:"required"`
}

type datingMatchStateResponse struct {
	OpenMatch bool `json:"open_match"`
}

func (h *Handler) DatingMatchState(c *gin.Context) {
	// Service-only. Same three gates, same order, as
	// CreateDatingMatchConversation: key configured (fail closed), no user
	// identity on the request, correct internal key.
	if h.internalServiceKey == "" {
		h.log.Warn("dating-match state: INTERNAL_SERVICE_KEY not configured; refusing request",
			"request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return
	}
	if carriesUserIdentity(c) {
		h.log.Warn("dating-match state: refused request carrying user identity",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		h.log.Warn("dating-match state: bad or missing internal service key",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return
	}

	var body datingMatchStateRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	userA, err := uuid.Parse(body.UserA)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_a", nil, nil)
		return
	}
	userB, err := uuid.Parse(body.UserB)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_b", nil, nil)
		return
	}
	open, err := h.svc.HasOpenDatingMatch(c.Request.Context(), userA, userB)
	if err != nil {
		h.log.Warn("dating-match state lookup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read dating match state", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, datingMatchStateResponse{OpenMatch: open}, nil)
}

// DatingTurns — POST /internal/v1/chat/dating-match/turns (dating mechanic
// M11). Service-only, the same three gates as DatingMatchState. Body:
// {user_id}. Answers how many of the user's open dating-match conversations
// wait on a reply from them.
type datingTurnsRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

type datingTurnsResponse struct {
	Owed int `json:"owed"`
}

func (h *Handler) DatingTurns(c *gin.Context) {
	if h.internalServiceKey == "" {
		h.log.Warn("dating turns: INTERNAL_SERVICE_KEY not configured; refusing request",
			"request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return
	}
	if carriesUserIdentity(c) {
		h.log.Warn("dating turns: refused request carrying user identity",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		h.log.Warn("dating turns: bad or missing internal service key",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return
	}
	var body datingTurnsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	userID, err := uuid.Parse(body.UserID)
	if err != nil || userID == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid user_id", nil, nil)
		return
	}
	owed, err := h.svc.DatingTurnsOwed(c.Request.Context(), userID)
	if err != nil {
		h.log.Warn("dating turns lookup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count dating turns", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, datingTurnsResponse{Owed: owed}, nil)
}
