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
	var paused *service.UploadsPausedError
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
	case errors.As(err, &paused):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeUploadsPaused,
			"Long-video uploads are paused on your account", gin.H{"until": paused.Until.UTC().Format(time.RFC3339)})
	case errors.Is(err, service.ErrCopyrightReviewTier):
		writeCopyrightReviewTier(c)
	default:
		return false
	}
	return true
}

// Copyright quick wins (2026-10-24) on the same publication routes:
//
//	403 UPLOADS_PAUSED     details {"until": RFC3339} — three upheld copyright
//	                       blocks in 90 days pause long-video publishing for
//	                       7 days; reels and posts are unaffected
//	409 COPYRIGHT_REVIEW   a members-only tier on a video that is (or would
//	                       be) held or blocked for copyright review
const (
	CodeUploadsPaused   = "UPLOADS_PAUSED"
	CodeCopyrightReview = "COPYRIGHT_REVIEW"
)

func writeCopyrightReviewTier(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, CodeCopyrightReview,
		"This video is in copyright review, so it cannot be members-only", nil)
}
