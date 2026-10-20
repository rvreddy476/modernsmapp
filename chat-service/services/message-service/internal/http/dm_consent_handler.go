package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DM consent model (founder-approved 20 Oct 2026): request list paging and
// badge count, deny reasons on create-conversation, and the accepted-chat
// state graph-service asks for when deciding calls.

// errorWithReason is the standard error envelope plus a machine-readable
// reason clients use to explain a refusal. The reason is also mirrored in
// details for clients that only read details.
type errorWithReason struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Reason  string            `json:"reason,omitempty"`
		Details map[string]string `json:"details,omitempty"`
	} `json:"error"`
}

func writeErrorWithReason(c *gin.Context, status int, code, message, reason string) {
	var body errorWithReason
	body.Error.Code, body.Error.Message, body.Error.Reason = code, message, reason
	if reason != "" {
		body.Error.Details = map[string]string{"reason": reason}
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(status)
	_ = json.NewEncoder(c.Writer).Encode(body)
}

// writeConsentError maps the DM-consent errors. Returns true when handled.
func writeConsentError(c *gin.Context, err error) bool {
	var denied *service.MessagingNotAllowedError
	switch {
	case errors.As(err, &denied):
		writeErrorWithReason(c, http.StatusForbidden, "MESSAGING_NOT_ALLOWED", err.Error(), denied.Reason)
		return true
	case errors.Is(err, service.ErrRateLimiterUnavailable):
		c.Writer.Header().Set("Retry-After", "30")
		api.Error(c.Writer, http.StatusServiceUnavailable, "RETRY_LATER", err.Error(), nil, nil)
		return true
	}
	return false
}

// RequestsPager is the paged requests surface (the production service).
type RequestsPager interface {
	ListRequestsPage(ctx context.Context, userID uuid.UUID, q service.RequestListQuery) (*service.RequestPage, error)
	CountRequests(ctx context.Context, userID uuid.UUID) (*service.RequestCounts, error)
}

// requestsPageMeta is meta for GET /v1/chat/requests.
type requestsPageMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// requestListQuery reads ?limit, ?offset (legacy), ?cursor, ?filter,
// ?filtered and ?direction. filtered=true|false, when present, overrides
// filter. The default is the caller's incoming, not-hidden requests.
func requestListQuery(c *gin.Context) (service.RequestListQuery, bool) {
	q := service.RequestListQuery{Limit: 20, Filter: c.Query("filter"), Direction: c.Query("direction"), Cursor: c.Query("cursor")}
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			q.Limit = n
		}
	}
	if o := c.Query("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			q.Offset = n
		}
	}
	if f, present := c.GetQuery("filtered"); present {
		v, err := strconv.ParseBool(f)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "filtered must be true or false", nil, nil)
			return q, false
		}
		q.Filter = "all"
		if v {
			q.Filter = "filtered"
		}
	}
	return q, true
}

func (h *Handler) listRequestsPaged(c *gin.Context, pager RequestsPager, userID uuid.UUID) {
	q, ok := requestListQuery(c)
	if !ok {
		return
	}
	page, err := pager.ListRequestsPage(c.Request.Context(), userID, q)
	if err != nil {
		if writeMessagingError(c, err) {
			return
		}
		h.log.Error("failed to list message requests", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list requests", nil, nil)
		return
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(c.Writer).Encode(struct {
		Data []service.RequestRow `json:"data"`
		Meta requestsPageMeta     `json:"meta"`
	}{Data: page.Rows, Meta: requestsPageMeta{NextCursor: page.NextCursor, HasMore: page.HasMore}})
}

// CountMessageRequests — GET /v1/chat/requests/count: the Requests badge.
func (h *Handler) CountMessageRequests(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	pager, ok := h.svc.(RequestsPager)
	if !ok {
		api.Error(c.Writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "request counts are not available", nil, nil)
		return
	}
	counts, err := pager.CountRequests(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to count message requests", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to count requests", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

// DirectChatStateService answers the accepted-chat question.
type DirectChatStateService interface {
	DirectChatAccepted(ctx context.Context, userA, userB uuid.UUID) (bool, error)
}

type directChatStateRequest struct {
	UserA string `json:"user_a" binding:"required"`
	UserB string `json:"user_b" binding:"required"`
}

type directChatStateResponse struct {
	Accepted bool `json:"accepted"`
}

// DirectChatState — POST /internal/v1/chat/direct-chat/state. Service-only:
// graph-service decides calls under who_can_call = accepted_chats_only by
// asking whether the (unordered) pair holds a live 1:1 chat both agreed to.
// Body {user_a, user_b}; 200 {"data":{"accepted":bool}}. Same three gates,
// same order, as the dating-match state route. POST so user ids stay out
// of URLs and access logs.
func (h *Handler) DirectChatState(c *gin.Context) {
	if h.internalServiceKey == "" {
		h.log.Warn("direct-chat state: INTERNAL_SERVICE_KEY not configured; refusing request",
			"request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "MISCONFIGURED", "internal-only endpoint not configured", nil, nil)
		return
	}
	if carriesUserIdentity(c) {
		h.log.Warn("direct-chat state: refused request carrying user identity",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED", "service-only endpoint; user requests are not accepted", nil, nil)
		return
	}
	if c.GetHeader("X-Internal-Service-Key") != h.internalServiceKey {
		h.log.Warn("direct-chat state: bad or missing internal service key",
			"request_id", RequestIDFromContext(c), "ip", c.ClientIP())
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		return
	}
	svc, ok := h.svc.(DirectChatStateService)
	if !ok {
		api.Error(c.Writer, http.StatusNotImplemented, "NOT_IMPLEMENTED", "direct-chat state is not available", nil, nil)
		return
	}
	var body directChatStateRequest
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
	if userA == uuid.Nil || userB == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "user ids must not be nil", nil, nil)
		return
	}
	accepted, err := svc.DirectChatAccepted(c.Request.Context(), userA, userB)
	if err != nil {
		h.log.Warn("direct-chat state lookup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read direct-chat state", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, directChatStateResponse{Accepted: accepted}, nil)
}
