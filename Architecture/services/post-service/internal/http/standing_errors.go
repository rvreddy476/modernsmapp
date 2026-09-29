package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Author standing on the wire (Copyright Match plan section 6.4, P-5).
//
// Every interactive publication route answers the same two refusals, so the
// mapping lives here once and each handler asks writeStandingError first:
//
//	403 AUTHOR_SUSPENDED      details {"standing","suspended_until","policy_version"}
//	503 STANDING_UNAVAILABLE  trust-safety could not answer; fail closed, retry later
//
// suspended_until is RFC3339 UTC or null (a "restricted" standing names no
// end). The message never says WHY the author is suspended: that is the
// strike detail behind trust-safety's own routes.
const (
	CodeAuthorSuspended     = "AUTHOR_SUSPENDED"
	CodeStandingUnavailable = "STANDING_UNAVAILABLE"
)

// writeStandingError writes the response for a standing refusal and
// reports whether it did.
func writeStandingError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	var suspended *service.AuthorSuspendedError
	switch {
	case errors.As(err, &suspended):
		details := gin.H{
			"standing":        suspended.Standing,
			"suspended_until": nil,
			"policy_version":  suspended.PolicyVersion,
		}
		if suspended.SuspendedUntil != nil {
			details["suspended_until"] = suspended.SuspendedUntil.UTC().Format(time.RFC3339)
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAuthorSuspended,
			"Your account cannot publish right now", details)
	case errors.Is(err, service.ErrAuthorSuspended):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAuthorSuspended,
			"Your account cannot publish right now", gin.H{"suspended_until": nil})
	case errors.Is(err, service.ErrStandingUnknown):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeStandingUnavailable,
			"Publishing is temporarily unavailable; please try again", nil)
	default:
		return false
	}
	return true
}
