package http

import (
	"errors"
	"net/http"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
)

// unavailableMessage is the whole body of an unclassified failure. The real
// error is logged with the request id; driver and peer text never reaches a
// client (L7).
const unavailableMessage = "the service is temporarily unavailable; try again"

// writeServiceError is the one mapping from a service error to a response
// for the conversation, message, settings, pin, request, group and
// invitation routes (C1, C2, L7):
//
//   - not a member, or something that does not exist for the caller: 404
//     NOT_FOUND "not found" — the parity routes' shape, so no route confirms
//     that a conversation exists to a non-member;
//   - a request the caller can fix: 400 (INVALID_REQUEST, or the specific
//     code clients already parse);
//   - a refusal of the action: 403, a conflict with the current state: 409,
//     an idempotency key still processing: 409 IDEMPOTENCY_IN_PROGRESS;
//   - anything unclassified (database, Scylla, Redis, a peer service): 503
//     UNAVAILABLE with a generic message, logged at Error. A client retries
//     a 503; it treats a 4xx as final, so an outage must never look like one.
//
// op names the operation in the log line.
func (h *Handler) writeServiceError(c *gin.Context, op string, err error) {
	if writeClassifiedError(c, err) {
		h.log.Warn(op, "err", err, "request_id", RequestIDFromContext(c))
		return
	}
	h.log.Error(op, "err", err, "request_id", RequestIDFromContext(c))
	api.Error(c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", unavailableMessage, nil, nil)
}

// writeClassifiedError writes the response for an error the service
// classified, and reports whether it did.
func writeClassifiedError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if writeIdempotencyError(c, err) || writeMessagingError(c, err) {
		return true
	}
	status, code, msg := 0, "", err.Error()
	switch {
	case errors.Is(err, service.ErrNotMember), errors.Is(err, service.ErrMessageNotFound),
		errors.Is(err, service.ErrPinNotFound), errors.Is(err, service.ErrNotFound):
		status, code, msg = http.StatusNotFound, "NOT_FOUND", "not found"
	case errors.Is(err, service.ErrInviteLinkNotFound):
		status, code = http.StatusNotFound, "INVITE_NOT_FOUND"
	case errors.Is(err, service.ErrInviteLinkNotLive):
		status, code = http.StatusGone, "INVITE_NOT_LIVE"
	case errors.Is(err, service.ErrInviteJoinBlocked):
		status, code = http.StatusForbidden, "JOIN_NOT_ALLOWED"
	case errors.Is(err, service.ErrNotPermittedRole), errors.Is(err, store.ErrRoleNotPermitted),
		errors.Is(err, service.ErrNotPermittedDelete), errors.Is(err, service.ErrForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, service.ErrMediaNotAllowed):
		status, code = http.StatusUnprocessableEntity, "MEDIA_NOT_ALLOWED"
	case errors.Is(err, store.ErrGroupFull):
		status, code = http.StatusConflict, "GROUP_FULL"
	case errors.Is(err, service.ErrMatchOnHold):
		// Dating audit H2: a member is held for review. Reversible, but a
		// 409 on purpose: clients park a 4xx send for the user to retry and
		// never auto-deliver it to someone under review (a 503 would).
		status, code = http.StatusConflict, "MATCH_ON_HOLD"
	case errors.Is(err, service.ErrMatchClosed):
		status, code = http.StatusConflict, "MATCH_CLOSED"
	case errors.Is(err, service.ErrOwnerMustTransfer), errors.Is(err, store.ErrOwnerRequired),
		errors.Is(err, service.ErrConflict):
		status, code = http.StatusConflict, "CONFLICT"
	case errors.Is(err, service.ErrInvalidInput), errors.Is(err, service.ErrInvalidReaction),
		errors.Is(err, service.ErrInvalidDisappearTimer), errors.Is(err, store.ErrPinLimitReached),
		errors.Is(err, store.ErrNewOwnerNotMember), errors.Is(err, store.ErrNotAMember):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	default:
		return false
	}
	api.Error(c.Writer, status, code, msg, nil, nil)
	return true
}
