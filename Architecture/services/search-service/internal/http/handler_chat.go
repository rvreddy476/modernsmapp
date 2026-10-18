package http

// GET /internal/v1/search/chat-messages — chat parity CONTRACT §6, lane B5.
//
// The only reader of chat_messages_v1. message-service calls it with the
// signed-in caller's id for GET /v1/chat/search and re-checks membership
// and clear/hide cut-offs on every hit before answering a client.
//
// Service-to-service only:
//   - the path has an `internal` segment, which the api-gateway refuses at
//     the edge (api-gateway/pkg/internalroutes), so no client reaches it;
//   - it demands X-Internal-Service-Key itself, on top of the engine-wide
//     gate, so a future change to that gate cannot expose it; and
//   - with no key configured it answers 503 rather than serving anyone's
//     messages to an unauthenticated caller.
//
// The caller id is a query parameter (not X-User-ID) because the caller is
// message-service, not the user; the results are filtered to messages the
// user is a member of (search.BuildChatSearchBody).

import (
	"crypto/hmac"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ChatMessagesPath is the internal route.
const ChatMessagesPath = "/internal/v1/search/chat-messages"

func (h *Handler) registerChatRoutes(r gin.IRoutes) {
	r.GET(ChatMessagesPath, h.requireServiceCaller, h.SearchChatMessages)
}

// requireServiceCaller fails closed: no configured key → 503, wrong or
// missing key → 401.
func (h *Handler) requireServiceCaller(c *gin.Context) {
	if h.internalKey == "" {
		chatError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "internal service key not configured")
		return
	}
	if !hmac.Equal([]byte(c.GetHeader("X-Internal-Service-Key")), []byte(h.internalKey)) {
		chatError(c, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required")
		return
	}
	c.Next()
}

// SearchChatMessages handles GET /internal/v1/search/chat-messages
// ?user_id=&q=&cursor=&limit= → {data:[{conversation_id, message_id,
// snippet, sender_id, at}], next_cursor}.
func (h *Handler) SearchChatMessages(c *gin.Context) {
	userID := strings.TrimSpace(c.Query("user_id"))
	if _, err := uuid.Parse(userID); err != nil {
		chatError(c, http.StatusBadRequest, "BAD_REQUEST", "user_id must be a UUID")
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if n := utf8.RuneCountInString(q); n < search.ChatQueryMinRunes {
		chatError(c, http.StatusBadRequest, "QUERY_TOO_SHORT", "q must be at least 2 characters")
		return
	} else if n > search.ChatQueryMaxRunes {
		chatError(c, http.StatusBadRequest, "QUERY_TOO_LONG", "q must be at most 200 characters")
		return
	}
	limit := search.ChatSearchDefaultLimit
	if l := c.Query("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil || v < 1 || v > search.ChatSearchMaxLimit {
			chatError(c, http.StatusBadRequest, "BAD_REQUEST", "limit must be 1..50")
			return
		}
		limit = v
	}

	hits, next, err := h.store.SearchChatMessages(c.Request.Context(), search.ChatSearchQuery{
		UserID: userID,
		Q:      q,
		Cursor: c.Query("cursor"),
		Limit:  limit,
	})
	if errors.Is(err, search.ErrInvalidChatCursor) {
		chatError(c, http.StatusBadRequest, "BAD_CURSOR", "cursor is not valid")
		return
	}
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "chat message search failed", "error", err)
		chatError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "search failed")
		return
	}
	var nextCursor any
	if next != "" {
		nextCursor = next
	}
	c.JSON(http.StatusOK, gin.H{"data": hits, "next_cursor": nextCursor})
}

func chatError(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}
