package http

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// Client error bodies carry a stable code and fixed copy. A handler never
// writes err.Error() to a client: store, driver, binder and verifier text
// (table, constraint and field names) stays in the server log. Each writer
// below maps the refusals a client may be told about with errors.Is and
// answers anything else with writeInternalError.

// msgInvalidBody is the answer to a body gin could not bind. The binder's
// text names Go struct fields and validator tags, so it is logged, not sent.
const msgInvalidBody = "Request body is invalid"

func (h *Handler) logger() *slog.Logger {
	if h != nil && h.log != nil {
		return h.log
	}
	return slog.Default()
}

// writeInternalError logs err with the request id and answers a generic 500.
func (h *Handler) writeInternalError(c *gin.Context, msg string, err error) {
	h.logger().Error(msg, "err", err, "request_id", RequestIDFromContext(c))
	api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
}

// writeBindError answers a body that did not bind: 400 INVALID_REQUEST with
// fixed copy; the binder's detail is logged at warn.
func (h *Handler) writeBindError(c *gin.Context, err error) {
	h.logger().Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
	api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", msgInvalidBody, nil, nil)
}

// The writers below do not log: each caller has already logged err with its
// own context.

// writeFollowError answers a FollowUser failure. The two refusals keep their
// 400 FOLLOW_FAILED; anything else (block lookup, insert) is a 500.
func writeFollowError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCannotFollowSelf):
		api.Error(c.Writer, http.StatusBadRequest, "FOLLOW_FAILED", "You cannot follow yourself", nil, nil)
	case errors.Is(err, service.ErrCannotFollowUser):
		api.Error(c.Writer, http.StatusBadRequest, "FOLLOW_FAILED", "You cannot follow this user", nil, nil)
	default:
		writeGenericInternal(c)
	}
}

// writeBlockError answers a BlockUser failure. Blocking yourself keeps its
// 400 BLOCK_FAILED; anything else (the insert) is a 500.
func writeBlockError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrCannotBlockSelf) {
		api.Error(c.Writer, http.StatusBadRequest, "BLOCK_FAILED", "You cannot block yourself", nil, nil)
		return
	}
	writeGenericInternal(c)
}

// writeHandleChangeError answers a ChangeHandle failure. The known refusals
// keep their 400 BAD_REQUEST and add a stable details.reason; a cooldown also
// says when the next change is allowed. Store failures are a 500.
func writeHandleChangeError(c *gin.Context, err error) {
	var cooldown *service.HandleCooldownError
	switch {
	case errors.As(err, &cooldown):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "You changed your handle recently. Try again later.",
			map[string]any{"reason": "HANDLE_COOLDOWN", "cooldown_until": cooldown.Until.UTC().Format(time.RFC3339)}, nil)
	case errors.Is(err, service.ErrUsernameTaken):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "That username is already taken",
			map[string]any{"reason": "USERNAME_TAKEN"}, nil)
	case errors.Is(err, service.ErrUsernameEmpty):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "username is required",
			map[string]any{"reason": "USERNAME_REQUIRED"}, nil)
	case errors.Is(err, service.ErrProfileNotFound):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Profile not found",
			map[string]any{"reason": "PROFILE_NOT_FOUND"}, nil)
	default:
		writeGenericInternal(c)
	}
}

// writeModuleProfileError answers a module-profile service failure. Only the
// service's ErrInvalidModule is a 400 (its fixed copy); any other error
// (store, driver, cache) is logged by the caller and answered with a generic
// 500 so no SQL or driver text leaks.
func writeModuleProfileError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrInvalidModule) {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", service.ErrInvalidModule.Error(), nil, nil)
		return
	}
	api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error", nil, nil)
}

// writeGenericInternal answers a generic 500. The caller has already logged
// the underlying error with its context.
func writeGenericInternal(c *gin.Context) {
	api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
}
