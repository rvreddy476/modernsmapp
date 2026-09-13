package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type realtimeTokenRequest struct {
	Scope string  `json:"scope"`
	ID    *string `json:"id"`
}

var realtimeTokenErrors = []errorMapping{
	{service.ErrRealtimeScopeInvalid, http.StatusUnprocessableEntity, "FOOD_REALTIME_SCOPE_INVALID"},
	{service.ErrRealtimeIDRequired, http.StatusUnprocessableEntity, "FOOD_REALTIME_ID_REQUIRED"},
	{service.ErrRealtimeIDNotAllowed, http.StatusUnprocessableEntity, "FOOD_REALTIME_ID_NOT_ALLOWED"},
	{service.ErrRealtimeNotConfigured, http.StatusServiceUnavailable, "FOOD_REALTIME_NOT_CONFIGURED"},
}

// IssueRealtimeToken — POST /v1/food/realtime/token
//
//	{"scope": "order" | "restaurant" | "delivery", "id": "<uuid>"}
//
// Returns an HMAC-signed token for exactly one scope (see
// service.IssueRealtimeToken for the topics each grants). id is required for
// order and restaurant and refused for delivery. The token lives
// service.RealtimeTokenTTL (5 minutes); notification-service checks it when an
// SSE connection opens, so clients fetch a new one per (re)connect.
//
// 404 FOOD_NOT_FOUND covers an order or restaurant that is not the caller's
// and a caller who is not a delivery partner — never distinguishable from a
// missing id.
func (h *Handler) IssueRealtimeToken(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var body realtimeTokenRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY", "request body must be a JSON object with a scope", nil)
		return
	}
	var id *uuid.UUID
	if body.ID != nil {
		parsed, err := uuid.Parse(*body.ID)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "FOOD_REALTIME_ID_INVALID", "id must be a UUID", nil)
			return
		}
		id = &parsed
	}
	token, err := h.svc.IssueRealtimeToken(ctx, uid, body.Scope, id)
	if err != nil {
		for _, m := range realtimeTokenErrors {
			if errors.Is(err, m.target) {
				api.ErrorWithContext(ctx, c.Writer, m.status, m.code, err.Error(), nil)
				return
			}
		}
		if writeKnownError(c, err) {
			return
		}
		slog.Error("food-service: realtime token failed", "scope", body.Scope, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FOOD_REALTIME_TOKEN_FAILED", "could not issue a realtime token", nil)
		return
	}
	api.JSONWithContext(ctx, c.Writer, http.StatusOK, token)
}

// requireUser extracts the X-User-Id. Mirrors the helper used by the
// other handler files; kept private to avoid polluting handler.go.
func (h *Handler) requireUser(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return uuid.Nil, false
	}
	return id, true
}
